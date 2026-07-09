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
				sendEvent(ctx, events, Event{
					Kind:    EventGap,
					Time:    time.Now(),
					Backend: backend.Name(),
					Message: fmt.Sprintf("process snapshot failed; expected a complete process table, received error %v", err),
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
	now := time.Now()
	for id, proc := range next {
		if _, ok := previous[id]; ok {
			continue
		}
		sendEvent(ctx, events, processStartEvent(now, backendName, proc, next))
	}
	for id, proc := range previous {
		if _, ok := next[id]; ok {
			continue
		}
		code, hasCode := exitCodes[proc.PID]
		if hasCode {
			sendEvent(ctx, events, processStopEvent(now, backendName, proc, previous, &code))
		} else {
			sendEvent(ctx, events, processStopEvent(now, backendName, proc, previous, nil))
		}
	}
}

func processStartEvent(now time.Time, backendName string, proc Process, snapshot ProcessSnapshot) Event {
	return Event{
		Kind:        EventStart,
		Time:        now,
		PID:         proc.PID,
		ParentPID:   proc.ParentPID,
		Command:     proc.Command,
		Exe:         proc.Exe,
		Cwd:         proc.Cwd,
		User:        proc.User,
		Backend:     backendName,
		ParentChain: buildParentChain(proc, snapshot),
	}
}

func processStopEvent(now time.Time, backendName string, proc Process, snapshot ProcessSnapshot, exitCode *int) Event {
	duration := now.Sub(proc.CapturedAt)
	if !proc.StartedAt.IsZero() {
		duration = now.Sub(proc.StartedAt)
	}
	if duration < 0 {
		duration = 0
	}
	return Event{
		Kind:           EventStop,
		Time:           now,
		PID:            proc.PID,
		ParentPID:      proc.ParentPID,
		Command:        proc.Command,
		Exe:            proc.Exe,
		Cwd:            proc.Cwd,
		User:           proc.User,
		DurationMillis: duration.Milliseconds(),
		ExitCode:       exitCode,
		Backend:        backendName,
		ParentChain:    buildParentChain(proc, snapshot),
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
	parents := []ProcessSummary{}
	seen := map[int]bool{proc.PID: true}
	parentPID := proc.ParentPID
	for parentPID > 0 {
		if seen[parentPID] {
			break
		}
		parent, ok := findProcessByPID(snapshot, parentPID)
		if !ok {
			break
		}
		parents = append(parents, ProcessSummary{
			PID:     parent.PID,
			Command: parent.Command,
			Exe:     parent.Exe,
			User:    parent.User,
		})
		seen[parentPID] = true
		parentPID = parent.ParentPID
	}
	return parents
}

func findProcessByPID(snapshot ProcessSnapshot, pid int) (Process, bool) {
	for _, proc := range snapshot {
		if proc.PID == pid {
			return proc, true
		}
	}
	return Process{}, false
}
