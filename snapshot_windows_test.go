//go:build windows

package main

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsNativeSnapshotIdentifiesCurrentProcess(t *testing.T) {
	snapshot, err := ReadProcessSnapshot()
	if err != nil {
		t.Fatalf("read native Windows snapshot: %v", err)
	}
	proc, found := findProcessByPID(snapshot, os.Getpid())
	if !found {
		t.Fatalf("expected current process %d in native Windows snapshot", os.Getpid())
	}
	if proc.ID == "" || proc.Exe == "" || proc.StartedAt.IsZero() ||
		proc.UID == "" || proc.User == "" {
		t.Fatalf("expected stable identity, executable, start time, and owner, received %#v", proc)
	}
	if !strings.Contains(proc.Command, "-test") {
		t.Fatalf("expected current process command line with test arguments, received %q", proc.Command)
	}
}
