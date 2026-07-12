//go:build darwin && cgo && endpointsecurity

package main

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMacOSEndpointSequenceGapUsesGlobalSequence(t *testing.T) {
	sequences := map[EventKind]uint64{}
	global := uint64(20)
	message := macOSEndpointSequenceGap(macOSEndpointNotice{kind: EventExec, sequence: 23, globalSequence: true}, sequences, &global)
	if !strings.Contains(message, "expected sequence 21, received 23") || global != 23 {
		t.Fatalf("expected an exact global sequence gap, received message=%q global=%d", message, global)
	}
}

func TestApplyMacOSEndpointExecPreservesProcessLifetime(t *testing.T) {
	startedAt := time.Unix(10, 0)
	capturedAt := time.Unix(11, 0)
	previous := capturedProcess(processID(7, "10000000000"), 7, 1, "old", "/bin/old", "", "501", startedAt, capturedAt)
	processes := ProcessSnapshot{previous.ID: previous}
	notice := macOSEndpointNotice{
		kind: EventExec, pid: 7, parentPID: 1, uid: "501", startedAt: startedAt,
		eventTime: time.Unix(12, 0), command: "new --arg", exe: "/bin/new",
	}
	events := make(chan Event, 1)

	applyMacOSEndpointNotice(context.Background(), events, processes, notice)

	event := <-events
	if event.Kind != EventExec || event.ProcessID != previous.ID || event.Command != "new --arg" {
		t.Fatalf("expected attributed macOS exec, received %#v", event)
	}
	proc := processes[previous.ID]
	if !proc.CapturedAt.Equal(capturedAt) || !proc.StartedAt.Equal(startedAt) {
		t.Fatalf("expected preserved lifetime, received %#v", proc)
	}
}
