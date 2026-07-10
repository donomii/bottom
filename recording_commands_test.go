package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSQLiteRecordingQueryAndReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	config := Config{Backend: BackendPoll, Format: FormatSQLite, OutputPath: path, RecorderBuffer: 8, SQLiteBatch: 2, SQLiteFlush: time.Millisecond}
	recorder, err := newRecorder(config)
	if err != nil {
		t.Fatalf("create recording: %v", err)
	}
	exitCode := 7
	events := []Event{
		{Kind: EventStart, Time: time.Unix(10, 0), ProcessID: "p1", PID: 10, ParentPID: 1, Command: "worker --job one", Exe: "/bin/worker", Backend: BackendPoll},
		{Kind: EventStop, Time: time.Unix(11, 0), ProcessID: "p1", PID: 10, ParentPID: 1, Command: "worker --job one", Exe: "/bin/worker", DurationMillis: 1000, ExitCode: &exitCode, Backend: BackendPoll},
		{Kind: EventGap, Time: time.Unix(12, 0), Backend: BackendPoll, Message: "snapshot unavailable"},
	}
	for _, event := range events {
		if err := recorder.Write(event); err != nil {
			t.Fatalf("record event: %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close recording: %v", err)
	}
	read, err := readSQLiteRecording(path)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	if len(read) != 3 || read[0].Kind != EventStart || read[2].Kind != EventGap {
		t.Fatalf("expected ordered lifecycle and gap events, received %#v", read)
	}
	query := RecordingReadConfig{InputPaths: []string{path}, Format: FormatJSONL, OutputPath: filepath.Join(t.TempDir(), "query.jsonl"), Filter: Filter{EventMode: string(EventStop), HasExitCode: true, ExitCode: 7}}
	if err := runRecordingQuery(query); err != nil {
		t.Fatalf("run recording query: %v", err)
	}
	queryOutput, err := os.ReadFile(query.OutputPath)
	if err != nil {
		t.Fatalf("read query output: %v", err)
	}
	if !bytes.Contains(queryOutput, []byte(`"kind":"stop"`)) || bytes.Contains(queryOutput, []byte(`"kind":"start"`)) {
		t.Fatalf("expected only matching stop event, received %q", queryOutput)
	}
	var report bytes.Buffer
	if err := writeRecordingReport(&report, read); err != nil {
		t.Fatalf("write recording report: %v", err)
	}
	if !strings.Contains(report.String(), "events=3") || !strings.Contains(report.String(), "gaps=1") || !strings.Contains(report.String(), "failed_exits=1") {
		t.Fatalf("expected event, gap, and failure counts, received %q", report.String())
	}
}

func TestParseRecordingReadConfigRelativeTimeAndValidation(t *testing.T) {
	now := time.Unix(1000, 0)
	config, err := parseRecordingReadConfig("query", []string{"-since", "15m", "-until", "5m", "-exit-code", "0", "-events", "stop"}, now)
	if err != nil {
		t.Fatalf("parse recording query: %v", err)
	}
	if !config.Filter.Since.Equal(now.Add(-15*time.Minute)) || !config.Filter.Until.Equal(now.Add(-5*time.Minute)) || !config.Filter.HasExitCode || config.Filter.ExitCode != 0 {
		t.Fatalf("expected parsed relative times and zero exit code, received %#v", config.Filter)
	}
	if _, err := parseRecordingReadConfig("query", []string{"-since", "5m", "-until", "15m"}, now); err == nil {
		t.Fatalf("expected reversed time range to be rejected")
	}
	if _, err := parseRecordingReadConfig("query", []string{"-speed", "2"}, now); err == nil {
		t.Fatalf("expected replay-only option on query to be rejected")
	}
}

