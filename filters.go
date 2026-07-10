package main

import (
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var eventRegexCache = struct {
	sync.RWMutex
	values map[string]*regexp.Regexp
}{values: map[string]*regexp.Regexp{}}

func (filter Filter) Accepts(event Event) bool {
	if !filter.acceptsKind(event.Kind) {
		return false
	}
	if !filter.Since.IsZero() && event.Time.Before(filter.Since) {
		return false
	}
	if !filter.Until.IsZero() && event.Time.After(filter.Until) {
		return false
	}
	if filter.HasExitCode && (event.ExitCode == nil || *event.ExitCode != filter.ExitCode) {
		return false
	}
	if filter.User != "" && event.User != filter.User && event.UID != filter.User {
		return false
	}
	if filter.CwdContains != "" && !strings.Contains(event.Cwd, filter.CwdContains) {
		return false
	}
	if filter.ExeContains != "" && !strings.Contains(event.Exe, filter.ExeContains) {
		return false
	}
	if filter.ContainerContains != "" && !strings.Contains(event.ContainerID, filter.ContainerContains) {
		return false
	}
	if filter.UnitContains != "" && !strings.Contains(event.SystemdUnit, filter.UnitContains) {
		return false
	}
	if filter.ParentPID != 0 && event.ParentPID != filter.ParentPID {
		return false
	}
	if filter.AncestorPID != 0 && !hasAncestor(event, filter.AncestorPID) {
		return false
	}
	if event.Kind == EventStop && filter.MinDuration > 0 && time.Duration(event.DurationMillis)*time.Millisecond < filter.MinDuration {
		return false
	}
	if event.Kind == EventStop && filter.MaxDuration > 0 && time.Duration(event.DurationMillis)*time.Millisecond > filter.MaxDuration {
		return false
	}
	if len(filter.Include) > 0 && !containsOne(eventSearchText(event), filter.Include) {
		return false
	}
	if len(filter.Exclude) > 0 && containsOne(eventSearchText(event), filter.Exclude) {
		return false
	}
	if len(filter.IncludeRegex) > 0 && !matchesOne(eventSearchTextOriginal(event), filter.IncludeRegex) {
		return false
	}
	if len(filter.ExcludeRegex) > 0 && matchesOne(eventSearchTextOriginal(event), filter.ExcludeRegex) {
		return false
	}
	return true
}

func (filter Filter) acceptsKind(kind EventKind) bool {
	if filter.EventMode == "" || filter.EventMode == EventModeBoth || filter.EventMode == EventModeAll {
		return kind == EventStart || kind == EventExec || kind == EventStop || kind == EventChurn || kind == EventGap
	}
	return string(kind) == filter.EventMode
}

func hasAncestor(event Event, pid int) bool {
	if event.ParentPID == pid {
		return true
	}
	for _, parent := range event.ParentChain {
		if parent.PID == pid {
			return true
		}
	}
	return false
}

func containsOne(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func matchesOne(text string, expressions []string) bool {
	for _, expression := range expressions {
		pattern := cachedEventRegex(expression)
		if pattern != nil && pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func cachedEventRegex(expression string) *regexp.Regexp {
	eventRegexCache.RLock()
	pattern := eventRegexCache.values[expression]
	eventRegexCache.RUnlock()
	if pattern != nil {
		return pattern
	}
	compiled, err := regexp.Compile(expression)
	if err != nil {
		return nil
	}
	eventRegexCache.Lock()
	eventRegexCache.values[expression] = compiled
	eventRegexCache.Unlock()
	return compiled
}

func eventSearchText(event Event) string {
	return strings.ToLower(eventSearchTextOriginal(event))
}

func eventSearchTextOriginal(event Event) string {
	parts := []string{
		string(event.Kind),
		event.ProcessID,
		event.Command,
		event.Exe,
		event.Cwd,
		event.User,
		event.UID,
		event.TTY,
		event.Session,
		event.Cgroup,
		event.SystemdUnit,
		event.ContainerID,
		event.Host,
		strconv.Itoa(event.PID),
		strconv.Itoa(event.ParentPID),
		event.Message,
	}
	for _, parent := range event.ParentChain {
		parts = append(parts, parent.Command, parent.Exe, parent.User, strconv.Itoa(parent.PID))
	}
	return strings.Join(parts, "\n")
}
