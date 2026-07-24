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

func TestPSOutputExcludesOnlyBottomSnapshotCollector(t *testing.T) {
	output := []byte("100 50 jer /bin/ps -axo pid=,ppid=,user=,command=\n101 50 jer /bin/ps aux\n")
	snapshot := parsePSOutput(output, time.Unix(1, 0), 100)
	if _, found := snapshot["100"]; found {
		t.Fatalf("expected Bottom's ps collector to be excluded")
	}
	proc, found := snapshot["101"]
	if !found || proc.Command != "/bin/ps aux" {
		t.Fatalf("expected unrelated ps command to remain visible, received %#v", snapshot)
	}
}

func TestEventLogWritesCommand(t *testing.T) {
	var output bytes.Buffer
	event := Event{Kind: EventStart, Time: time.Unix(1, 0), PID: 9, Command: "sample", Backend: BackendPoll}
	if err := writeEventLog(&output, event, false, false); err != nil {
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
	if config.ShowParentExe {
		t.Fatalf("expected parent executable display to be disabled in default readable output")
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
	config, err = parseConfig([]string{"-tui"})
	if err != nil {
		t.Fatalf("parse TUI config: %v", err)
	}
	if !config.ShowParentExe {
		t.Fatalf("expected parent executable display to be enabled by default in the TUI")
	}
	config, err = parseConfig([]string{"-tui", "-parent-exe=false"})
	if err != nil {
		t.Fatalf("parse TUI without parent executable config: %v", err)
	}
	if config.ShowParentExe {
		t.Fatalf("expected -parent-exe=false to disable the TUI parent executable display")
	}
	config, err = parseConfig([]string{"-parent-exe"})
	if err != nil {
		t.Fatalf("parse readable parent executable config: %v", err)
	}
	if !config.ShowParentExe {
		t.Fatalf("expected -parent-exe to enable readable parent executable display")
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
	line := formatEventLog(event, false, false)
	if line != "Stop:  9        sample-worker" {
		t.Fatalf("expected readable stop text, received %q", line)
	}
}

func TestTextOutputCanIncludeParentPID(t *testing.T) {
	event := Event{Kind: EventStart, PID: 9, ParentPID: 4, Command: "sample-worker"}
	line := formatEventLog(event, true, false)
	if line != "Start: 9        4        sample-worker" {
		t.Fatalf("expected readable parent PID text, received %q", line)
	}
}

func TestTextOutputCanIncludeParentExecutable(t *testing.T) {
	event := Event{
		Kind:        EventStart,
		PID:         9,
		Command:     "sample-worker",
		ParentChain: []ProcessSummary{{PID: 4, Exe: "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"}},
	}
	line := formatEventLog(event, false, true)
	if !strings.Contains(line, "ChatGPT") || !strings.HasSuffix(line, "sample-worker") {
		t.Fatalf("expected parent executable before the unbounded command, received %q", line)
	}
}

func TestTextOutputAlignsFixedColumnsAndLeavesMessageUnbounded(t *testing.T) {
	command := strings.Repeat("x", 200)
	events := []Event{
		{Kind: EventStart, PID: 1, Command: command},
		{Kind: EventExec, PID: 22, Command: command},
		{Kind: EventStop, PID: 333, Command: command},
		{Kind: EventGap, Message: command},
	}
	for _, options := range []struct {
		showPPID      bool
		showParentExe bool
	}{
		{},
		{showPPID: true},
		{showParentExe: true},
		{showPPID: true, showParentExe: true},
	} {
		commandColumn := -1
		for _, event := range events {
			event.ParentPID = 4
			event.ParentChain = []ProcessSummary{{PID: 4, Exe: "/bin/parent"}}
			line := formatEventLog(event, options.showPPID, options.showParentExe)
			column := strings.Index(line, command)
			if commandColumn < 0 {
				commandColumn = column
			}
			if column != commandColumn || !strings.HasSuffix(line, command) {
				t.Fatalf("expected unbounded final column at %d, received %q", commandColumn, line)
			}
		}
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
	line := formatEventLog(event, false, false)
	if strings.ContainsRune(line, '\x1b') || line != `Start: 7        worker\x1b[2J` {
		t.Fatalf("expected escaped command in text output, received %q", line)
	}
}
