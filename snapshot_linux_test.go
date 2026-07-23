//go:build linux

package main

import (
	"os"
	"testing"
	"time"
)

func TestLinuxProcessStartedAtConvertsClockTicks(t *testing.T) {
	bootedAt := time.Unix(100, 0)
	clock := linuxProcessClock{bootedAt: bootedAt, valid: true}
	startedAt := clock.processStartedAt("250", time.Unix(110, 0))
	expected := time.Unix(102, 500000000)
	if !startedAt.Equal(expected) {
		t.Fatalf("expected start time %s, received %s", expected, startedAt)
	}
}

func TestLinuxProcessStartedAtRejectsInvalidAndFutureTokens(t *testing.T) {
	clock := linuxProcessClock{bootedAt: time.Unix(100, 0), valid: true}
	if startedAt := clock.processStartedAt("invalid", time.Unix(110, 0)); !startedAt.IsZero() {
		t.Fatalf("expected invalid token to have no start time, received %s", startedAt)
	}
	if startedAt := clock.processStartedAt("1300", time.Unix(110, 0)); !startedAt.IsZero() {
		t.Fatalf("expected future token to have no start time, received %s", startedAt)
	}
}

func TestReadProcessSnapshotIncludesCurrentProcessStartTime(t *testing.T) {
	snapshot, err := ReadProcessSnapshot()
	if err != nil {
		t.Fatalf("read process snapshot: %v", err)
	}
	proc, ok := findProcessByPID(snapshot, os.Getpid())
	if !ok {
		t.Fatalf("expected snapshot to include pid %d", os.Getpid())
	}
	if proc.StartedAt.IsZero() {
		t.Fatalf("expected pid %d to have an operating-system start time", os.Getpid())
	}
	if proc.StartedAt.After(proc.CapturedAt.Add(time.Second)) {
		t.Fatalf("expected start time %s not to follow capture time %s", proc.StartedAt, proc.CapturedAt)
	}
	if proc.UID == "" || proc.Session == "" {
		t.Fatalf("expected pid %d to include uid and session, received uid=%q session=%q", os.Getpid(), proc.UID, proc.Session)
	}
}
