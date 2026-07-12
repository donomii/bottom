package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestEventCorrelatorReportsImmediateServiceRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	correlator := newEventCorrelator(ctx, time.Second)
	now := time.Unix(100, 0)
	stop := Event{Kind: EventStop, Time: now, PID: 10, SystemdUnit: "worker.service", Backend: BackendPoll}
	start := Event{Kind: EventStart, Time: now.Add(2 * time.Second), PID: 11, SystemdUnit: "worker.service", Backend: BackendPoll}
	correlator.Observe(stop)
	_, correlated := correlator.Observe(start)
	if len(correlated) != 1 || correlated[0].Kind != EventRestart || correlated[0].Count != 1 {
		t.Fatalf("expected one service restart event, received %#v", correlated)
	}
	if !strings.Contains(correlated[0].Message, "pid 10 stopped") {
		t.Fatalf("expected stopped process attribution, received %q", correlated[0].Message)
	}
}

func TestEventCorrelatorDoesNotJoinLateServiceStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	correlator := newEventCorrelator(ctx, time.Second)
	now := time.Unix(100, 0)
	correlator.Observe(Event{Kind: EventStop, Time: now, PID: 10, SystemdUnit: "worker.service", Backend: BackendPoll})
	_, correlated := correlator.Observe(Event{Kind: EventStart, Time: now.Add(serviceRestartWindow + time.Second), PID: 11, SystemdUnit: "worker.service", Backend: BackendPoll})
	if len(correlated) != 0 {
		t.Fatalf("expected late service start to remain uncorrelated, received %#v", correlated)
	}
}
