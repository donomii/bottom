package main

import (
	"testing"
	"time"
)

func TestSameProcessGenerationRejectsRapidStablePIDReuse(t *testing.T) {
	startedAt := time.Unix(1, 0)
	first := Process{ID: "42:100", PID: 42, StartedAt: startedAt}
	second := Process{ID: "42:101", PID: 42, StartedAt: startedAt.Add(10 * time.Millisecond)}
	if sameProcessGeneration(first, second) {
		t.Fatalf("expected distinct stable process identities to remain different despite close start times")
	}
	first.ID = "connector:42:1"
	if !sameProcessGeneration(first, second) {
		t.Fatalf("expected provisional connector identity to align with close stable start time")
	}
}
