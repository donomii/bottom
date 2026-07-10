//go:build windows

package main

import (
	"os"
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
	if proc.ID == "" || proc.Exe == "" || proc.StartedAt.IsZero() {
		t.Fatalf("expected stable identity, executable, and start time, received %#v", proc)
	}
}
