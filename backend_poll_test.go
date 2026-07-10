package main

import (
	"context"
	"testing"
	"time"
)

func TestPreserveFirstObservationsCarriesOriginalTimes(t *testing.T) {
	firstCaptured := time.Unix(20, 0)
	firstStarted := time.Unix(10, 0)
	previous := ProcessSnapshot{
		"7:1": capturedProcess("7:1", 7, 1, "worker", "/bin/worker", "/", "jer", firstStarted, firstCaptured),
	}
	next := ProcessSnapshot{
		"7:1": capturedProcess("7:1", 7, 1, "worker", "/bin/worker", "/", "jer", firstStarted.Add(time.Second), firstCaptured.Add(time.Second)),
	}

	preserveFirstObservations(previous, next)

	if !next["7:1"].CapturedAt.Equal(firstCaptured) {
		t.Fatalf("expected captured time %s, received %s", firstCaptured, next["7:1"].CapturedAt)
	}
	if !next["7:1"].StartedAt.Equal(firstStarted) {
		t.Fatalf("expected started time %s, received %s", firstStarted, next["7:1"].StartedAt)
	}
}

func TestPreserveFirstObservationsKeepsNewlyAvailableStartTime(t *testing.T) {
	firstCaptured := time.Unix(20, 0)
	discoveredStart := time.Unix(10, 0)
	previous := ProcessSnapshot{
		"7:1": capturedProcess("7:1", 7, 1, "worker", "/bin/worker", "/", "jer", time.Time{}, firstCaptured),
	}
	next := ProcessSnapshot{
		"7:1": capturedProcess("7:1", 7, 1, "worker", "/bin/worker", "/", "jer", discoveredStart, firstCaptured.Add(time.Second)),
	}

	preserveFirstObservations(previous, next)

	if !next["7:1"].StartedAt.Equal(discoveredStart) {
		t.Fatalf("expected newly available started time %s, received %s", discoveredStart, next["7:1"].StartedAt)
	}
}

func TestProcessStopEventUsesOperatingSystemStartTime(t *testing.T) {
	startedAt := time.Unix(10, 0)
	proc := capturedProcess("7:1", 7, 1, "worker", "/bin/worker", "/", "jer", startedAt, time.Unix(19, 0))
	event := processStopEvent(time.Unix(25, 0), BackendPoll, proc, ProcessSnapshot{"7:1": proc}, nil)
	if event.DurationMillis != 15000 {
		t.Fatalf("expected 15000ms duration, received %dms", event.DurationMillis)
	}
}

func TestProcessStartEventSeparatesStartAndObservationTimes(t *testing.T) {
	startedAt := time.Unix(10, 0)
	observedAt := time.Unix(12, 0)
	proc := capturedProcess("7:1", 7, 1, "worker", "/bin/worker", "/", "jer", startedAt, observedAt)
	event := processStartEvent(observedAt, BackendPoll, proc, ProcessSnapshot{"7:1": proc})
	if !event.Time.Equal(startedAt) || !event.ObservedAt.Equal(observedAt) {
		t.Fatalf("expected start %s observed %s, received start=%s observed=%s", startedAt, observedAt, event.Time, event.ObservedAt)
	}
}

func TestSnapshotDiffEmitsExecForStableProcessCommandChange(t *testing.T) {
	startedAt := time.Unix(10, 0)
	capturedAt := time.Unix(20, 0)
	previous := ProcessSnapshot{
		"7:1": capturedProcess("7:1", 7, 1, "worker --mode one", "/bin/worker", "/", "jer", startedAt, capturedAt),
	}
	next := ProcessSnapshot{
		"7:1": capturedProcess("7:1", 7, 1, "worker --mode two", "/bin/worker-v2", "/", "jer", startedAt, capturedAt.Add(time.Second)),
	}
	events := make(chan Event, 1)

	emitSnapshotDiff(context.Background(), BackendPoll, previous, next, events)

	event := <-events
	if event.Kind != EventExec || event.ProcessID != "7:1" || event.Command != "worker --mode two" || event.Exe != "/bin/worker-v2" {
		t.Fatalf("expected exec for changed stable process, received %#v", event)
	}
	select {
	case extra := <-events:
		t.Fatalf("expected only one exec event, received extra %#v", extra)
	default:
	}
}
