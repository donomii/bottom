//go:build linux

package main

import (
	"testing"
	"time"
)

func TestParseOOMKillCount(t *testing.T) {
	count, ok := parseOOMKillCount([]byte("low 0\nhigh 0\noom 3\noom_kill 2\n"))
	if !ok || count != 2 {
		t.Fatalf("expected oom_kill count 2, received count=%d ok=%t", count, ok)
	}
	if _, ok := parseOOMKillCount([]byte("oom_kill invalid\n")); ok {
		t.Fatalf("expected invalid oom_kill count to be rejected")
	}
}

func TestCorrelateMemoryPressureRequiresObservedCounterIncrease(t *testing.T) {
	now := time.Unix(100, 0)
	monitor := &linuxMemoryPressureMonitor{states: map[string]memoryPressureState{
		"/worker": {OOMKills: 3, LastIncreaseAt: now.Add(-time.Second), Known: true},
	}}
	correlator := &eventCorrelator{source: systemCorrelationSource{memoryPressure: monitor.memoryPressure}}
	code := 137
	event := Event{Kind: EventStop, Time: now, DurationMillis: 5000, ExitCode: &code, Cgroup: "/worker"}
	event = correlator.correlateMemoryPressure(event)
	if event.Message == "" {
		t.Fatalf("expected cgroup memory-pressure correlation")
	}
}
