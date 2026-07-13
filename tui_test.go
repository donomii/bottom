package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTUIRecorderPauseSearchDetailsAndCoverage(t *testing.T) {
	var output bytes.Buffer
	recorder := NewTUIRecorder(&output)
	first := Event{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Command: "alpha task", Exe: "/bin/alpha", Backend: BackendPoll}
	if err := recorder.Write(first); err != nil {
		t.Fatalf("write first tui event: %v", err)
	}
	recorder.handleCommand("p")
	pausedSize := output.Len()
	if err := recorder.Write(Event{Kind: EventGap, Time: time.Unix(2, 0), Message: "coverage lost", Backend: BackendPoll}); err != nil {
		t.Fatalf("write gap while paused: %v", err)
	}
	if output.Len() != pausedSize {
		t.Fatalf("expected paused tui not to redraw, size changed from %d to %d", pausedSize, output.Len())
	}
	recorder.handleCommand("/coverage lost")
	recorder.handleCommand("d")
	frame := recorder.render()
	if !strings.Contains(frame, "coverage_gaps=1") || !strings.Contains(frame, "coverage lost") || !strings.Contains(frame, "Selected event") {
		t.Fatalf("expected coverage, search result, and details in frame, received %q", frame)
	}
}

func TestTruncatePreservesUTF8(t *testing.T) {
	actual := truncate("αβγδε", 4)
	if actual != "α..." {
		t.Fatalf("expected rune-safe truncation, received %q", actual)
	}
	if !utf8.ValidString(actual) {
		t.Fatalf("expected valid UTF-8, received %q", actual)
	}
}

func TestSanitizeTerminalTextEscapesControlSequences(t *testing.T) {
	actual := sanitizeTerminalText("safe\x1b[2J\nname\u009b31m")
	expected := `safe\x1b[2J\x0aname\x9b31m`
	if actual != expected {
		t.Fatalf("expected escaped terminal controls %q, received %q", expected, actual)
	}
}

func TestTUIProcessGroupsKeepSemanticContext(t *testing.T) {
	first := tuiProcessGroup(Event{Exe: "/bin/worker", ParentPID: 1, UID: "1000", SystemdUnit: "one.service"})
	second := tuiProcessGroup(Event{Exe: "/bin/worker", ParentPID: 2, UID: "1000", SystemdUnit: "two.service"})
	if first == second || !strings.Contains(first, "parent=1") || !strings.Contains(second, "unit=two.service") {
		t.Fatalf("expected distinct semantic process groups, received first=%q second=%q", first, second)
	}
}

func TestTUIImmediateKeysEditSearchAndCycleViews(t *testing.T) {
	var output bytes.Buffer
	recorder := NewTUIRecorder(&output)
	recorder.handleKey('/')
	for _, key := range "worker" {
		recorder.handleKey(key)
	}
	recorder.handleKey('\r')
	recorder.handleKey('c')
	recorder.handleKey('s')
	if recorder.search != "worker" || recorder.searching {
		t.Fatalf("expected immediate search to apply, received search=%q searching=%t", recorder.search, recorder.searching)
	}
	if recorder.columns != tuiColumnsContext || recorder.sortMode != tuiSortDuration {
		t.Fatalf("expected context columns and duration sort, received columns=%s sort=%s", recorder.columnName(), recorder.sortName())
	}
	recorder.handleKey('x')
	if recorder.search != "" {
		t.Fatalf("expected immediate search clear, received %q", recorder.search)
	}
}

func TestTUISortsEventsWithoutChangingStoredOrder(t *testing.T) {
	recorder := NewTUIRecorder(&bytes.Buffer{})
	recorder.events = []Event{
		{Kind: EventStop, PID: 2, Command: "short", DurationMillis: 10},
		{Kind: EventStop, PID: 1, Command: "long", DurationMillis: 50},
	}
	recorder.sortMode = tuiSortDuration
	filtered := recorder.filteredEvents()
	if filtered[0].Command != "long" || recorder.events[0].Command != "short" {
		t.Fatalf("expected sorted view without changing stored timeline, received view=%q stored=%q", filtered[0].Command, recorder.events[0].Command)
	}
}

func TestTUIAdaptsVisibleRowsAndCommandWidth(t *testing.T) {
	recorder := NewTUIRecorder(&bytes.Buffer{})
	recorder.width = 60
	recorder.height = 18
	if limit := recorder.visibleEventLimit(); limit != 3 {
		t.Fatalf("expected compact terminal to show three events, received %d", limit)
	}
	line := recorder.eventLine(Event{Kind: EventStart, Command: strings.Repeat("x", 80)})
	if utf8.RuneCountInString(line) > recorder.width {
		t.Fatalf("expected event line to fit width %d, received width %d line=%q", recorder.width, utf8.RuneCountInString(line), line)
	}
}

func TestTUIControlKeyStopsItsOwnRunContext(t *testing.T) {
	stops := 0
	recorder := newTUIRecorder(&bytes.Buffer{}, func() { stops++ })
	recorder.handleKey(0x03)
	recorder.handleKey(0x04)
	if stops != 1 || recorder.status != "stopping" {
		t.Fatalf("expected one TUI stop request, received stops=%d status=%q", stops, recorder.status)
	}
}