func TestParseRecordingReadConfigAcceptsRepeatedInputs(t *testing.T) {
	now := time.Unix(1000, 0)
	config, err := parseRecordingReadConfig("query", []string{"-input", "first.sqlite", "-input", "second.sqlite"}, now)
	if err != nil {
		t.Fatalf("parse repeated recording inputs: %v", err)
	}
	if len(config.InputPaths) != 2 || config.InputPaths[0] != "first.sqlite" || config.InputPaths[1] != "second.sqlite" {
		t.Fatalf("expected ordered recording inputs, received %#v", config.InputPaths)
	}
	defaultConfig, err := parseRecordingReadConfig("query", nil, now)
	if err != nil {
		t.Fatalf("parse default recording input: %v", err)
	}
	if len(defaultConfig.InputPaths) != 1 || defaultConfig.InputPaths[0] != "bottom.sqlite" {
		t.Fatalf("expected bottom.sqlite default input, received %#v", defaultConfig.InputPaths)
	}
	tooMany := []string{}
	for index := 0; index <= maxRecordingInputPaths; index++ {
		tooMany = append(tooMany, "-input", fmt.Sprintf("recording-%d.sqlite", index))
	}
	if _, err := parseRecordingReadConfig("query", tooMany, now); err == nil {
		t.Fatalf("expected more than %d recording inputs to be rejected", maxRecordingInputPaths)
	}
}

func TestRecordingReadersMergeMultipleInputs(t *testing.T) {
	directory := t.TempDir()
	firstPath := filepath.Join(directory, "first.sqlite")
	secondPath := filepath.Join(directory, "second.sqlite")
	writeReaderTestRecording(t, firstPath, []Event{
		{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Backend: BackendPoll},
		{Kind: EventStart, Time: time.Unix(30, 0), PID: 30, Backend: BackendPoll},
	})
	writeReaderTestRecording(t, secondPath, []Event{
		{Kind: EventStart, Time: time.Unix(20, 0), PID: 20, Backend: BackendPoll},
		{Kind: EventStart, Time: time.Unix(30, 0), PID: 31, Backend: BackendPoll},
	})
	config := RecordingReadConfig{InputPaths: []string{firstPath, secondPath}, Filter: Filter{EventMode: EventModeAll}}
	events, err := filteredRecordingEvents(config)
	if err != nil {
		t.Fatalf("merge recording inputs: %v", err)
	}
	expectedPIDs := []int{10, 20, 30, 31}
	if len(events) != len(expectedPIDs) {
		t.Fatalf("expected %d merged events, received %#v", len(expectedPIDs), events)
	}
	for index, expectedPID := range expectedPIDs {
		if events[index].PID != expectedPID {
			t.Fatalf("expected merged pid %d at index %d, received %#v", expectedPID, index, events)
		}
	}
	config.Limit = 2
	limited, err := filteredRecordingEvents(config)
	if err != nil {
		t.Fatalf("limit merged recording inputs: %v", err)
	}
	if len(limited) != 2 || limited[0].PID != 10 || limited[1].PID != 20 {
		t.Fatalf("expected earliest two merged events, received %#v", limited)
	}
	reportPath := filepath.Join(directory, "report.txt")
	reportConfig := RecordingReadConfig{
		InputPaths: []string{firstPath, secondPath}, OutputPath: reportPath, Filter: Filter{EventMode: EventModeAll},
	}
	if err := runRecordingReport(reportConfig); err != nil {
		t.Fatalf("report merged recording inputs: %v", err)
	}
	report, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read merged recording report: %v", err)
	}
	if !bytes.Contains(report, []byte("events=4")) {
		t.Fatalf("expected four reported events, received %q", report)
	}
	duplicate := RecordingReadConfig{InputPaths: []string{firstPath, filepath.Join(directory, ".", "first.sqlite")}, Filter: Filter{EventMode: EventModeAll}}
	if _, err := filteredRecordingEvents(duplicate); err == nil || !strings.Contains(err.Error(), "refer to the same file") {
		t.Fatalf("expected duplicate recording input rejection, received %v", err)
	}
}

