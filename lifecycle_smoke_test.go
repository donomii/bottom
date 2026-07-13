//go:build smoke

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

const lifecycleSmokeHelperEnvironment = "BOTTOM_LIFECYCLE_SMOKE_HELPER"

func TestLifecycleSmokeHelper(t *testing.T) {
	if os.Getenv(lifecycleSmokeHelperEnvironment) != "1" {
		return
	}
	time.Sleep(750 * time.Millisecond)
}

func TestPollingLifecycleSmoke(t *testing.T) {
	runLifecycleSmoke(t, NewPollingBackend(10*time.Millisecond))
}

func runLifecycleSmoke(t *testing.T, backend LifecycleBackend) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan Event, 4096)
	backendResult := make(chan error, 1)
	go func() {
		backendResult <- backend.Watch(ctx, events)
	}()
	select {
	case err := <-backendResult:
		t.Fatalf("start %s lifecycle smoke backend: expected an active watcher, received %v", backend.Name(), err)
	case <-time.After(time.Second):
	}
	command := exec.Command(os.Args[0], "-test.run=^TestLifecycleSmokeHelper$")
	command.Env = append(os.Environ(), lifecycleSmokeHelperEnvironment+"=1")
	if err := command.Start(); err != nil {
		t.Fatalf("start natural-exit lifecycle smoke helper with %s: %v", backend.Name(), err)
	}
	pid := command.Process.Pid
	if err := command.Wait(); err != nil {
		t.Fatalf("wait for natural exit of lifecycle smoke helper pid=%d with %s: %v", pid, backend.Name(), err)
	}
	wantedStart := false
	wantedStop := false
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for !wantedStart || !wantedStop {
		select {
		case event := <-events:
			if event.PID != pid {
				continue
			}
			wantedStart = wantedStart || event.Kind == EventStart
			wantedStop = wantedStop || event.Kind == EventStop
		case err := <-backendResult:
			t.Fatalf("watch lifecycle smoke helper pid=%d with %s: expected start=%t stop=%t, backend ended with %v", pid, backend.Name(), wantedStart, wantedStop, err)
		case <-deadline.C:
			t.Fatalf("watch lifecycle smoke helper pid=%d with %s: expected start and stop events, received start=%t stop=%t", pid, backend.Name(), wantedStart, wantedStop)
		}
	}
	if testing.Verbose() {
		fmt.Printf("lifecycle smoke ok backend=%s pid=%d\n", backend.Name(), pid)
	}
}
