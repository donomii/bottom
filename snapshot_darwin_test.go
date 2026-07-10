//go:build darwin

package main

import (
	"os"
	"testing"
)

func TestDarwinNativeSnapshotIdentifiesCurrentProcess(t *testing.T) {
	snapshot, err := ReadProcessSnapshot()
	if err != nil {
		t.Fatalf("read native macOS snapshot: %v", err)
	}
	proc, found := findProcessByPID(snapshot, os.Getpid())
	if !found {
		t.Fatalf("expected current process %d in native macOS snapshot", os.Getpid())
	}
	if proc.ID == "" || proc.StartedAt.IsZero() || proc.UID == "" {
		t.Fatalf("expected stable identity, start time, and UID, received %#v", proc)
	}
}
