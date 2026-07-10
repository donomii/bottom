package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type churnGroup struct {
	key         string
	starts      []time.Time
	lastTouched time.Time
	lastReport  time.Time
	exemplar    Event
	previous    *churnGroup
	next        *churnGroup
}

type activeProcess struct {
	startedAt time.Time
	event     Event
}

type processGroupIdentity struct {
	executable string
	parent     string
	owner      string
	unit       string
	container  string
}

type ChurnDetector struct {
	window      time.Duration
	threshold   int
	cooldown    time.Duration
	maxKeys     int
	maxLifetime time.Duration
	groups      map[string]*churnGroup
	active      map[string]activeProcess
	oldest      *churnGroup
	newest      *churnGroup
	nextPrune   time.Time
}

func NewChurnDetector(window time.Duration, threshold int) *ChurnDetector {
	return newChurnDetector(window, threshold, window, 4096, 0)
}

func NewConfiguredChurnDetector(config Config) *ChurnDetector {
	return newChurnDetector(config.ChurnWindow, config.ChurnThreshold, config.ChurnCooldown, config.ChurnMaxKeys, config.ChurnMaxLife)
}

func newChurnDetector(window time.Duration, threshold int, cooldown time.Duration, maxKeys int, maxLifetime time.Duration) *ChurnDetector {
	return &ChurnDetector{
		window:      window,
		threshold:   threshold,
		cooldown:    cooldown,
		maxKeys:     maxKeys,
		maxLifetime: maxLifetime,
		groups:      map[string]*churnGroup{},
		active:      map[string]activeProcess{},
	}
}

func (detector *ChurnDetector) Observe(event Event) (Event, bool) {
	detector.prune(event.Time)
	switch event.Kind {
	case EventStart:
		if detector.maxLifetime == 0 {
			return detector.recordStartAt(event, event.Time)
		}
		detector.active[activeProcessKey(event)] = activeProcess{startedAt: event.Time, event: event}
		return Event{}, false
	case EventExec:
		key := activeProcessKey(event)
		active, ok := detector.active[key]
		if ok {
			active.event = event
			detector.active[key] = active
		}
		return Event{}, false
	case EventStop:
		if detector.maxLifetime == 0 {
			return Event{}, false
		}
		return detector.observeStop(event)
	default:
		return Event{}, false
	}
}

func (detector *ChurnDetector) observeStop(event Event) (Event, bool) {
	activeKey := activeProcessKey(event)
	active, ok := detector.active[activeKey]
	delete(detector.active, activeKey)
	exemplar := event
	startedAt := event.Time
	duration := time.Duration(event.DurationMillis) * time.Millisecond
	if ok {
		exemplar = mergeLifecycleEvent(active.event, event)
		startedAt = active.startedAt
		if duration == 0 && !active.startedAt.IsZero() {
			duration = event.Time.Sub(active.startedAt)
		}
	} else if duration > 0 {
		startedAt = event.Time.Add(-duration)
	}
	if detector.maxLifetime > 0 && (duration < 0 || duration > detector.maxLifetime) {
		return Event{}, false
	}
	return detector.recordStartAt(exemplar, startedAt)
}

func (detector *ChurnDetector) recordStartAt(event Event, startedAt time.Time) (Event, bool) {
	key := churnGroupKey(event)
	if key == "" {
		return Event{}, false
	}
	group := detector.groups[key]
	if group == nil {
		detector.makeRoom()
		group = &churnGroup{key: key}
		detector.groups[key] = group
	}
	cutoff := event.Time.Add(-detector.window)
	kept := group.starts[:0]
	for _, seenAt := range group.starts {
		if !seenAt.Before(cutoff) {
			kept = append(kept, seenAt)
		}
	}
	group.starts = kept
	if !startedAt.Before(cutoff) {
		group.starts = append(group.starts, startedAt)
	}
	group.lastTouched = event.Time
	group.exemplar = event
	detector.touchGroup(group)
	if len(group.starts) < detector.threshold {
		return Event{}, false
	}
	if !group.lastReport.IsZero() && event.Time.Sub(group.lastReport) < detector.cooldown {
		return Event{}, false
	}
	group.lastReport = event.Time
	return churnEventFrom(event, key, len(group.starts), detector.window), true
}

func (detector *ChurnDetector) prune(now time.Time) {
	if now.IsZero() || !detector.nextPrune.IsZero() && now.Before(detector.nextPrune) {
		return
	}
	pruneInterval := detector.window / 4
	if pruneInterval < 100*time.Millisecond {
		pruneInterval = 100 * time.Millisecond
	}
	if pruneInterval > time.Second {
		pruneInterval = time.Second
	}
	detector.nextPrune = now.Add(pruneInterval)
	groupCutoff := now.Add(-(detector.window + detector.cooldown))
	for key, group := range detector.groups {
		if group.lastTouched.Before(groupCutoff) {
			detector.deleteGroup(key, group)
		}
	}
	activeAge := detector.window + detector.maxLifetime
	if activeAge <= 0 {
		activeAge = detector.window * 2
	}
	activeCutoff := now.Add(-activeAge)
	for key, active := range detector.active {
		if active.startedAt.Before(activeCutoff) {
			delete(detector.active, key)
		}
	}
}

