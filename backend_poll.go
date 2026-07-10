package main

import (
	"context"
	"fmt"
	"time"
)

type PollingBackend struct {
	interval time.Duration
}

func NewPollingBackend(interval time.Duration) PollingBackend {
	return PollingBackend{interval: interval}
}

func (backend PollingBackend) Name() string {
	return BackendPoll
}

func (backend PollingBackend) Watch(ctx context.Context, events chan<- Event) error {
	previous, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot for polling backend: %w", err)
	}
	ticker := time.NewTicker(backend.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			next, err := ReadProcessSnapshot()
			if err != nil {
				now := time.Now()
				sendEvent(ctx, events, Event{
					Kind:       EventGap,
					Time:       now,
					ObservedAt: now,
					Backend:    backend.Name(),
					Message:    fmt.Sprintf("process snapshot failed; expected a complete process table, received error %v", err),
				})
				continue
			}
			emitSnapshotDiff(ctx, backend.Name(), previous, next, events)
			previous = next
		}
	}
}

func emitSnapshotDiff(ctx context.Context, backendName string, previous ProcessSnapshot, next ProcessSnapshot, events chan<- Event) {
	emitSnapshotDiffWithExitCodes(ctx, backendName, previous, next, events, map[int]int{})
}

func emitSnapshotDiffWithExitCodes(ctx context.Context, backendName string, previous ProcessSnapshot, next ProcessSnapshot, events chan<- Event, exitCodes map[int]int) {
	preserveFirstObservations(previous, next)
	now := time.Now()
	previousByPID := indexProcessesByPID(previous)
	nextByPID := indexProcessesByPID(next)
	for id, proc := range next {
		previousProcess, ok := previous[id]
		if ok {
			if previousProcess.Command != proc.Command || previousProcess.Exe != proc.Exe {
				event := processEventIndexed(now, now, backendName, proc, nextByPID)
				event.Kind = EventExec
				sendEvent(ctx, events, event)
			}
			continue
		}
		sendEvent(ctx, events, processStartEventIndexed(now, backendName, proc, nextByPID))
	}
	for id, proc := range previous {
		if _, ok := next[id]; ok {
			continue
		}
		code, hasCode := exitCodes[proc.PID]
		if hasCode {
			sendEvent(ctx, events, processStopEventIndexed(now, backendName, proc, previousByPID, &code))
		} else {
			sendEvent(ctx, events, processStopEventIndexed(now, backendName, proc, previousByPID, nil))
		}
	}
}

func preserveFirstObservations(previous ProcessSnapshot, next ProcessSnapshot) {
	for id, nextProcess := range next {
		previousProcess, ok := previous[id]
		if !ok {
			continue
		}
		if !previousProcess.CapturedAt.IsZero() {
			nextProcess.CapturedAt = previousProcess.CapturedAt
		}
		if !previousProcess.StartedAt.IsZero() {
			nextProcess.StartedAt = previousProcess.StartedAt
		}
		next[id] = nextProcess
	}
}

func processStartEvent(now time.Time, backendName string, proc Process, snapshot ProcessSnapshot) Event {
	return processStartEventIndexed(now, backendName, proc, indexProcessesByPID(snapshot))
}

func processStartEventIndexed(now time.Time, backendName string, proc Process, processesByPID map[int]Process) Event {
	eventTime := now
	if !proc.StartedAt.IsZero() {
		eventTime = proc.StartedAt
	}
	event := processEventIndexed(eventTime, now, backendName, proc, processesByPID)
	event.Kind = EventStart
	return event
}

func processStartEventObserved(eventTime time.Time, observedAt time.Time, backendName string, proc Process, snapshot ProcessSnapshot) Event {
	event := processEvent(eventTime, observedAt, backendName, proc, snapshot)
	event.Kind = EventStart
	return event
}

func processStopEvent(now time.Time, backendName string, proc Process, snapshot ProcessSnapshot, exitCode *int) Event {
	return processStopEventIndexed(now, backendName, proc, indexProcessesByPID(snapshot), exitCode)
}

func processStopEventIndexed(now time.Time, backendName string, proc Process, processesByPID map[int]Process, exitCode *int) Event {
	duration := now.Sub(proc.CapturedAt)
	if !proc.StartedAt.IsZero() {
		duration = now.Sub(proc.StartedAt)
	}
	if duration < 0 {
		duration = 0
	}
	event := processEventIndexed(now, now, backendName, proc, processesByPID)
	event.Kind = EventStop
	event.DurationMillis = duration.Milliseconds()
	event.ExitCode = exitCode
	return event
}

func processStopEventObserved(eventTime time.Time, observedAt time.Time, backendName string, proc Process, snapshot ProcessSnapshot, exitCode *int) Event {
	duration := eventTime.Sub(proc.CapturedAt)
	if !proc.StartedAt.IsZero() {
		duration = eventTime.Sub(proc.StartedAt)
	}
	if duration < 0 {
		duration = 0
	}
	event := processEvent(eventTime, observedAt, backendName, proc, snapshot)
	event.Kind = EventStop
	event.DurationMillis = duration.Milliseconds()
	event.ExitCode = exitCode
	return event
}

func processEvent(eventTime time.Time, observedAt time.Time, backendName string, proc Process, snapshot ProcessSnapshot) Event {
	return processEventIndexed(eventTime, observedAt, backendName, proc, indexProcessesByPID(snapshot))
}

func processEventIndexed(eventTime time.Time, observedAt time.Time, backendName string, proc Process, processesByPID map[int]Process) Event {
	return Event{
		Time:        eventTime,
		ObservedAt:  observedAt,
		ProcessID:   proc.ID,
		PID:         proc.PID,
		ParentPID:   proc.ParentPID,
		Command:     proc.Command,
		Exe:         proc.Exe,
		Cwd:         proc.Cwd,
		User:        proc.User,
		UID:         proc.UID,
		TTY:         proc.TTY,
		Session:     proc.Session,
		Cgroup:      proc.Cgroup,
		SystemdUnit: proc.SystemdUnit,
		ContainerID: proc.ContainerID,
		Backend:     backendName,
		ParentChain: buildParentChainIndexed(proc, processesByPID),
	}
}

func sendEvent(ctx context.Context, events chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}

func buildParentChain(proc Process, snapshot ProcessSnapshot) []ProcessSummary {
	return buildParentChainIndexed(proc, indexProcessesByPID(snapshot))
}

func buildParentChainIndexed(proc Process, processesByPID map[int]Process) []ProcessSummary {
	parents := []ProcessSummary{}
	seen := map[int]bool{proc.PID: true}
	parentPID := proc.ParentPID
	for parentPID > 0 {
		if seen[parentPID] {
			break
		}
		parent, ok := processesByPID[parentPID]
		if !ok {
			break
		}
		parents = append(parents, ProcessSummary{
			PID:       parent.PID,
			ProcessID: parent.ID,
			Command:   parent.Command,
			Exe:       parent.Exe,
			User:      parent.User,
		})
		seen[parentPID] = true
		parentPID = parent.ParentPID
	}
	return parents
}

func indexProcessesByPID(snapshot ProcessSnapshot) map[int]Process {
	processes := make(map[int]Process, len(snapshot))
	for _, proc := range snapshot {
		processes[proc.PID] = proc
	}
	return processes
}

func findProcessByPID(snapshot ProcessSnapshot, pid int) (Process, bool) {
	for _, proc := range snapshot {
		if proc.PID == pid {
			return proc, true
		}
	}
	return Process{}, false
}
