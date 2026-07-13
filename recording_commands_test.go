package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLRecordingQueryAndReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.jsonl")
	exitCode := 3
	writeReaderTestRecording(t, path, []Event{
		{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Command: "compiler main.go", Exe: "/bin/compiler", ParentPID: 1},
		{Kind: EventStop, Time: time.Unix(11, 0), PID: 10, Command: "compiler main.go", Exe: "/bin/compiler", ParentPID: 1, DurationMillis: 1000, ExitCode: &exitCode},
	})
	query := RecordingReadConfig{InputPaths: []string{path}, Format: FormatJSONL, Filter: Filter{EventMode: string(EventStop)}}
	events, err := filteredRecordingEvents(query)
	if err != nil {
		t.Fatalf("query JSONL recording: %v", err)
	}
	if len(events) != 1 || events[0].Kind != EventStop || events[0].ExitCode == nil || *events[0].ExitCode != 3 {
		t.Fatalf("expected one failed stop event, received %#v", events)
	}
	var report bytes.Buffer
	if err := writeRecordingReport(&report, events); err != nil {
		t.Fatalf("report JSONL recording: %v", err)
	}
	if !strings.Contains(report.String(), "events=1") || !strings.Contains(report.String(), "failed_exits=1") {
		t.Fatalf("unexpected report: %q", report.String())
	}
}

func TestParseRecordingReadConfigRelativeTimeAndValidation(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	config, err := parseRecordingReadConfig("query", []string{"-since", "15m", "-until", "5m", "-exit-code", "7"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !config.Filter.Since.Equal(now.Add(-15*time.Minute)) || !config.Filter.Until.Equal(now.Add(-5*time.Minute)) || !config.Filter.HasExitCode || config.Filter.ExitCode != 7 {
		t.Fatalf("unexpected recording filters: %#v", config.Filter)
	}
	if len(config.InputPaths) != 1 || config.InputPaths[0] != "bottom.jsonl" {
		t.Fatalf("expected bottom.jsonl default input, received %#v", config.InputPaths)
	}
	if _, err := parseRecordingReadConfig("query", []string{"-since", "5m", "-until", "15m"}, now); err == nil {
		t.Fatal("expected reversed time range to be rejected")
	}
}

func TestParseRecordingReadConfigAcceptsRepeatedInputs(t *testing.T) {
	config, err := parseRecordingReadConfig("query", []string{"-input", "first.jsonl", "-input", "second.jsonl"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(config.InputPaths) != 2 || config.InputPaths[0] != "first.jsonl" || config.InputPaths[1] != "second.jsonl" {
		t.Fatalf("expected ordered repeated inputs, received %#v", config.InputPaths)
	}
	tooMany := []string{}
	for index := 0; index <= maxRecordingInputPaths; index++ {
		tooMany = append(tooMany, "-input", fmt.Sprintf("recording-%d.jsonl", index))
	}
	if _, err := parseRecordingReadConfig("query", tooMany, time.Now()); err == nil {
		t.Fatal("expected too many inputs to be rejected")
	}
}

func TestRecordingReadersMergeMultipleJSONLInputs(t *testing.T) {
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.jsonl")
	secondPath := filepath.Join(directory, "second.jsonl")
	writeReaderTestRecording(t, firstPath, []Event{
		{Kind: EventStart, Time: time.Unix(10, 0), Sequence: 2, PID: 10},
		{Kind: EventStop, Time: time.Unix(30, 0), Sequence: 4, PID: 30},
	})
	writeReaderTestRecording(t, secondPath, []Event{
		{Kind: EventExec, Time: time.Unix(20, 0), Sequence: 1, PID: 20},
		{Kind: EventGap, Time: time.Unix(30, 0), Sequence: 3, Message: "gap"},
	})
	events, err := filteredRecordingEvents(RecordingReadConfig{
		InputPaths: []string{firstPath, secondPath}, Filter: Filter{EventMode: EventModeAll},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 4 || events[0].PID != 10 || events[1].PID != 20 || events[2].Kind != EventGap || events[3].PID != 30 {
		t.Fatalf("expected chronological merged events, received %#v", events)
	}
}

func TestRecordingLimitDefersLaterJSONLError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.jsonl")
	writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Command: "worker"}})
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteString("{\n")
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append invalid JSONL: write=%v close=%v", writeErr, closeErr)
	}
	config := RecordingReadConfig{InputPaths: []string{path}, Filter: Filter{EventMode: EventModeAll}, Limit: 1}
	events, err := filteredRecordingEvents(config)
	if err != nil || len(events) != 1 || events[0].PID != 10 {
		t.Fatalf("expected first event through limit, events=%#v error=%v", events, err)
	}
	config.Limit = 0
	if _, err := filteredRecordingEvents(config); err == nil || !strings.Contains(err.Error(), "decode JSONL") {
		t.Fatalf("expected unlimited read to surface invalid JSONL, received %v", err)
	}
}

func TestRecordingCommandsRejectOutputAliases(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "recording.jsonl")
	comparisonPath := filepath.Join(directory, "comparison.jsonl")
	writeReaderTestRecording(t, inputPath, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10}})
	writeReaderTestRecording(t, comparisonPath, []Event{{Kind: EventStart, Time: time.Unix(20, 0), PID: 20}})
	inputAlias := filepath.Join(directory, ".", filepath.Base(inputPath))
	query := RecordingReadConfig{InputPaths: []string{inputPath}, OutputPath: inputAlias, Format: FormatText, Filter: Filter{EventMode: EventModeAll}}
	if err := runRecordingQuery(query); err == nil || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected query output alias rejection, received %v", err)
	}
	report := RecordingReadConfig{InputPaths: []string{inputPath}, OutputPath: inputAlias, Filter: Filter{EventMode: EventModeAll}}
	if err := runRecordingReport(report); err == nil || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected report output alias rejection, received %v", err)
	}
	compare := RecordingCompareConfig{BeforePath: inputPath, AfterPath: comparisonPath, OutputPath: inputAlias}
	if err := runRecordingCompare(compare); err == nil || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected comparison output alias rejection, received %v", err)
	}
}