func TestRecordingInputLimitDefersLaterFileValidationError(t *testing.T) {
	directory := t.TempDir()
	earlyPath := filepath.Join(directory, "early.sqlite")
	laterPath := filepath.Join(directory, "later.sqlite")
	writeReaderTestRecording(t, earlyPath, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Backend: BackendPoll}})
	writeReaderTestRecording(t, laterPath, []Event{{Kind: EventStart, Time: time.Unix(20, 0), PID: 20, Backend: BackendPoll}})
	db, err := sql.Open("sqlite", laterPath)
	if err != nil {
		t.Fatalf("open later recording for corruption: %v", err)
	}
	_, updateErr := db.Exec(`UPDATE events SET event_json = '{'`)
	closeErr := db.Close()
	if updateErr != nil || closeErr != nil {
		t.Fatalf("corrupt later recording: update=%v close=%v", updateErr, closeErr)
	}
	config := RecordingReadConfig{
		InputPaths: []string{earlyPath, laterPath}, Limit: 1, Filter: Filter{EventMode: EventModeAll},
	}
	events, err := filteredRecordingEvents(config)
	if err != nil {
		t.Fatalf("read earlier file before later validation error: %v", err)
	}
	if len(events) != 1 || events[0].PID != 10 {
		t.Fatalf("expected earlier file event through limit, received %#v", events)
	}
	config.Limit = 0
	if _, err := filteredRecordingEvents(config); err == nil || !strings.Contains(err.Error(), "decode versioned event") {
		t.Fatalf("expected unlimited merged reading to surface later file error, received %v", err)
	}
}

func TestRecordingQueryOpensEveryInputBeforeCreatingOutput(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "recording.sqlite")
	missingPath := filepath.Join(directory, "missing.sqlite")
	outputPath := filepath.Join(directory, "query.jsonl")
	writeReaderTestRecording(t, inputPath, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Backend: BackendPoll}})
	config := RecordingReadConfig{
		InputPaths: []string{inputPath, missingPath}, OutputPath: outputPath,
		Format: FormatJSONL, Filter: Filter{EventMode: EventModeAll},
	}
	if err := runRecordingQuery(config); err == nil || !strings.Contains(err.Error(), missingPath) {
		t.Fatalf("expected missing recording error containing %q, received %v", missingPath, err)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("expected no query output after input validation failure, received %v", err)
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
		t.Fatalf("write recording comparison: %v", err)
	}
	if !strings.Contains(output.String(), "/bin/compiler <- 1") || !strings.Contains(output.String(), "/bin/linker <- 1") || !strings.Contains(output.String(), "+1") {
		t.Fatalf("expected duration, failure, and added process changes, received %q", output.String())
	}
	if _, err := parseRecordingCompareConfig([]string{"-before", "same.sqlite", "-after", "same.sqlite"}); err == nil {
		t.Fatalf("expected identical comparison paths to be rejected")
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
		t.Fatalf("write ancestry report: %v", err)
	}
	if !strings.Contains(report.String(), "/bin/shell -> /bin/worker") || !strings.Contains(report.String(), "/sbin/init-before -> /bin/shell") {
		t.Fatalf("expected every captured ancestry edge, received %q", report.String())
	}
	var comparison bytes.Buffer
	if err := writeRecordingComparison(&comparison, []Event{before}, []Event{after}); err != nil {
		t.Fatalf("write ancestry comparison: %v", err)
	}
	if !strings.Contains(comparison.String(), "/bin/worker <- /bin/shell <- /sbin/init-before") || !strings.Contains(comparison.String(), "/bin/worker <- /bin/shell <- /sbin/init-after") {
		t.Fatalf("expected higher ancestors to affect comparison groups, received %q", comparison.String())
	}
}

func TestSQLiteRecordingReaderIsReadOnlyAndEscapesPath(t *testing.T) {
	directory := t.TempDir()
	originalPath := filepath.Join(directory, "recording.sqlite")
	path := filepath.Join(directory, "recording # name.sqlite")
	writeReaderTestRecording(t, originalPath, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Command: "worker", Backend: BackendPoll}})
	if err := os.Rename(originalPath, path); err != nil {
		t.Fatalf("rename recording to escaped path: %v", err)
	}
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		t.Fatalf("open read-only recording: %v", err)
	}
	if _, err := reader.db.Exec(`DELETE FROM events`); err == nil {
		t.Fatal("expected the recording reader connection to reject writes")
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close read-only recording: %v", err)
	}
	events, err := readSQLiteRecording(path)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	if len(events) != 1 || events[0].PID != 10 {
		t.Fatalf("expected one preserved event, received %#v", events)
	}
}

