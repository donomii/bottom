package main

import (
	"strings"
	"testing"
	"time"
)

func TestConfiguredChurnDetectorGroupsVolatileArgumentsAndRetainsContext(t *testing.T) {
	detector := newChurnDetector(10*time.Second, 2, 5*time.Second, 16, time.Second)
	startedAt := time.Unix(100, 0)
	first := churnLifecycleEvent("one", "/usr/bin/worker --request one", startedAt)
	if _, ok := detector.Observe(first); ok {
		t.Fatalf("expected first start not to report churn")
	}
	firstStop := first
	firstStop.Kind = EventStop
	firstStop.Time = startedAt.Add(100 * time.Millisecond)
	firstStop.DurationMillis = 100
	if _, ok := detector.Observe(firstStop); ok {
		t.Fatalf("expected first short lifetime not to report churn")
	}
	second := churnLifecycleEvent("two", "/usr/bin/worker --request two", startedAt.Add(time.Second))
	detector.Observe(second)
	secondStop := second
	secondStop.Kind = EventStop
	secondStop.Time = second.Time.Add(150 * time.Millisecond)
	secondStop.DurationMillis = 150
	churn, ok := detector.Observe(secondStop)
	if !ok {
		t.Fatalf("expected second short lifetime with a different argument to report churn")
	}
	if churn.Count != 2 || churn.ParentPID != 7 || churn.Cwd != "/work" || churn.SystemdUnit != "worker.service" {
		t.Fatalf("expected count and source context on churn event, received %#v", churn)
	}
	if !strings.Contains(churn.Message, "/usr/bin/worker") {
		t.Fatalf("expected churn message to identify executable group, received %q", churn.Message)
	}
}

func TestConfiguredChurnDetectorIgnoresLongLifetimesAndRepeatsAfterCooldown(t *testing.T) {
	detector := newChurnDetector(20*time.Second, 2, 2*time.Second, 16, time.Second)
	base := time.Unix(200, 0)
	long := churnLifecycleEvent("long", "/usr/bin/worker", base)
	detector.Observe(long)
	longStop := long
	longStop.Kind = EventStop
	longStop.Time = base.Add(2 * time.Second)
	longStop.DurationMillis = 2000
	if _, ok := detector.Observe(longStop); ok {
		t.Fatalf("expected long lifetime not to count toward restart churn")
	}
	for index, offset := range []time.Duration{3 * time.Second, 4 * time.Second} {
		event := churnLifecycleEvent(string(rune('a'+index)), "/usr/bin/worker", base.Add(offset))
		detector.Observe(event)
		event.Kind = EventStop
		event.Time = event.Time.Add(100 * time.Millisecond)
		event.DurationMillis = 100
		_, reported := detector.Observe(event)
		if index == 0 && reported {
			t.Fatalf("expected first counted short lifetime to stay below threshold")
		}
		if index == 1 && !reported {
			t.Fatalf("expected threshold crossing to report churn")
		}
	}
	third := churnLifecycleEvent("third", "/usr/bin/worker", base.Add(7*time.Second))
	detector.Observe(third)
	third.Kind = EventStop
	third.Time = third.Time.Add(100 * time.Millisecond)
	third.DurationMillis = 100
	if _, ok := detector.Observe(third); !ok {
		t.Fatalf("expected sustained churn to report after cooldown")
	}
}

func TestChurnDetectorBoundsDistinctGroups(t *testing.T) {
	detector := newChurnDetector(time.Minute, 10, time.Second, 2, 0)
	base := time.Unix(300, 0)
	for index, executable := range []string{"/bin/a", "/bin/b", "/bin/c"} {
		event := churnLifecycleEvent(executable, executable, base.Add(time.Duration(index)*time.Second))
		event.Exe = executable
		detector.Observe(event)
	}
	if len(detector.groups) != 2 {
		t.Fatalf("expected at most two churn groups, received %d", len(detector.groups))
	}
}

func TestConfiguredChurnDetectorStoresLifecycleStartTimes(t *testing.T) {
	detector := newChurnDetector(10*time.Second, 2, time.Second, 16, 5*time.Second)
	startedAt := time.Unix(400, 0)
	start := churnLifecycleEvent("one", "/usr/bin/worker", startedAt)
	detector.Observe(start)
	stop := start
	stop.Kind = EventStop
	stop.Time = startedAt.Add(750 * time.Millisecond)
	stop.DurationMillis = 750
	if _, ok := detector.Observe(stop); ok {
		t.Fatalf("expected first qualifying lifetime to stay below threshold")
	}
	group := detector.groups[churnGroupKey(stop)]
	if group == nil || len(group.starts) != 1 || !group.starts[0].Equal(startedAt) {
		t.Fatalf("expected churn window to retain start time %s, received %#v", startedAt, group)
	}

	stopOnlyDetector := newChurnDetector(10*time.Second, 2, time.Second, 16, 5*time.Second)
	stopOnly := churnLifecycleEvent("stop-only", "/usr/bin/worker", startedAt.Add(3*time.Second))
	stopOnly.Kind = EventStop
	stopOnly.DurationMillis = 500
	stopOnlyDetector.Observe(stopOnly)
	stopOnlyGroup := stopOnlyDetector.groups[churnGroupKey(stopOnly)]
	expectedStart := stopOnly.Time.Add(-500 * time.Millisecond)
	if stopOnlyGroup == nil || len(stopOnlyGroup.starts) != 1 || !stopOnlyGroup.starts[0].Equal(expectedStart) {
		t.Fatalf("expected stop-only lifetime to derive start time %s, received %#v", expectedStart, stopOnlyGroup)
	}
}

func TestStartBasedChurnDoesNotCountStopsTwice(t *testing.T) {
	detector := newChurnDetector(10*time.Second, 3, 0, 16, 0)
	base := time.Unix(500, 0)
	for index := 0; index < 2; index++ {
		start := churnLifecycleEvent(string(rune('a'+index)), "/usr/bin/worker", base.Add(time.Duration(index)*time.Second))
		if _, reported := detector.Observe(start); reported {
			t.Fatalf("expected start %d to stay below threshold", index+1)
		}
		stop := start
		stop.Kind = EventStop
		stop.Time = start.Time.Add(100 * time.Millisecond)
		stop.DurationMillis = 100
		if _, reported := detector.Observe(stop); reported {
			t.Fatalf("expected stop %d not to count as another start", index+1)
		}
	}
	third := churnLifecycleEvent("c", "/usr/bin/worker", base.Add(2*time.Second))
	if churn, reported := detector.Observe(third); !reported || churn.Count != 3 {
		t.Fatalf("expected the third distinct start to report count 3, received event=%#v reported=%t", churn, reported)
	}
}

func churnLifecycleEvent(id string, command string, eventTime time.Time) Event {
	return Event{
		Kind:        EventStart,
		Time:        eventTime,
		ProcessID:   id,
		PID:         100,
		ParentPID:   7,
		Command:     command,
		Exe:         "/usr/bin/worker",
		Cwd:         "/work",
		UID:         "1000",
		SystemdUnit: "worker.service",
		Backend:     BackendPoll,
	}
}
