package main

import (
	"errors"
	"flag"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestParseTraceConfigRequiresExactCommandBoundary(t *testing.T) {
	config, err := parseTraceConfig([]string{"-poll", "5ms", "-ppid", "-tail", "1s", "--", "go", "test", "./..."})
	if err != nil {
		t.Fatalf("parse trace config: %v", err)
	}
	if config.PollInterval != 5*time.Millisecond || !config.ShowPPID || config.Tail != time.Second || strings.Join(config.Command, " ") != "go test ./..." {
		t.Fatalf("expected parsed trace settings and command, received %#v", config)
	}
	if _, err := parseTraceConfig([]string{"go", "test"}); err == nil {
		t.Fatalf("expected trace command without -- boundary to be rejected")
	}
	if _, err := parseTraceConfig([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected trace help without a command boundary, received %v", err)
	}
}

func TestSelectTracedProcessesFindsDescendantTreeAndRetainsReparentedProcess(t *testing.T) {
	snapshot := ProcessSnapshot{
		"10": {ID: "10", PID: 10, ParentPID: 1},
		"11": {ID: "11", PID: 11, ParentPID: 10},
		"12": {ID: "12", PID: 12, ParentPID: 11},
		"20": {ID: "20", PID: 20, ParentPID: 1},
	}
	selected := selectTracedProcesses(snapshot, map[int]bool{10: true}, 10, true)
	if len(selected) != 3 || selected[12].ParentPID != 11 {
		t.Fatalf("expected root and recursive descendants only, received %#v", selected)
	}
	reparented := ProcessSnapshot{"12": {ID: "12", PID: 12, ParentPID: 1}}
	selected = selectTracedProcesses(reparented, map[int]bool{12: true}, 10, false)
	if len(selected) != 1 || selected[12].PID != 12 {
		t.Fatalf("expected already tracked reparented descendant, received %#v", selected)
	}
}

func TestTraceCancellationReturnsLogFailure(t *testing.T) {
	expected := errors.New("log unavailable")
	err := recordTraceCancellation(func(Event) error { return expected }, time.Unix(1, 0))
	if !errors.Is(err, expected) || !strings.Contains(err.Error(), "cancellation gap") {
		t.Fatalf("expected cancellation gap write failure, received %v", err)
	}
}

func TestReapTraceRootWaitsAfterLogFailure(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("find test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestTraceNaturalChild$")
	command.Env = append(os.Environ(), "BOTTOM_TRACE_NATURAL_CHILD=1")
	if err := command.Start(); err != nil {
		t.Fatalf("start natural trace child: %v", err)
	}
	results := make(chan traceCommandResult, 1)
	go waitForTracedCommand(command, results)
	logErr := errors.New("log failed")
	if err := reapTraceRoot(results, false, logErr, []string{"natural-child"}); !errors.Is(err, logErr) {
		t.Fatalf("expected log error after reaping trace root, received %v", err)
	}
	if command.ProcessState == nil || !command.ProcessState.Exited() {
		t.Fatalf("expected trace root to be reaped after its natural exit")
	}
}

func TestTraceNaturalChild(t *testing.T) {
	if os.Getenv("BOTTOM_TRACE_NATURAL_CHILD") == "1" {
		time.Sleep(20 * time.Millisecond)
	}
}