func TestRecordingComparisonReportsEpisodeChanges(t *testing.T) {
	exitZero := 0
	exitFailure := 3
	before := []Event{{Kind: EventStop, Exe: "/bin/compiler", ParentPID: 1, DurationMillis: 100, ExitCode: &exitZero}}
	after := []Event{
		{Kind: EventStop, Exe: "/bin/compiler", ParentPID: 1, DurationMillis: 200, ExitCode: &exitFailure},
		{Kind: EventStart, Exe: "/bin/linker", ParentPID: 1},
	}
	var output bytes.Buffer
	if err := writeRecordingComparison(&output, before, after); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "/bin/compiler <- 1") || !strings.Contains(output.String(), "/bin/linker <- 1") || !strings.Contains(output.String(), "+1") {
		t.Fatalf("unexpected comparison: %q", output.String())
	}
	if _, err := parseRecordingCompareConfig([]string{"-before", "same.jsonl", "-after", "same.jsonl"}); err == nil {
		t.Fatal("expected identical comparison paths to be rejected")
	}
}

func TestReportsAndComparisonUseFullCapturedAncestry(t *testing.T) {
	before := Event{
		Kind: EventStart, Exe: "/bin/worker", ParentPID: 2,
		ParentChain: []ProcessSummary{{PID: 2, Exe: "/bin/shell"}, {PID: 1, Exe: "/sbin/init-before"}},
	}
	after := before
	after.ParentChain = []ProcessSummary{{PID: 2, Exe: "/bin/shell"}, {PID: 1, Exe: "/sbin/init-after"}}
	var report bytes.Buffer
	if err := writeRecordingReport(&report, []Event{before}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "/bin/shell -> /bin/worker") || !strings.Contains(report.String(), "/sbin/init-before -> /bin/shell") {
		t.Fatalf("unexpected ancestry report: %q", report.String())
	}
	var comparison bytes.Buffer
	if err := writeRecordingComparison(&comparison, []Event{before}, []Event{after}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(comparison.String(), "/bin/worker <- /bin/shell <- /sbin/init-before") || !strings.Contains(comparison.String(), "/bin/worker <- /bin/shell <- /sbin/init-after") {
		t.Fatalf("unexpected ancestry comparison: %q", comparison.String())
	}
}

func TestHumanReportsEscapeProcessControls(t *testing.T) {
	event := Event{Kind: EventStart, Exe: "/bin/worker\x1b[2J", ParentPID: 1}
	var report bytes.Buffer
	if err := writeRecordingReport(&report, []Event{event}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(report.String(), '\x1b') || !strings.Contains(report.String(), `\x1b[2J`) {
		t.Fatalf("expected escaped report text, received %q", report.String())
	}
}

func writeReaderTestRecording(t *testing.T, path string, events []Event) {
	t.Helper()
	session := testRecordingSession(t)
	recorder, err := newJSONLRecorder(path, session)
	if err != nil {
		t.Fatal(err)
	}
	for index, event := range events {
		if event.SchemaVersion == 0 {
			event.SchemaVersion = EventSchemaVersion
		}
		if event.SessionID == "" {
			event.SessionID = session.ID
		}
		if event.Sequence == 0 {
			event.Sequence = uint64(index + 1)
		}
		if event.Backend == "" {
			event.Backend = BackendPoll
		}
		if err := recorder.Write(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}
