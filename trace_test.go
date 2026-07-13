package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTraceConfigRequiresExactCommandBoundary(t *testing.T) {
	config, err := parseTraceConfig([]string{"-poll", "5ms", "-tail", "1s", "--", "go", "test", "./..."})
	if err != nil {
		t.Fatalf("parse trace config: %v", err)
	}
	if config.Recorder.Backend != BackendTrace || config.Recorder.PollInterval != 5*time.Millisecond || config.Tail != time.Second || strings.Join(config.Command, " ") != "go test ./..." {
		t.Fatalf("expected parsed trace settings and command, received %#v", config)
	}
	if config.Recorder.Format != FormatJSONL || config.Recorder.OutputPath != "bottom-trace.jsonl" {
		t.Fatalf("expected default JSONL trace recording, received %#v", config.Recorder)
	}
	if _, err := parseTraceConfig([]string{"go", "test"}); err == nil {
		t.Fatalf("expected trace command without -- boundary to be rejected")
	}
	if _, err := parseTraceConfig([]string{"-h"}); !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected trace help without a command boundary, received %v", err)
	}
	textConfig, err := parseTraceConfig([]string{"-format", "text", "--", "go", "test"})
	if err != nil || textConfig.Recorder.OutputPath != "bottom-trace.log" {
		t.Fatalf("expected format-specific trace path, received config=%#v err=%v", textConfig, err)
	}
	if _, err := parseTraceConfig([]string{"-tui", "--", "go", "test"}); err == nil || !strings.Contains(err.Error(), "shares the terminal") {
		t.Fatalf("expected trace tui to be rejected before command execution, received %v", err)
	}
	if _, err := parseTraceConfig([]string{"-output", "trace.jsonl", "-perfetto", "./trace.jsonl", "--", "go", "test"}); err == nil || !strings.Contains(err.Error(), "different files") {
		t.Fatalf("expected equivalent trace outputs to be rejected, received %v", err)
	}
}

func TestParseTraceConfigRejectsPathsThroughEquivalentDirectories(t *testing.T) {
	directory := t.TempDir()
	realDirectory := filepath.Join(directory, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("create trace output directory: %v", err)
	}
	linkedDirectory := filepath.Join(directory, "linked")
	if err := os.Symlink(realDirectory, linkedDirectory); err != nil {
		t.Skipf("create trace output directory link: %v", err)
	}
	_, err := parseTraceConfig([]string{
		"-output", filepath.Join(realDirectory, "uncreated", "trace.jsonl"),
		"-perfetto", filepath.Join(linkedDirectory, "uncreated", "trace.jsonl"),
		"--", "go", "test",
	})
	if err == nil || !strings.Contains(err.Error(), "different files") {
		t.Fatalf("expected paths through equivalent directories to be rejected, received %v", err)
	}
	danglingTarget := filepath.Join(directory, "future.jsonl")
	danglingLink := filepath.Join(directory, "future-link.jsonl")
	if err := os.Symlink(danglingTarget, danglingLink); err != nil {
		t.Skipf("create dangling trace output link: %v", err)
	}
	_, err = parseTraceConfig([]string{"-output", danglingLink, "-perfetto", danglingTarget, "--", "go", "test"})
	if err == nil || !strings.Contains(err.Error(), "different files") {
		t.Fatalf("expected a dangling output link and its target to be rejected, received %v", err)
	}
}

func TestRunTraceRejectsOutputConflictBeforeCommandExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "same-output")
	config := TraceConfig{
		Recorder:     Config{OutputPath: path},
		PerfettoPath: path,
		Command:      []string{"command-must-not-run"},
	}
	if err := runTrace(context.Background(), config); err == nil || !strings.Contains(err.Error(), "different files") {
		t.Fatalf("expected runTrace to reject output conflict before command execution, received %v", err)
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

func TestWritePerfettoTraceUsesPrivateNewFileAndRedacts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.json")
	events := []Event{{Kind: EventExec, Time: time.Unix(1, 0), PID: 4, Command: "worker --token secret", Exe: "/bin/worker", Backend: "trace"}}
	if err := writePerfettoTrace(path, events, []string{"secret"}); err != nil {
		t.Fatalf("write Perfetto trace: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read Perfetto trace: %v", err)
	}
	if strings.Contains(string(data), "secret") || !strings.Contains(string(data), "[REDACTED]") {
		t.Fatalf("expected redacted Perfetto trace, received %q", data)
	}
	if err := writePerfettoTrace(path, events, nil); err == nil {
		t.Fatalf("expected existing Perfetto path not to be overwritten")
	}
}

func TestWriteTraceEventRetainsOnlyForRequestedExport(t *testing.T) {
	recorder := &traceEventRecorder{}
	event := Event{Kind: EventStart, Time: time.Unix(1, 0), PID: 7, Command: "worker", Backend: "trace"}
	if err := writeTraceEvent(recorder, nil, event); err != nil {
		t.Fatalf("write trace event without export: %v", err)
	}
	if len(recorder.events) != 1 {
		t.Fatalf("expected trace event to reach recorder, received %d events", len(recorder.events))
	}
	retained := []Event{}
	if err := writeTraceEvent(recorder, &retained, event); err != nil {
		t.Fatalf("write trace event with export: %v", err)
	}
	if len(retained) != 1 || retained[0].PID != event.PID {
		t.Fatalf("expected requested export to retain the event, received %#v", retained)
	}
}

func TestTraceCancellationReturnsGapWriteFailure(t *testing.T) {
	expected := errors.New("recording unavailable")
	recorder := traceFailingRecorder{err: expected}
	err := recordTraceCancellation(recorder, nil, time.Unix(1, 0))
	if !errors.Is(err, expected) || !strings.Contains(err.Error(), "cancellation gap") {
		t.Fatalf("expected cancellation gap write failure, received %v", err)
	}
}

func TestReapTraceRootWaitsAfterRecordingFailure(t *testing.T) {
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
	recordingErr := errors.New("recording failed")
	if err := reapTraceRoot(results, false, recordingErr, []string{"natural-child"}); !errors.Is(err, recordingErr) {
		t.Fatalf("expected recording error after reaping trace root, received %v", err)
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

type traceEventRecorder struct {
	events []Event
}

type traceFailingRecorder struct {
	err error
}

func (recorder traceFailingRecorder) Write(Event) error {
	return recorder.err
}

func (recorder traceFailingRecorder) Close() error {
	return nil
}

func (recorder *traceEventRecorder) Write(event Event) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func (recorder *traceEventRecorder) Close() error {
	return nil
}
