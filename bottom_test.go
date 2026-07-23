package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestSnapshotDiffAddsParentChain(t *testing.T) {
	now := time.Unix(1, 0)
	previous := ProcessSnapshot{
		"1": capturedProcess("1", 1, 0, "parent", "/bin/parent", "/", "jer", time.Time{}, now),
	}
	next := ProcessSnapshot{
		"1": capturedProcess("1", 1, 0, "parent", "/bin/parent", "/", "jer", time.Time{}, now),
		"2": capturedProcess("2", 2, 1, "child", "/bin/child", "/", "jer", time.Time{}, now),
	}
	events := make(chan Event, 1)
	emitSnapshotDiff(context.Background(), BackendPoll, previous, next, events)
	event := <-events
	if event.Kind != EventStart || event.PID != 2 {
		t.Fatalf("expected start for pid 2, received kind=%s pid=%d", event.Kind, event.PID)
	}
	if len(event.ParentChain) != 1 || event.ParentChain[0].PID != 1 {
		t.Fatalf("expected one parent chain entry for pid 1, received %#v", event.ParentChain)
	}
}

func TestEventLogWritesCommand(t *testing.T) {
	var output bytes.Buffer
	event := Event{Kind: EventStart, Time: time.Unix(1, 0), PID: 9, Command: "sample", Backend: BackendPoll}
	if err := writeEventLog(&output, event, false); err != nil {
		t.Fatalf("write text event: %v", err)
	}
	if output.String() == "" {
		t.Fatalf("expected text output")
	}
}

func TestWatchStartLog(t *testing.T) {
	var output bytes.Buffer
	if err := writeWatchStarted(&output); err != nil {
		t.Fatalf("write watch start log: %v", err)
	}
	if output.String() != "Starting process watch\n" {
		t.Fatalf("expected process watch banner, received %q", output.String())
	}
}

func TestParseConfigUsesMillisecondPolling(t *testing.T) {
	config, err := parseConfig(nil)
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if config.PollInterval != 100*time.Millisecond {
		t.Fatalf("expected default poll interval 100ms, received %s", config.PollInterval)
	}
	if config.ShowPPID {
		t.Fatalf("expected parent PID display to be disabled by default")
	}
	config, err = parseConfig([]string{"-poll", "25ms"})
	if err != nil {
		t.Fatalf("parse millisecond poll config: %v", err)
	}
	if config.PollInterval != 25*time.Millisecond {
		t.Fatalf("expected poll interval 25ms, received %s", config.PollInterval)
	}
	config, err = parseConfig([]string{"-ppid"})
	if err != nil {
		t.Fatalf("parse parent PID display config: %v", err)
	}
	if !config.ShowPPID {
		t.Fatalf("expected -ppid to enable parent PID display")
	}
}

func TestParseConfigRejectsInvalidCrossOptionCombinations(t *testing.T) {
	cases := [][]string{
		{"-format", "binary"},
		{"unexpected"},
	}
	for _, args := range cases {
		if _, err := parseConfig(args); err == nil {
			t.Fatalf("expected invalid arguments %q to be rejected", args)
		}
	}
}

func TestSelectBackendRejectsPlaceholderNames(t *testing.T) {
	placeholderNames := []string{"linux-ebpf", "windows-wmi", "macos-kqueue-placeholder"}
	for _, name := range placeholderNames {
		_, _, err := selectBackend(Config{Backend: name, PollInterval: time.Millisecond})
		if err == nil {
			t.Fatalf("expected backend %q to be rejected", name)
		}
		if !strings.Contains(err.Error(), "auto, poll, linux-proc-connector, windows-etw, or macos-endpoint-security") {
			t.Fatalf("expected backend error to list real choices, received %q", err.Error())
		}
	}
}

func TestStopTextIncludesCommand(t *testing.T) {
	event := Event{Kind: EventStop, Time: time.Unix(1, 0), PID: 9, Command: "sample-worker", DurationMillis: 83, Backend: BackendPoll}
	line := formatEventLog(event, false)
	if line != "Stop: 9: sample-worker" {
		t.Fatalf("expected readable stop text, received %q", line)
	}
}

func TestTextOutputCanIncludeParentPID(t *testing.T) {
	event := Event{Kind: EventStart, PID: 9, ParentPID: 4, Command: "sample-worker"}
	line := formatEventLog(event, true)
	if line != "Start: 9 (ppid 4): sample-worker" {
		t.Fatalf("expected readable parent PID text, received %q", line)
	}
}

func TestVersionLineIncludesBuildIdentity(t *testing.T) {
	line := versionLine()
	for _, field := range []string{"bottom ", " commit=", " built="} {
		if !strings.Contains(line, field) {
			t.Fatalf("expected version line %q to contain %q", line, field)
		}
	}
}

func TestVersionLinePreservesInjectedReleaseIdentity(t *testing.T) {
	originalVersion := version
	originalCommit := commit
	originalBuildDate := buildDate
	version = "v1.2.3"
	commit = "0123456789abcdef"
	buildDate = "2026-07-09T00:00:00Z"
	defer func() {
		version = originalVersion
		commit = originalCommit
		buildDate = originalBuildDate
	}()
	expected := "bottom v1.2.3 commit=0123456789abcdef built=2026-07-09T00:00:00Z"
	if line := versionLine(); line != expected {
		t.Fatalf("expected injected release identity %q, received %q", expected, line)
	}
}

func TestTextOutputEscapesParentTerminalControls(t *testing.T) {
	event := Event{
		Kind:    EventStart,
		Time:    time.Unix(1, 0),
		PID:     7,
		Command: "worker\x1b[2J",
		Backend: BackendPoll,
	}
	line := formatEventLog(event, false)
	if strings.ContainsRune(line, '\x1b') || line != `Start: 7: worker\x1b[2J` {
		t.Fatalf("expected escaped command in text output, received %q", line)
	}
}
