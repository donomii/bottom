package main

import (
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