func (detector *ChurnDetector) makeRoom() {
	if len(detector.groups) < detector.maxKeys {
		return
	}
	if detector.oldest != nil {
		detector.deleteGroup(detector.oldest.key, detector.oldest)
	}
}

func (detector *ChurnDetector) touchGroup(group *churnGroup) {
	if detector.newest == group {
		return
	}
	detector.unlinkGroup(group)
	group.previous = detector.newest
	group.next = nil
	if detector.newest != nil {
		detector.newest.next = group
	} else {
		detector.oldest = group
	}
	detector.newest = group
}

func (detector *ChurnDetector) deleteGroup(key string, group *churnGroup) {
	detector.unlinkGroup(group)
	delete(detector.groups, key)
}

func (detector *ChurnDetector) unlinkGroup(group *churnGroup) {
	if group.previous != nil {
		group.previous.next = group.next
	} else if detector.oldest == group {
		detector.oldest = group.next
	}
	if group.next != nil {
		group.next.previous = group.previous
	} else if detector.newest == group {
		detector.newest = group.previous
	}
	group.previous = nil
	group.next = nil
}

func activeProcessKey(event Event) string {
	if event.ProcessID != "" {
		return event.ProcessID
	}
	return strconv.Itoa(event.PID)
}

func churnGroupKey(event Event) string {
	return processGroupIdentityForEvent(event).key()
}

func processGroupIdentityForEvent(event Event) processGroupIdentity {
	executable := event.Exe
	if executable == "" {
		fields := strings.Fields(event.Command)
		if len(fields) > 0 {
			executable = fields[0]
		}
	}
	parent := strconv.Itoa(event.ParentPID)
	if len(event.ParentChain) > 0 {
		if event.ParentChain[0].ProcessID != "" {
			parent = event.ParentChain[0].ProcessID
		} else if event.ParentChain[0].Exe != "" {
			parent = event.ParentChain[0].Exe
		}
	}
	owner := event.UID
	if owner == "" {
		owner = event.User
	}
	return processGroupIdentity{executable: executable, parent: parent, owner: owner, unit: event.SystemdUnit, container: event.ContainerID}
}

func (identity processGroupIdentity) key() string {
	if identity.executable == "" {
		return ""
	}
	return strings.Join([]string{identity.executable, identity.parent, identity.owner, identity.unit, identity.container}, "\x00")
}

func (identity processGroupIdentity) label() string {
	if identity.executable == "" {
		return ""
	}
	parts := []string{identity.executable, "parent=" + identity.parent}
	if identity.owner != "" {
		parts = append(parts, "owner="+identity.owner)
	}
	if identity.unit != "" {
		parts = append(parts, "unit="+identity.unit)
	}
	if identity.container != "" {
		parts = append(parts, "container="+identity.container)
	}
	return strings.Join(parts, " | ")
}

func mergeLifecycleEvent(start Event, stop Event) Event {
	merged := stop
	if merged.ProcessID == "" {
		merged.ProcessID = start.ProcessID
	}
	if merged.Command == "" {
		merged.Command = start.Command
	}
	if merged.Exe == "" {
		merged.Exe = start.Exe
	}
	if merged.Cwd == "" {
		merged.Cwd = start.Cwd
	}
	if merged.User == "" {
		merged.User = start.User
	}
	if merged.UID == "" {
		merged.UID = start.UID
	}
	if merged.TTY == "" {
		merged.TTY = start.TTY
	}
	if merged.Session == "" {
		merged.Session = start.Session
	}
	if merged.Cgroup == "" {
		merged.Cgroup = start.Cgroup
	}
	if merged.SystemdUnit == "" {
		merged.SystemdUnit = start.SystemdUnit
	}
	if merged.ContainerID == "" {
		merged.ContainerID = start.ContainerID
	}
	if len(merged.ParentChain) == 0 {
		merged.ParentChain = start.ParentChain
	}
	return merged
}

func churnEventFrom(event Event, key string, count int, window time.Duration) Event {
	event.Kind = EventChurn
	event.Count = count
	event.WindowMillis = window.Milliseconds()
	event.Message = fmt.Sprintf("process group %q reached %d short-lived starts inside %s", strings.ReplaceAll(key, "\x00", " | "), count, window)
	return event
}
