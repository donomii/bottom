package main

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestFilterAcceptsIncludeExcludeAndDuration(t *testing.T) {
	event := Event{
		Kind:           EventStop,
		Time:           time.Unix(1, 0),
		PID:            42,
		ParentPID:      7,
		Command:        "worker --job compile",
		Exe:            "/usr/bin/worker",
		Cwd:            "/tmp/project",
		User:           "jer",
		DurationMillis: 250,
		Backend:        BackendPoll,
	}
	filter := Filter{
		Include:     []string{"compile"},
		Exclude:     []string{"browser"},
		User:        "jer",
		ParentPID:   7,
		EventMode:   string(EventStop),
		MinDuration: 200 * time.Millisecond,
		MaxDuration: 300 * time.Millisecond,
	}
	if !filter.Accepts(event) {
		t.Fatalf("expected filter to accept event")
	}
	filter.Exclude = []string{"worker"}
	if filter.Accepts(event) {
		t.Fatalf("expected filter to reject excluded command")
	}
}

func TestChurnDetectorReportsThreshold(t *testing.T) {
	now := time.Unix(1, 0)
	detector := NewChurnDetector(time.Second, 3)
	event := Event{Kind: EventStart, Time: now, Command: "flapper", Backend: BackendPoll}
	if _, ok := detector.Observe(event); ok {
		t.Fatalf("expected first start to stay below threshold")
	}
	event.Time = now.Add(100 * time.Millisecond)
	if _, ok := detector.Observe(event); ok {
		t.Fatalf("expected second start to stay below threshold")
	}
	event.Time = now.Add(200 * time.Millisecond)
	churn, ok := detector.Observe(event)
	if !ok {
		t.Fatalf("expected third start to report churn")
	}
	if churn.Kind != EventChurn || churn.Count != 3 {
		t.Fatalf("expected churn count 3, received kind=%s count=%d", churn.Kind, churn.Count)
	}
}

func TestSnapshotDiffAddsParentChain(t *testing.T) {
	now := time.Unix(1, 0)
	previous := ProcessSnapshot{
		"1": capturedProcess("1", 1, 0, "parent", "/bin/parent", "/", "jer", time.Time{}, now),
	}
	next := ProcessSnapshot{
		"1": capturedProcess("1", 1, 0, "parent", "/bin/parent", "/", "jer", time.Time{}, now),
		"2": capturedProcess("2", 2, 1, "child", "/bin/child", "/", "jer", time.Time{}, now),
	}
	events := make(chan Event, 1)
	emitSnapshotDiff(context.Background(), BackendPoll, previous, next, events)
	event := <-events
	if event.Kind != EventStart || event.PID != 2 {
		t.Fatalf("expected start for pid 2, received kind=%s pid=%d", event.Kind, event.PID)
	}
	if len(event.ParentChain) != 1 || event.ParentChain[0].PID != 1 {
		t.Fatalf("expected one parent chain entry for pid 1, received %#v", event.ParentChain)
	}
}

func TestTextRecorderWritesCommand(t *testing.T) {
	var output bytes.Buffer
	event := Event{Kind: EventStart, Time: time.Unix(1, 0), PID: 9, Command: "sample", Backend: BackendPoll}
	if err := (textRecorder{writer: &output}).Write(event); err != nil {
		t.Fatalf("write text event: %v", err)
	}
	if output.String() == "" {
		t.Fatalf("expected text output")
	}
}
