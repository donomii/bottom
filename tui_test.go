package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTUIPauseSearchDetailsAndCoverage(t *testing.T) {
	var output bytes.Buffer
	recorder := NewTUI(&output)
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
	if !strings.Contains(frame, "PAUSED") || !strings.Contains(frame, `filter "coverage lost"`) || !strings.Contains(frame, "coverage lost") || !strings.Contains(frame, "Selected event") {
		t.Fatalf("expected coverage, search result, and details in frame, received %q", frame)
	}
	if strings.Contains(frame, "bottom - live process activity") || strings.Contains(frame, "capture gap") {
		t.Fatalf("expected frame without redundant title and gap count, received %q", frame)
	}
}

func TestTUIStatusLineUsesReadableLabels(t *testing.T) {
	recorder := NewTUI(&bytes.Buffer{})
	recorder.events = []Event{{Kind: EventStart, PID: 7, Command: "worker"}}
	recorder.backend = BackendPoll
	recorder.searching = true
	line := recorder.statusLine()
	for _, expected := range []string{"poll", "1 event", "search: _"} {
		if !strings.Contains(line, expected) {
			t.Fatalf("expected status line %q to contain %q", line, expected)
		}
	}
	if strings.Contains(line, "LIVE") {
		t.Fatalf("expected status line without a live label, received %q", line)
	}
	for _, diagnostic := range []string{"backend=", "coverage_gaps=", "paused=", "search=", "scroll=", "columns=", "sort="} {
		if strings.Contains(line, diagnostic) {
			t.Fatalf("expected readable status line without %q, received %q", diagnostic, line)
		}
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

func TestTUIImmediateKeysEditSearchAndCycleViews(t *testing.T) {
	var output bytes.Buffer
	recorder := NewTUI(&output)
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
	recorder := NewTUI(&bytes.Buffer{})
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
	recorder := NewTUI(&bytes.Buffer{})
	recorder.width = 60
	recorder.height = 18
	if limit := recorder.visibleEventLimit(); limit != 5 {
		t.Fatalf("expected compact terminal to show five events, received %d", limit)
	}
	line := recorder.eventLine(Event{Kind: EventStart, Command: strings.Repeat("x", 80)})
	if utf8.RuneCountInString(line) > recorder.width {
		t.Fatalf("expected event line to fit width %d, received width %d line=%q", recorder.width, utf8.RuneCountInString(line), line)
	}
}

func TestTUIQuitKeysStopImmediatelyFromSearch(t *testing.T) {
	for _, key := range []rune{'q', 0x03, 0x04} {
		stops := 0
		recorder := newTUI(&bytes.Buffer{}, func() { stops++ }, false, true)
		recorder.searching = true
		recorder.searchDraft = "worker"
		recorder.handleKey(key)
		if stops != 1 || recorder.status != "stopping" {
			t.Fatalf("expected key %q to stop the TUI immediately, received stops=%d status=%q", key, stops, recorder.status)
		}
	}
}

func TestTUIEscapeCancelsSearchAndQuitsOutsideSearch(t *testing.T) {
	stops := 0
	recorder := newTUI(&bytes.Buffer{}, func() { stops++ }, false, true)
	recorder.search = "existing"
	recorder.searching = true
	recorder.searchDraft = "unfinished"
	recorder.handleKey(0x1b)
	if stops != 0 || recorder.searching || recorder.searchDraft != "existing" {
		t.Fatalf("expected Escape to cancel search without stopping, received stops=%d searching=%t draft=%q", stops, recorder.searching, recorder.searchDraft)
	}
	recorder.handleKey(0x1b)
	if stops != 1 || recorder.status != "stopping" {
		t.Fatalf("expected Escape outside search to stop the TUI, received stops=%d status=%q", stops, recorder.status)
	}
}

func TestTUIQuitCommandStopsImmediately(t *testing.T) {
	stops := 0
	recorder := newTUI(&bytes.Buffer{}, func() { stops++ }, false, true)
	recorder.handleCommand("q")
	if stops != 1 || recorder.status != "stopping" {
		t.Fatalf("expected q command to stop the TUI immediately, received stops=%d status=%q", stops, recorder.status)
	}
}

func TestTUIPPIDColumnFollowsOption(t *testing.T) {
	event := Event{Kind: EventStart, Time: time.Unix(1, 0), PID: 42, ParentPID: 7, Command: "worker"}
	withoutPPID := NewTUI(&bytes.Buffer{})
	if strings.Contains(withoutPPID.columnHeader(), "ppid") || strings.Contains(tuiEventDetail(event, false, true), "parent=") {
		t.Fatalf("expected TUI parent PID to be hidden without -ppid")
	}
	withPPID := newTUI(&bytes.Buffer{}, nil, true, true)
	if !strings.Contains(withPPID.columnHeader(), "ppid") || !strings.Contains(withPPID.eventLine(event), "42       7") || !strings.Contains(tuiEventDetail(event, true, true), "parent=7") {
		t.Fatalf("expected TUI parent PID to be visible with -ppid")
	}
}

func TestTUIParentExecutableIsShownByDefaultAndCanBeDisabled(t *testing.T) {
	event := Event{
		Kind:        EventStart,
		Time:        time.Unix(1, 0),
		PID:         42,
		Command:     "ps",
		ParentChain: []ProcessSummary{{PID: 7, Exe: "/Applications/ChatGPT.app/Contents/MacOS/ChatGPT"}},
	}
	withParent := NewTUI(&bytes.Buffer{})
	if !strings.Contains(withParent.columnHeader(), "parent") || !strings.Contains(withParent.eventLine(event), "ChatGPT") || !strings.Contains(tuiEventDetail(event, false, true), `parent_exe="ChatGPT"`) {
		t.Fatalf("expected TUI parent executable to be visible by default")
	}
	withoutParent := newTUI(&bytes.Buffer{}, nil, false, false)
	if strings.Contains(withoutParent.columnHeader(), "parent") || strings.Contains(withoutParent.eventLine(event), "ChatGPT") || strings.Contains(tuiEventDetail(event, false, false), "parent_exe=") {
		t.Fatalf("expected disabled TUI parent executable to be hidden")
	}
}
