package main

import (
	"strings"
	"time"
)

func processExecEvent(eventTime time.Time, observedAt time.Time, backendName string, proc Process, snapshot ProcessSnapshot) Event {
	event := processEvent(eventTime, observedAt, backendName, proc, snapshot)
	event.Kind = EventExec
	return event
}

func preserveProcessObservation(previous Process, next *Process) {
	if !previous.CapturedAt.IsZero() {
		next.CapturedAt = previous.CapturedAt
	}
	if !previous.StartedAt.IsZero() {
		next.StartedAt = previous.StartedAt
	}
}

func sameProcessGeneration(first Process, second Process) bool {
	if first.PID != second.PID {
		return false
	}
	if first.ID == second.ID {
		return true
	}
	if !provisionalProcessID(first.ID) && !provisionalProcessID(second.ID) {
		return false
	}
	if first.StartedAt.IsZero() || second.StartedAt.IsZero() {
		return false
	}
	difference := first.StartedAt.Sub(second.StartedAt)
	if difference < 0 {
		difference = -difference
	}
	return difference <= 2*time.Second
}

func provisionalProcessID(id string) bool {
	return strings.HasPrefix(id, "connector:") || strings.HasPrefix(id, "es:") || strings.HasPrefix(id, "etw:") || strings.HasPrefix(id, "trace:")
}

func removeProcessByPID(snapshot ProcessSnapshot, pid int) {
	for id, proc := range snapshot {
		if proc.PID == pid {
			delete(snapshot, id)
		}
	}
}
