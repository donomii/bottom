package main

import (
	"context"
	"fmt"
	"time"
)

const (
	serviceRestartWindow  = 30 * time.Second
	serviceRestartMaxKeys = 4096
)

type memoryPressureState struct {
	OOMKills       uint64
	LastIncreaseAt time.Time
	Known          bool
}

type systemCorrelationSource struct {
	register       func(string) bool
	memoryPressure func(string) (memoryPressureState, bool)
}

type serviceRestartState struct {
	lastStop Event
	restarts []time.Time
	touched  time.Time
}

type eventCorrelator struct {
	window   time.Duration
	maxKeys  int
	services map[string]*serviceRestartState
	source   systemCorrelationSource
}

func newEventCorrelator(ctx context.Context, interval time.Duration) *eventCorrelator {
	return &eventCorrelator{
		window: serviceRestartWindow, maxKeys: serviceRestartMaxKeys,
		services: map[string]*serviceRestartState{}, source: newSystemCorrelationSource(ctx, interval),
	}
}

func (correlator *eventCorrelator) Observe(event Event) (Event, []Event) {
	correlator.registerCgroup(event.Cgroup)
	if event.Kind == EventStop {
		event = correlator.correlateMemoryPressure(event)
		correlator.rememberServiceStop(event)
		return event, nil
	}
	if event.Kind == EventStart {
		if restart, ok := correlator.correlateServiceStart(event); ok {
			return event, []Event{restart}
		}
	}
	return event, nil
}

func (correlator *eventCorrelator) registerCgroup(cgroup string) {
	if cgroup != "" && correlator.source.register != nil {
		correlator.source.register(cgroup)
	}
}

func (correlator *eventCorrelator) correlateMemoryPressure(event Event) Event {
	if event.ExitCode == nil || *event.ExitCode != 137 || event.Cgroup == "" || correlator.source.memoryPressure == nil {
		return event
	}
	state, ok := correlator.source.memoryPressure(event.Cgroup)
	startedAt := event.Time.Add(-time.Duration(event.DurationMillis) * time.Millisecond)
	if !ok || state.LastIncreaseAt.Before(startedAt) || state.LastIncreaseAt.After(event.Time.Add(time.Second)) {
		return event
	}
	detail := fmt.Sprintf("memory-pressure termination correlated with cgroup oom_kill counter=%d observed_at=%s", state.OOMKills, state.LastIncreaseAt.Format(time.RFC3339Nano))
	if event.Message == "" {
		event.Message = detail
	} else {
		event.Message += "; " + detail
	}
	return event
}

func (correlator *eventCorrelator) rememberServiceStop(event Event) {
	if event.SystemdUnit == "" {
		return
	}
	state := correlator.serviceState(event.SystemdUnit, event.Time)
	state.lastStop = event
	state.touched = event.Time
}

func (correlator *eventCorrelator) correlateServiceStart(event Event) (Event, bool) {
	state := correlator.services[event.SystemdUnit]
	if event.SystemdUnit == "" || state == nil || state.lastStop.Time.IsZero() {
		return Event{}, false
	}
	delay := event.Time.Sub(state.lastStop.Time)
	if delay < 0 || delay > correlator.window {
		return Event{}, false
	}
	cutoff := event.Time.Add(-correlator.window)
	kept := state.restarts[:0]
	for _, restartAt := range state.restarts {
		if !restartAt.Before(cutoff) {
			kept = append(kept, restartAt)
		}
	}
	state.restarts = append(kept, event.Time)
	state.touched = event.Time
	stoppedPID := state.lastStop.PID
	state.lastStop = Event{}
	restart := event
	restart.Kind = EventRestart
	restart.Count = len(state.restarts)
	restart.WindowMillis = correlator.window.Milliseconds()
	restart.Message = fmt.Sprintf("service restart activity: unit %s started pid %d after pid %d stopped %s earlier", event.SystemdUnit, event.PID, stoppedPID, delay)
	return restart, true
}

func (correlator *eventCorrelator) serviceState(unit string, now time.Time) *serviceRestartState {
	state := correlator.services[unit]
	if state != nil {
		return state
	}
	if len(correlator.services) >= correlator.maxKeys {
		correlator.evictOldestService()
	}
	state = &serviceRestartState{touched: now}
	correlator.services[unit] = state
	return state
}

func (correlator *eventCorrelator) evictOldestService() {
	oldestUnit := ""
	var oldestTime time.Time
	for unit, state := range correlator.services {
		if oldestTime.IsZero() || state.touched.Before(oldestTime) {
			oldestUnit = unit
			oldestTime = state.touched
		}
	}
	delete(correlator.services, oldestUnit)
}
