package main

import (
	"testing"
	"time"
)

type triggerCaptureRecorder struct {
	events  []Event
	closed  bool
	flushes int
}

func (recorder *triggerCaptureRecorder) Write(event Event) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func (recorder *triggerCaptureRecorder) Close() error {
	recorder.closed = true
	return nil
}

func (recorder *triggerCaptureRecorder) Flush() error {
	recorder.flushes++
	return nil
}

func TestTriggeredRecorderFlushesOrderedPreAndPostEvents(t *testing.T) {
	target := &triggerCaptureRecorder{}
	trigger, err := newEventTrigger("churn")
	if err != nil {
		t.Fatalf("create churn trigger: %v", err)
	}
	recorder := newTriggeredRecorder(target, 3, time.Second, trigger)
	base := time.Unix(1, 0)
	for index, kind := range []EventKind{EventStart, EventExec, EventStop, EventChurn} {
		if err := recorder.Write(Event{Kind: kind, Time: base.Add(time.Duration(index) * time.Millisecond), PID: index + 1}); err != nil {
			t.Fatalf("write pre-trigger event %d: %v", index, err)
		}
	}
	if len(target.events) != 3 || target.events[0].Kind != EventExec || target.events[2].Kind != EventChurn {
		t.Fatalf("expected last three pre-trigger events in order, received %#v", target.events)
	}
	if err := recorder.Write(Event{Kind: EventStop, Time: base.Add(500 * time.Millisecond), PID: 5}); err != nil {
		t.Fatalf("write post-trigger event: %v", err)
	}
	if len(target.events) != 4 {
		t.Fatalf("expected active post-trigger event to be written, received %d", len(target.events))
	}
	if err := recorder.Write(Event{Kind: EventStart, Time: base.Add(2 * time.Second), PID: 6}); err != nil {
		t.Fatalf("write rearmed event: %v", err)
	}
	if len(target.events) != 4 {
		t.Fatalf("expected event after post-trigger window to remain buffered, received %d", len(target.events))
	}
	if err := recorder.Close(); err != nil || !target.closed {
		t.Fatalf("expected target to close, received err=%v closed=%t", err, target.closed)
	}
}

func TestEventTriggerValidatesAndMatchesFailureAndRegex(t *testing.T) {
	failure, err := newEventTrigger("failed-exit")
	if err != nil {
		t.Fatalf("create failed-exit trigger: %v", err)
	}
	code := 2
	if !failure.matches(Event{Kind: EventStop, ExitCode: &code}) {
		t.Fatalf("expected non-zero stop event to match failed-exit trigger")
	}
	pattern, err := newEventTrigger(`regex:compile-[0-9]+`)
	if err != nil {
		t.Fatalf("create regular expression trigger: %v", err)
	}
	if !pattern.matches(Event{Command: "worker compile-42"}) {
		t.Fatalf("expected searchable command to match regular expression trigger")
	}
	casePattern, err := newEventTrigger(`regex:Compile-[A-Z]+`)
	if err != nil {
		t.Fatalf("create case-sensitive regular expression trigger: %v", err)
	}
	if !casePattern.matches(Event{Command: "worker Compile-ABC"}) {
		t.Fatalf("expected trigger expression to match original-case command")
	}
	if _, err := newEventTrigger("unknown"); err == nil {
		t.Fatalf("expected unknown trigger to be rejected")
	}
}

func TestTriggeredRecorderAppliesOutputFilterAfterTriggerDecision(t *testing.T) {
	target := &triggerCaptureRecorder{}
	filtered := newFilteringRecorder(target, Filter{Include: []string{"keep"}, EventMode: EventModeAll})
	trigger, err := newEventTrigger("gap")
	if err != nil {
		t.Fatalf("create gap trigger: %v", err)
	}
	recorder := newTriggeredRecorder(filtered, 4, time.Second, trigger)
	base := time.Unix(10, 0)
	for _, event := range []Event{
		{Kind: EventStart, Time: base, Command: "discard before"},
		{Kind: EventStart, Time: base.Add(time.Millisecond), Command: "keep before"},
		{Kind: EventGap, Time: base.Add(2 * time.Millisecond), Message: "capture incomplete"},
		{Kind: EventStop, Time: base.Add(3 * time.Millisecond), Command: "discard after"},
		{Kind: EventExec, Time: base.Add(4 * time.Millisecond), Command: "keep after"},
	} {
		if err := recorder.Write(event); err != nil {
			t.Fatalf("write routed trigger event: %v", err)
		}
	}
	if len(target.events) != 3 || target.events[0].Command != "keep before" || target.events[1].Kind != EventGap || target.events[2].Command != "keep after" {
		t.Fatalf("expected matching events and mandatory gap only, received %#v", target.events)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close routed trigger recorder: %v", err)
	}
}

func TestFilteredChurnStillActivatesTriggeredRecording(t *testing.T) {
	target := &triggerCaptureRecorder{}
	filtered := newFilteringRecorder(target, Filter{EventMode: string(EventStart)})
	trigger, err := newEventTrigger("churn")
	if err != nil {
		t.Fatalf("create churn trigger: %v", err)
	}
	recorder := newTriggeredRecorder(filtered, 3, time.Second, trigger)
	base := time.Unix(20, 0)
	for _, event := range []Event{
		{Kind: EventStart, Time: base, Command: "worker"},
		{Kind: EventStop, Time: base.Add(time.Millisecond), Command: "worker"},
		{Kind: EventChurn, Time: base.Add(2 * time.Millisecond), Command: "worker"},
		{Kind: EventStart, Time: base.Add(3 * time.Millisecond), Command: "worker"},
	} {
		if err := recorder.Write(event); err != nil {
			t.Fatalf("write churn-routed event: %v", err)
		}
	}
	if len(target.events) != 2 || target.events[0].Kind != EventStart || target.events[1].Kind != EventStart {
		t.Fatalf("expected churn to activate output without leaking filtered events, received %#v", target.events)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close churn-routed recorder: %v", err)
	}
}

func TestTriggeredRecorderForwardsFlushThroughTransparentWrappers(t *testing.T) {
	target := &triggerCaptureRecorder{}
	var pipeline Recorder = newSessionRecorder(target, recordingSession{})
	pipeline = newRedactingRecorder(pipeline, []string{"secret"})
	pipeline = newFilteringRecorder(pipeline, Filter{EventMode: EventModeAll})
	trigger, err := newEventTrigger("churn")
	if err != nil {
		t.Fatalf("create churn trigger: %v", err)
	}
	pipeline = newTriggeredRecorder(pipeline, 2, time.Second, trigger)
	flusher, ok := pipeline.(recorderFlusher)
	if !ok {
		t.Fatalf("expected triggered pipeline to support flushing")
	}
	if err := flusher.Flush(); err != nil {
		t.Fatalf("flush triggered pipeline: %v", err)
	}
	if target.flushes != 1 {
		t.Fatalf("expected one forwarded flush, received %d", target.flushes)
	}
	if err := pipeline.Close(); err != nil {
		t.Fatalf("close flushed trigger pipeline: %v", err)
	}
}