func TestRecordingCommandsRejectOutputAliases(t *testing.T) {
	directory := t.TempDir()
	inputPath := filepath.Join(directory, "recording.sqlite")
	comparisonPath := filepath.Join(directory, "comparison.sqlite")
	writeReaderTestRecording(t, inputPath, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Backend: BackendPoll}})
	writeReaderTestRecording(t, comparisonPath, []Event{{Kind: EventStart, Time: time.Unix(20, 0), PID: 20, Backend: BackendPoll}})
	inputAlias := filepath.Dir(inputPath) + string(os.PathSeparator) + "." + string(os.PathSeparator) + filepath.Base(inputPath)

	query := RecordingReadConfig{InputPaths: []string{inputPath}, OutputPath: inputAlias, Format: FormatText, Filter: Filter{EventMode: EventModeAll}}
	if err := runRecordingQuery(query); err == nil || !strings.Contains(err.Error(), "bottom query output path") || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected query output alias rejection, received %v", err)
	}
	comparisonAlias := filepath.Join(filepath.Dir(comparisonPath), ".", filepath.Base(comparisonPath))
	query = RecordingReadConfig{InputPaths: []string{inputPath, comparisonPath}, OutputPath: comparisonAlias, Format: FormatText, Filter: Filter{EventMode: EventModeAll}}
	if err := runRecordingQuery(query); err == nil || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected query output alias rejection for every input, received %v", err)
	}
	report := RecordingReadConfig{InputPaths: []string{inputPath}, OutputPath: inputAlias, Filter: Filter{EventMode: EventModeAll}}
	if err := runRecordingReport(report); err == nil || !strings.Contains(err.Error(), "bottom report output path") || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected report output alias rejection, received %v", err)
	}
	compare := RecordingCompareConfig{BeforePath: inputPath, AfterPath: comparisonPath, OutputPath: inputAlias}
	if err := runRecordingCompare(compare); err == nil || !strings.Contains(err.Error(), "bottom compare output path") || !strings.Contains(err.Error(), "resolves to input recording") {
		t.Fatalf("expected comparison output alias rejection, received %v", err)
	}
	if _, err := readSQLiteRecording(inputPath); err != nil {
		t.Fatalf("read input after rejected aliases: %v", err)
	}
}

func TestRecordingStreamStopsAtLimitBeforeLaterInvalidRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Command: "worker accepted", Backend: BackendPoll}})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open recording for invalid row setup: %v", err)
	}
	invalidTime := time.Unix(20, 0).UTC()
	_, insertErr := db.Exec(`INSERT INTO events (time, time_key, kind, backend, schema_version, event_json) VALUES (?, ?, ?, ?, ?, ?)`,
		invalidTime.Format(time.RFC3339Nano), formatRecordingTimeKey(invalidTime), EventStart, BackendPoll, EventSchemaVersion, `{`)
	closeErr := db.Close()
	if insertErr != nil || closeErr != nil {
		t.Fatalf("add later invalid row: insert=%v close=%v", insertErr, closeErr)
	}

	queryPath := filepath.Join(t.TempDir(), "query.jsonl")
	query := RecordingReadConfig{
		InputPaths: []string{path}, OutputPath: queryPath, Format: FormatJSONL, Limit: 1,
		Filter: Filter{EventMode: EventModeAll, Include: []string{"worker"}},
	}
	if err := runRecordingQuery(query); err != nil {
		t.Fatalf("stream limited query: %v", err)
	}
	queryOutput, err := os.ReadFile(queryPath)
	if err != nil {
		t.Fatalf("read limited query: %v", err)
	}
	if !bytes.Contains(queryOutput, []byte("worker accepted")) {
		t.Fatalf("expected first matching event, received %q", queryOutput)
	}

	reportPath := filepath.Join(t.TempDir(), "report.txt")
	report := RecordingReadConfig{
		InputPaths: []string{path}, OutputPath: reportPath, Limit: 1,
		Filter: Filter{EventMode: EventModeAll, Include: []string{"worker"}},
	}
	if err := runRecordingReport(report); err != nil {
		t.Fatalf("stream limited report: %v", err)
	}
	reportOutput, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read limited report: %v", err)
	}
	if !bytes.Contains(reportOutput, []byte("events=1")) {
		t.Fatalf("expected one reported event, received %q", reportOutput)
	}
	if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "decode versioned event") {
		t.Fatalf("expected unlimited reading to reach the invalid later row, received %v", err)
	}
}

