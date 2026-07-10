package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestRecordingOutputsAreCreatedOwnerOnly(t *testing.T) {
	directory := t.TempDir()
	textPath := filepath.Join(directory, "events.jsonl")
	_, closer, err := openOutput(textPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnlyFile(t, textPath)

	sqlitePath := filepath.Join(directory, "events.sqlite")
	recorder, err := newSQLiteRecorder(sqlitePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	assertOwnerOnlyFile(t, sqlitePath)
}

func TestJSONLAndCSVStoreSessionAndGapRecords(t *testing.T) {
	directory := t.TempDir()
	session := testRecordingSession(t)
	event := Event{
		SchemaVersion: EventSchemaVersion,
		SessionID:     "capture-session",
		Sequence:      7,
		Host:          "capture-host",
		BootID:        "capture-boot",
		Kind:          EventGap,
		Time:          time.Date(2026, 7, 9, 12, 0, 0, 3, time.UTC),
		ObservedAt:    time.Date(2026, 7, 9, 12, 0, 1, 4, time.UTC),
		Backend:       BackendPoll,
		Count:         4,
		Message:       "four lifecycle notices were unavailable",
	}

	jsonPath := filepath.Join(directory, "events.jsonl")
	jsonRecorder, err := newJSONLRecorder(jsonPath, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonRecorder.Write(event); err != nil {
		t.Fatal(err)
	}
	if err := jsonRecorder.Close(); err != nil {
		t.Fatal(err)
	}
	jsonContents, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	jsonLines := strings.Split(strings.TrimSpace(string(jsonContents)), "\n")
	if len(jsonLines) != 3 {
		t.Fatalf("expected session start, gap, and session end JSONL records, received %d lines: %s", len(jsonLines), jsonContents)
	}
	type storedRecord struct {
		RecordType         string `json:"record_type"`
		RecordingSessionID string `json:"recording_session_id"`
		SessionID          string `json:"session_id"`
		Sequence           uint64 `json:"sequence"`
		Kind               string `json:"kind"`
		Message            string `json:"message"`
	}
	var jsonGap storedRecord
	if err := json.Unmarshal([]byte(jsonLines[1]), &jsonGap); err != nil {
		t.Fatal(err)
	}
	jsonGapMatches := jsonGap.RecordType == recordTypeGap && jsonGap.RecordingSessionID == session.ID &&
		jsonGap.SessionID == event.SessionID && jsonGap.Sequence == event.Sequence && jsonGap.Kind == string(EventGap) &&
		jsonGap.Message == event.Message
	if !jsonGapMatches {
		t.Fatalf("unexpected JSONL gap record: %+v", jsonGap)
	}

	csvPath := filepath.Join(directory, "events.csv")
	csvRecorder, err := newCSVRecorderWithSession(csvPath, session)
	if err != nil {
		t.Fatal(err)
	}
	if err := csvRecorder.Write(event); err != nil {
		t.Fatal(err)
	}
	if err := csvRecorder.Close(); err != nil {
		t.Fatal(err)
	}
	csvFile, err := os.Open(csvPath)
	if err != nil {
		t.Fatal(err)
	}
	records, readErr := csv.NewReader(csvFile).ReadAll()
	closeErr := csvFile.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if len(records) != 4 {
		t.Fatalf("expected CSV header, session start, gap, and session end, received %d rows", len(records))
	}
	columns := csvColumns(records[0])
	gapRow := records[2]
	csvGapMatches := gapRow[columns["record_type"]] == recordTypeGap && gapRow[columns["recording_session_id"]] == session.ID &&
		gapRow[columns["session_id"]] == event.SessionID && gapRow[columns["message"]] == event.Message
	if !csvGapMatches {
		t.Fatalf("unexpected CSV gap row: %v", gapRow)
	}
}

func TestCSVRejectsAnIncompatibleExistingHeader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.csv")
	if err := os.WriteFile(path, []byte("time,kind,pid\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := newCSVRecorderWithSession(path, testRecordingSession(t))
	if err == nil || !strings.Contains(err.Error(), "expected header") || !strings.Contains(err.Error(), `received ["time" "kind" "pid"]`) {
		t.Fatalf("expected detailed incompatible CSV schema error, received %v", err)
	}
}

func TestSQLiteMigratesLegacySchemaAndStoresIndexedSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		time TEXT NOT NULL,
		kind TEXT NOT NULL,
		pid INTEGER,
		parent_pid INTEGER,
		user TEXT,
		command TEXT,
		exe TEXT,
		cwd TEXT,
		duration_ms INTEGER,
		exit_code INTEGER,
		backend TEXT NOT NULL,
		count INTEGER,
		window_ms INTEGER,
		message TEXT,
		parent_chain TEXT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	legacyTimeText := "2026-07-09T05:00:00.100001-07:00"
	if _, err := db.Exec(`INSERT INTO events (time, kind, pid, backend) VALUES (?, ?, ?, ?)`, legacyTimeText, EventStart, 7, BackendPoll); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	session := testRecordingSession(t)
	recorder, err := newSQLiteRecorderWithOptions(path, session, recorderOptions{bufferSize: -1, sqliteBatchSize: 2, flushInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	event := Event{
		SchemaVersion: EventSchemaVersion,
		SessionID:     "capture-session",
		Sequence:      11,
		Kind:          EventStart,
		Time:          time.Now().UTC(),
		ObservedAt:    time.Now().UTC(),
		ProcessID:     "42:100",
		PID:           42,
		Command:       "worker --serve",
		Exe:           "/usr/bin/worker",
		SystemdUnit:   "worker.service",
		ContainerID:   "container-1",
		Backend:       BackendPoll,
	}
	gap := Event{
		SchemaVersion: EventSchemaVersion,
		SessionID:     event.SessionID,
		Sequence:      12,
		Kind:          EventGap,
		Time:          time.Now().UTC(),
		ObservedAt:    time.Now().UTC(),
		Backend:       BackendPoll,
		Count:         3,
		Message:       "expected sequence 9, received 12",
	}
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	if recorder.pending != 1 {
		t.Fatalf("expected one pending SQLite row before batch commit, received %d", recorder.pending)
	}
	if err := recorder.Write(gap); err != nil {
		t.Fatal(err)
	}
	if recorder.pending != 0 {
		t.Fatalf("expected batch to commit at two rows, received %d pending rows", recorder.pending)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var version int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != recordingSchemaVersion {
		t.Fatalf("expected recording schema version %d, received %d", recordingSchemaVersion, version)
	}
	var migratedTimeKey string
	if err := db.QueryRow(`SELECT time_key FROM events WHERE pid = 7`).Scan(&migratedTimeKey); err != nil {
		t.Fatal(err)
	}
	legacyTime, err := time.Parse(time.RFC3339Nano, legacyTimeText)
	if err != nil {
		t.Fatal(err)
	}
	if migratedTimeKey != formatRecordingTimeKey(legacyTime) {
		t.Fatalf("expected migrated UTC nanosecond time key %q, received %q", formatRecordingTimeKey(legacyTime), migratedTimeKey)
	}
	var storedSessionID string
	var processID string
	var eventJSON string
	if err := db.QueryRow(`SELECT session_id, process_id, event_json FROM events WHERE pid = 42`).Scan(&storedSessionID, &processID, &eventJSON); err != nil {
		t.Fatal(err)
	}
	if storedSessionID != event.SessionID || processID != event.ProcessID || !strings.Contains(eventJSON, `"systemd_unit":"worker.service"`) {
		t.Fatalf("unexpected stored event session=%q process=%q json=%s", storedSessionID, processID, eventJSON)
	}
	var gapMessage string
	var gapCount int
	if err := db.QueryRow(`SELECT message, count FROM gaps WHERE sequence = 12`).Scan(&gapMessage, &gapCount); err != nil {
		t.Fatal(err)
	}
	if gapMessage != gap.Message || gapCount != gap.Count {
		t.Fatalf("unexpected stored gap message=%q count=%d", gapMessage, gapCount)
	}
	var endedAt string
	if err := db.QueryRow(`SELECT ended_at FROM sessions WHERE id = ?`, session.ID).Scan(&endedAt); err != nil {
		t.Fatal(err)
	}
	if endedAt == "" {
		t.Fatal("expected SQLite session end timestamp")
	}
	var indexCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name IN (
		'events_session_sequence', 'events_process_time', 'events_exe_time', 'events_unit_time', 'events_container_time',
		'events_legacy_time', 'gaps_legacy_time'
	)`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 7 {
		t.Fatalf("expected seven query indexes, received %d", indexCount)
	}
	for _, name := range []string{
		"events_recording_session_time", "events_time", "events_kind_time", "events_pid_time", "events_process_time",
		"events_parent_time", "events_exe_time", "events_command_time", "events_exit_time", "events_unit_time",
		"events_container_time", "gaps_recording_session_time", "gaps_time",
	} {
		var definition string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&definition); err != nil {
			t.Fatalf("read migrated index %q: %v", name, err)
		}
		if !strings.Contains(definition, "time_key") || !strings.Contains(definition, "sequence") {
			t.Fatalf("expected migrated index %q to order by normalized time and sequence, received %q", name, definition)
		}
	}
}

func TestSQLiteRetentionRemovesExpiredSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "retention.sqlite")
	oldSession := testRecordingSession(t)
	recorder, err := newSQLiteRecorderWithOptions(path, oldSession, recorderOptions{bufferSize: -1, sqliteBatchSize: 1, flushInterval: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	oldEventTime := time.Now().UTC().Add(-48 * time.Hour)
	if err := recorder.Write(Event{Kind: EventStart, Time: oldEventTime, PID: 8, Command: "old", Backend: BackendPoll}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	oldStartedAt := oldEventTime.Format(time.RFC3339Nano)
	if _, err := db.Exec(`UPDATE sessions SET started_at = ?, ended_at = ? WHERE id = ?`, oldStartedAt, oldStartedAt, oldSession.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	newSession := testRecordingSession(t)
	recorder, err = newSQLiteRecorderWithOptions(path, newSession, recorderOptions{
		bufferSize: -1, sqliteBatchSize: 1, flushInterval: time.Second, retention: 24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var oldSessions int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE id = ?`, oldSession.ID).Scan(&oldSessions); err != nil {
		t.Fatal(err)
	}
	var oldEvents int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events WHERE recording_session_id = ?`, oldSession.ID).Scan(&oldEvents); err != nil {
		t.Fatal(err)
	}
	if oldSessions != 0 || oldEvents != 0 {
		t.Fatalf("expected expired session and events to be removed, received sessions=%d events=%d", oldSessions, oldEvents)
	}
}

func TestBufferedRecorderReportsBackpressureAndSinkFailure(t *testing.T) {
	gate := &gatedRecorder{entered: make(chan struct{}), release: make(chan struct{})}
	recorder := newBufferedRecorder(gate, 1, time.Hour)
	event := Event{Kind: EventStart, Time: time.Now(), PID: 1, Backend: BackendPoll}
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-gate.entered:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for buffered recorder to begin the first write")
	}
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	err := recorder.Write(event)
	if !errors.Is(err, errRecorderBackpressure) || !strings.Contains(err.Error(), "capacity=1 queued=1") {
		t.Fatalf("expected detailed recorder backpressure error, received %v", err)
	}
	close(gate.release)
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}

	failure := errors.New("storage device rejected write")
	failing := &failingRecorder{failure: failure, called: make(chan struct{})}
	recorder = newBufferedRecorder(failing, 2, time.Hour)
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	select {
	case <-failing.called:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for recording sink failure")
	}
	if err := recorder.Close(); !errors.Is(err, failure) {
		t.Fatalf("expected close to return recording sink failure, received %v", err)
	}
}

func TestBufferedSQLiteFlushesPartialBatchOnInterval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flush.sqlite")
	session := testRecordingSession(t)
	sqliteTarget, err := newSQLiteRecorderWithOptions(path, session, recorderOptions{
		bufferSize: -1, sqliteBatchSize: 100, flushInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	recorder := newBufferedRecorder(sqliteTarget, 4, 10*time.Millisecond)
	if err := recorder.Write(Event{Kind: EventStart, Time: time.Now().UTC(), PID: 31, Backend: BackendPoll}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		sqliteTarget.mutex.Lock()
		pending := sqliteTarget.pending
		sqliteTarget.mutex.Unlock()
		if pending == 0 && sqliteEventCount(t, path) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial SQLite batch was not committed within the configured flush interval")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSessionRecorderPreservesProvidedEventMetadata(t *testing.T) {
	target := &collectingRecorder{}
	session := testRecordingSession(t)
	recorder := newSessionRecorder(target, session)
	observedAt := time.Date(2026, 7, 9, 12, 30, 0, 0, time.FixedZone("offset", -7*60*60))
	event := Event{
		SchemaVersion: 9,
		SessionID:     "provided-session",
		Sequence:      44,
		Host:          "provided-host",
		BootID:        "provided-boot",
		Kind:          EventExec,
		Time:          observedAt,
		ObservedAt:    observedAt,
		PID:           90,
		Backend:       BackendLinuxProcConnector,
	}
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if len(target.events) != 1 {
		t.Fatalf("expected one stored event, received %d", len(target.events))
	}
	stored := target.events[0]
	metadataMatches := stored.SchemaVersion == event.SchemaVersion && stored.SessionID == event.SessionID &&
		stored.Sequence == event.Sequence && stored.Host == event.Host && stored.BootID == event.BootID &&
		stored.ObservedAt.Equal(event.ObservedAt)
	if !metadataMatches {
		t.Fatalf("expected provided event metadata to remain unchanged, received %+v", stored)
	}
}

func TestMultiRecorderWritesEverySink(t *testing.T) {
	first := &collectingRecorder{}
	second := &collectingRecorder{}
	recorder := newMultiRecorder(first, second)
	event := Event{Kind: EventStop, Time: time.Now(), PID: 17, Backend: BackendPoll}
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	if len(first.events) != 1 || len(second.events) != 1 || !first.closed || !second.closed {
		t.Fatalf("expected both sinks to receive and close, received first=%+v second=%+v", first, second)
	}
}

func TestRotationAndRedactionPreservePrivateSegments(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "events.jsonl")
	config := Config{
		Backend:        BackendPoll,
		Format:         FormatJSONL,
		OutputPath:     path,
		RecorderBuffer: -1,
		RotateSize:     1,
		Redact:         []string{"secret-token"},
	}
	recorder, err := newRecorder(config)
	if err != nil {
		t.Fatal(err)
	}
	event := Event{Kind: EventStart, Time: time.Now().UTC(), PID: 29, Command: "worker --token secret-token", Backend: BackendPoll}
	if err := recorder.Write(event); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	segments, err := filepath.Glob(filepath.Join(directory, "events.*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(segments) != 1 {
		t.Fatalf("expected one rotated segment, received %v", segments)
	}
	paths := append(segments, path)
	combined := strings.Builder{}
	for _, segment := range paths {
		assertOwnerOnlyFile(t, segment)
		contents, err := os.ReadFile(segment)
		if err != nil {
			t.Fatal(err)
		}
		combined.Write(contents)
	}
	if strings.Contains(combined.String(), "secret-token") || !strings.Contains(combined.String(), "[REDACTED]") {
		t.Fatalf("expected exact redaction across rotated output, received %s", combined.String())
	}
}

func TestRedactionCoversRecordedContextWithoutMutatingInput(t *testing.T) {
	event := Event{
		Host:        "secret-host",
		ProcessID:   "secret-process",
		Command:     "worker --token secret-token",
		Exe:         "/secret/bin/worker",
		Cwd:         "/secret/work",
		User:        "secret-user",
		Cgroup:      "/secret/cgroup",
		SystemdUnit: "secret.service",
		ContainerID: "secret-container",
		Message:     "secret diagnostic",
		ParentChain: []ProcessSummary{{PID: 1, ProcessID: "secret-parent", Command: "secret parent"}},
	}
	redacted := redactEvent(event, []string{"secret"})
	encoded, err := json.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "secret") || !strings.Contains(string(encoded), "[REDACTED]") {
		t.Fatalf("expected every recorded context field to be redacted, received %s", encoded)
	}
	if event.ParentChain[0].Command != "secret parent" || event.Command != "worker --token secret-token" {
		t.Fatalf("expected source event to remain unchanged, received %+v", event)
	}
}

type gatedRecorder struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (recorder *gatedRecorder) Write(Event) error {
	recorder.once.Do(func() { close(recorder.entered) })
	<-recorder.release
	return nil
}

func (recorder *gatedRecorder) Close() error {
	return nil
}

type failingRecorder struct {
	failure error
	called  chan struct{}
	once    sync.Once
}

func (recorder *failingRecorder) Write(Event) error {
	recorder.once.Do(func() { close(recorder.called) })
	return recorder.failure
}

func (recorder *failingRecorder) Close() error {
	return nil
}

type collectingRecorder struct {
	events []Event
	closed bool
}

func (recorder *collectingRecorder) Write(event Event) error {
	recorder.events = append(recorder.events, event)
	return nil
}

func (recorder *collectingRecorder) Close() error {
	recorder.closed = true
	return nil
}

func testRecordingSession(t *testing.T) recordingSession {
	t.Helper()
	session, err := newRecordingSession(BackendPoll)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func assertOwnerOnlyFile(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if permissions := info.Mode().Perm(); permissions != 0o600 {
		t.Fatalf("expected owner-only permissions 0600 for %q, received %04o", path, permissions)
	}
}

func csvColumns(header []string) map[string]int {
	columns := make(map[string]int, len(header))
	for index, name := range header {
		columns[name] = index
	}
	return columns
}

func sqliteEventCount(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