func TestSQLiteRecordingReaderDecodesLegacyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	writeReaderTestRecording(t, path, nil)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open recording for legacy row setup: %v", err)
	}
	legacyTime := time.Unix(30, 0).UTC()
	_, insertErr := db.Exec(`INSERT INTO events (
		time, time_key, kind, pid, parent_pid, user, command, exe, duration_ms, backend, schema_version, event_json
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		legacyTime.Format(time.RFC3339Nano), formatRecordingTimeKey(legacyTime), EventStop, 30, 1, "jer", "legacy worker", "/bin/legacy", 25, BackendPoll, EventSchemaVersion)
	closeErr := db.Close()
	if insertErr != nil || closeErr != nil {
		t.Fatalf("add legacy row: insert=%v close=%v", insertErr, closeErr)
	}
	events, err := readSQLiteRecording(path)
	if err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if len(events) != 1 || events[0].PID != 30 || events[0].Command != "legacy worker" || events[0].Kind != EventStop {
		t.Fatalf("expected decoded legacy stop event, received %#v", events)
	}
}

func TestHumanReportsEscapeProcessControls(t *testing.T) {
	processName := "/bin/worker\x1b[2J\u009b31m"
	events := []Event{{Kind: EventStart, Time: time.Unix(10, 0), Exe: processName, ParentPID: 1}}
	var report bytes.Buffer
	if err := writeRecordingReport(&report, events); err != nil {
		t.Fatalf("write escaped report: %v", err)
	}
	if strings.ContainsRune(report.String(), '\x1b') || strings.ContainsRune(report.String(), '\u009b') || !strings.Contains(report.String(), `\x1b`) || !strings.Contains(report.String(), `\x9b`) {
		t.Fatalf("expected escaped report controls, received %q", report.String())
	}
	var comparison bytes.Buffer
	if err := writeRecordingComparison(&comparison, nil, events); err != nil {
		t.Fatalf("write escaped comparison: %v", err)
	}
	if strings.ContainsRune(comparison.String(), '\x1b') || strings.ContainsRune(comparison.String(), '\u009b') || !strings.Contains(comparison.String(), `\x1b`) || !strings.Contains(comparison.String(), `\x9b`) {
		t.Fatalf("expected escaped comparison controls, received %q", comparison.String())
	}
}

func TestSQLiteRecordingOrdersAndRangesSubMillisecondTimes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	base := time.Date(2026, time.July, 9, 12, 0, 0, 0, time.UTC)
	earlier := base.Add(100 * time.Millisecond)
	later := earlier.Add(time.Microsecond)
	writeReaderTestRecording(t, path, []Event{
		{Kind: EventStart, Time: later, Sequence: 2, PID: 2, Command: "later", Backend: BackendPoll},
		{Kind: EventStart, Time: earlier, Sequence: 1, PID: 1, Command: "earlier", Backend: BackendPoll},
	})
	events, err := readSQLiteRecording(path)
	if err != nil {
		t.Fatalf("read sub-millisecond recording: %v", err)
	}
	if len(events) != 2 || events[0].PID != 1 || events[1].PID != 2 {
		t.Fatalf("expected exact chronological order for variable-width source timestamps, received %#v", events)
	}
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		t.Fatalf("open sub-millisecond recording: %v", err)
	}
	filtered := []Event{}
	readErr := reader.Stream(Filter{EventMode: EventModeAll, Until: earlier}, 1, func(event Event) error {
		filtered = append(filtered, event)
		return nil
	})
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read exact sub-millisecond range: read=%v close=%v", readErr, closeErr)
	}
	if len(filtered) != 1 || filtered[0].PID != 1 {
		t.Fatalf("expected limit to retain the earlier in-range event, received %#v", filtered)
	}
}

func TestSQLiteRecordingUsesNormalizedColumnsWhenRawJSONDisagrees(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	firstTime := time.Unix(10, 0).UTC()
	secondTime := time.Unix(20, 0).UTC()
	writeReaderTestRecording(t, path, []Event{
		{Kind: EventStart, Time: firstTime, Sequence: 1, PID: 10, Exe: "/normalized-match", Backend: BackendPoll},
		{Kind: EventStart, Time: secondTime, Sequence: 2, PID: 20, Exe: "/normalized-match", Backend: BackendPoll},
	})
	rawEvent := Event{SchemaVersion: EventSchemaVersion, Kind: EventStop, Time: secondTime.Add(time.Hour), PID: 999, Exe: "/raw-disagreement", Backend: BackendPoll}
	rawJSON, err := json.Marshal(rawEvent)
	if err != nil {
		t.Fatalf("encode disagreeing raw event: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open recording for raw disagreement: %v", err)
	}
	_, updateErr := db.Exec(`UPDATE events SET event_json = ? WHERE pid = ?`, string(rawJSON), 10)
	closeErr := db.Close()
	if updateErr != nil || closeErr != nil {
		t.Fatalf("store raw disagreement: update=%v close=%v", updateErr, closeErr)
	}
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		t.Fatalf("open recording with raw disagreement: %v", err)
	}
	filtered := []Event{}
	readErr := reader.Stream(Filter{EventMode: string(EventStart), ExeContains: "normalized-match"}, 1, func(event Event) error {
		filtered = append(filtered, event)
		return nil
	})
	readerCloseErr := reader.Close()
	if readErr != nil || readerCloseErr != nil {
		t.Fatalf("read recording with raw disagreement: read=%v close=%v", readErr, readerCloseErr)
	}
	if len(filtered) != 1 || filtered[0].PID != 10 || filtered[0].Kind != EventStart || filtered[0].Exe != "/normalized-match" {
		t.Fatalf("expected indexed normalized fields to remain authoritative through limit, received %#v", filtered)
	}
}

func TestRecordingAggregationsRemainExactWithManyProcessNames(t *testing.T) {
	events := []Event{}
	for index := 0; index < 64; index++ {
		events = append(events, Event{Kind: EventStart, Exe: fmt.Sprintf("/bin/process-%02d", index), ParentPID: 1})
	}
	for index := 0; index < 3; index++ {
		events = append(events, Event{Kind: EventStart, Exe: "/bin/frequent", ParentPID: 1})
	}
	var report bytes.Buffer
	if err := writeRecordingReport(&report, events); err != nil {
		t.Fatalf("write high-cardinality report: %v", err)
	}
	if !strings.Contains(report.String(), "       3  /bin/frequent") {
		t.Fatalf("expected exact top executable count, received %q", report.String())
	}
	topExecutables := strings.Split(strings.Split(report.String(), "Top executables\n")[1], "\nTop parents")[0]
	if lineCount := len(strings.Split(strings.TrimSpace(topExecutables), "\n")); lineCount != 10 {
		t.Fatalf("expected ten bounded top executable rows, received %d in %q", lineCount, topExecutables)
	}
	var comparison bytes.Buffer
	if err := writeRecordingComparison(&comparison, nil, events[:64]); err != nil {
		t.Fatalf("write high-cardinality comparison: %v", err)
	}
	if lineCount := len(strings.Split(strings.TrimSpace(comparison.String()), "\n")); lineCount != 66 {
		t.Fatalf("expected two headings and 64 exact process rows, received %d", lineCount)
	}
	if !strings.Contains(comparison.String(), "/bin/process-00 <- 1") || !strings.Contains(comparison.String(), "/bin/process-63 <- 1") {
		t.Fatalf("expected first and last process groups in comparison, received %q", comparison.String())
	}
}

func TestRecordingAggregationsUseFileBackedTemporaryStorage(t *testing.T) {
	db, err := openTemporaryAggregationDB("recording aggregation test")
	if err != nil {
		t.Fatalf("open temporary aggregation database: %v", err)
	}
	var tempStore int
	queryErr := db.QueryRow(`PRAGMA temp_store`).Scan(&tempStore)
	closeErr := db.Close()
	if queryErr != nil || closeErr != nil {
		t.Fatalf("inspect temporary aggregation database: query=%v close=%v", queryErr, closeErr)
	}
	if tempStore != 1 {
		t.Fatalf("expected file-backed SQLite temporary storage mode 1, received %d", tempStore)
	}
}

func TestRecordingQueryPlansUseAvailableIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(10, 0), PID: 10, Exe: "/bin/worker", Backend: BackendPoll}})
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		t.Fatalf("open recording for query plan: %v", err)
	}
	rows, err := reader.querySourceRows(recordingQuerySources[0], Filter{EventMode: string(EventStart)}, 1, true)
	if err != nil {
		t.Fatalf("explain kind recording query: %v", err)
	}
	kindPlan := recordingQueryPlanDetails(t, rows)
	if !strings.Contains(kindPlan, "events_kind_time") || strings.Contains(kindPlan, "SCAN events") || strings.Contains(kindPlan, "TEMP B-TREE") {
		t.Fatalf("expected indexed kind query without a full sort, received %s", kindPlan)
	}

	timeRows, err := reader.querySourceRows(recordingQuerySources[0], Filter{EventMode: EventModeAll, Since: time.Unix(5, 0)}, 1, true)
	if err != nil {
		t.Fatalf("explain time recording query: %v", err)
	}
	timePlan := recordingQueryPlanDetails(t, timeRows)
	if !strings.Contains(timePlan, "events_time") || strings.Contains(timePlan, "SCAN events") || strings.Contains(timePlan, "TEMP B-TREE") {
		t.Fatalf("expected indexed time query without a full sort, received %s", timePlan)
	}

	pidRows, err := reader.db.Query(`EXPLAIN QUERY PLAN SELECT event_json FROM events WHERE pid = ? ORDER BY time_key, sequence, id LIMIT 1`, 10)
	if err != nil {
		t.Fatalf("explain pid recording query: %v", err)
	}
	pidPlan := recordingQueryPlanDetails(t, pidRows)
	if !strings.Contains(pidPlan, "events_pid_time") || strings.Contains(pidPlan, "SCAN events") || strings.Contains(pidPlan, "TEMP B-TREE") {
		t.Fatalf("expected pid index, received %s", pidPlan)
	}

	exeRows, err := reader.db.Query(`EXPLAIN QUERY PLAN SELECT event_json FROM events WHERE exe = ? ORDER BY time_key, sequence, id LIMIT 1`, "/bin/worker")
	if err != nil {
		t.Fatalf("explain executable recording query: %v", err)
	}
	exePlan := recordingQueryPlanDetails(t, exeRows)
	if !strings.Contains(exePlan, "events_exe_time") || strings.Contains(exePlan, "SCAN events") || strings.Contains(exePlan, "TEMP B-TREE") {
		t.Fatalf("expected executable index, received %s", exePlan)
	}

	if err := reader.Close(); err != nil {
		t.Fatalf("close recording after query plan: %v", err)
	}
}

func recordingQueryPlanDetails(t *testing.T, rows *sql.Rows) string {
	t.Helper()
	details := []string{}
	for rows.Next() {
		var id int
		var parent int
		var unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan recording query plan: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recording query plan: %v", err)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close recording query plan: %v", err)
	}
	return strings.Join(details, "\n")
}

func writeReaderTestRecording(t *testing.T, path string, events []Event) {
	t.Helper()
	config := Config{Backend: BackendPoll, Format: FormatSQLite, OutputPath: path, RecorderBuffer: 8, SQLiteBatch: 2, SQLiteFlush: time.Millisecond}
	recorder, err := newRecorder(config)
	if err != nil {
		t.Fatalf("create reader test recording: %v", err)
	}
	for _, event := range events {
		if err := recorder.Write(event); err != nil {
			t.Fatalf("write reader test event: %v", err)
		}
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("close reader test recording: %v", err)
	}
}
