package main

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordingLimitDefersLaterCrossSourceValidationError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.sqlite")
	writeReaderTestRecording(t, path, []Event{
		{Kind: EventStart, Time: time.Unix(1, 0), Sequence: 1, PID: 1, Backend: BackendPoll},
		{Kind: EventGap, Time: time.Unix(100, 0), Sequence: 2, Backend: BackendPoll, Message: "later gap"},
	})
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open recording for later gap corruption: %v", err)
	}
	_, updateErr := db.Exec(`UPDATE gaps SET event_json = '{'`)
	closeErr := db.Close()
	if updateErr != nil || closeErr != nil {
		t.Fatalf("corrupt later gap: update=%v close=%v", updateErr, closeErr)
	}
	events, err := readRecordingWithLimit(path, 1)
	if err != nil {
		t.Fatalf("read earlier event before later cross-source error: %v", err)
	}
	if len(events) != 1 || events[0].PID != 1 {
		t.Fatalf("expected the earlier event through limit, received %#v", events)
	}
	if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "decode versioned event") {
		t.Fatalf("expected unlimited reading to surface the later gap error, received %v", err)
	}
}

func TestSQLiteRecordingRejectsInvalidNormalizedTimesAndKinds(t *testing.T) {
	t.Run("zero time", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recording.sqlite")
		writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open recording for zero time: %v", err)
		}
		zero := time.Time{}
		_, updateErr := db.Exec(`UPDATE events SET time = ?, time_key = ?`, zero.Format(time.RFC3339Nano), formatRecordingTimeKey(zero))
		closeErr := db.Close()
		if updateErr != nil || closeErr != nil {
			t.Fatalf("store zero normalized time: update=%v close=%v", updateErr, closeErr)
		}
		if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "expected a non-zero time") {
			t.Fatalf("expected zero normalized time rejection, received %v", err)
		}
	})
	for _, test := range []struct {
		name     string
		kind     EventKind
		expected string
	}{
		{name: "unknown event kind", kind: EventKind("unknown"), expected: "expected start, exec, stop, or churn"},
		{name: "gap in events table", kind: EventGap, expected: "gap kind is not valid in the events table"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.sqlite")
			writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatalf("open recording for invalid kind: %v", err)
			}
			_, updateErr := db.Exec(`UPDATE events SET kind = ?`, test.kind)
			closeErr := db.Close()
			if updateErr != nil || closeErr != nil {
				t.Fatalf("store invalid normalized kind: update=%v close=%v", updateErr, closeErr)
			}
			if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("expected normalized kind rejection containing %q, received %v", test.expected, err)
			}
		})
	}
	t.Run("versioned schema", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recording.sqlite")
		writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open recording for invalid normalized schema: %v", err)
		}
		_, updateErr := db.Exec(`UPDATE events SET schema_version = 2`)
		closeErr := db.Close()
		if updateErr != nil || closeErr != nil {
			t.Fatalf("store invalid normalized schema: update=%v close=%v", updateErr, closeErr)
		}
		if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "normalized event schema version") {
			t.Fatalf("expected normalized schema rejection, received %v", err)
		}
	})
	t.Run("empty backend", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recording.sqlite")
		writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open recording for empty normalized backend: %v", err)
		}
		_, updateErr := db.Exec(`UPDATE events SET backend = ''`)
		closeErr := db.Close()
		if updateErr != nil || closeErr != nil {
			t.Fatalf("store empty normalized backend: update=%v close=%v", updateErr, closeErr)
		}
		if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "normalized event backend") {
			t.Fatalf("expected normalized backend rejection, received %v", err)
		}
	})
}

func TestSQLiteRecordingRequiresCompleteRawEventObjects(t *testing.T) {
	validTime := time.Unix(1, 0).UTC().Format(time.RFC3339Nano)
	for _, test := range []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "malformed JSON", raw: `{`, expected: "decode versioned event"},
		{name: "whitespace", raw: " \n\t ", expected: "decode versioned event"},
		{name: "null", raw: `null`, expected: "expected an Event object"},
		{name: "empty object", raw: `{}`, expected: "expected schema version 1"},
		{name: "future schema", raw: `{"schema_version":2,"kind":"start","time":"` + validTime + `","backend":"poll"}`, expected: "expected schema version 1"},
		{name: "unknown kind", raw: `{"schema_version":1,"kind":"unknown","time":"` + validTime + `","backend":"poll"}`, expected: "expected a valid event kind"},
		{name: "zero time", raw: `{"schema_version":1,"kind":"start","time":"0001-01-01T00:00:00Z","backend":"poll"}`, expected: "expected a non-zero event time"},
		{name: "missing backend", raw: `{"schema_version":1,"kind":"start","time":"` + validTime + `"}`, expected: "expected a non-empty backend"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.sqlite")
			writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatalf("open recording for raw event validation: %v", err)
			}
			_, updateErr := db.Exec(`UPDATE events SET event_json = ?`, test.raw)
			closeErr := db.Close()
			if updateErr != nil || closeErr != nil {
				t.Fatalf("store invalid raw event: update=%v close=%v", updateErr, closeErr)
			}
			if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("expected raw event rejection containing %q, received %v", test.expected, err)
			}
		})
	}
}

func TestSQLiteRecordingValidatesNormalizedParentChains(t *testing.T) {
	t.Run("trimmed versioned JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recording.sqlite")
		writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open recording for versioned parent chain: %v", err)
		}
		_, updateErr := db.Exec(`UPDATE events SET parent_chain = ?`, " \n [{\"pid\":2,\"command\":\"parent\"}] \t")
		closeErr := db.Close()
		if updateErr != nil || closeErr != nil {
			t.Fatalf("store trimmed versioned parent chain: update=%v close=%v", updateErr, closeErr)
		}
		events, err := readSQLiteRecording(path)
		if err != nil {
			t.Fatalf("read trimmed versioned parent chain: %v", err)
		}
		if len(events) != 1 || len(events[0].ParentChain) != 1 || events[0].ParentChain[0].PID != 2 || events[0].ParentChain[0].Command != "parent" {
			t.Fatalf("expected decoded trimmed versioned parent chain, received %#v", events)
		}
	})
	t.Run("malformed versioned JSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recording.sqlite")
		writeReaderTestRecording(t, path, []Event{{Kind: EventStart, Time: time.Unix(1, 0), PID: 1, Backend: BackendPoll}})
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open recording for malformed versioned parent chain: %v", err)
		}
		_, updateErr := db.Exec(`UPDATE events SET parent_chain = '  not-json  '`)
		closeErr := db.Close()
		if updateErr != nil || closeErr != nil {
			t.Fatalf("store malformed versioned parent chain: update=%v close=%v", updateErr, closeErr)
		}
		if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "parent chain") || !strings.Contains(err.Error(), "as JSON") {
			t.Fatalf("expected malformed versioned parent chain rejection, received %v", err)
		}
	})
	t.Run("flattened legacy", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "recording.sqlite")
		writeReaderTestRecording(t, path, nil)
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatalf("open recording for flattened legacy parent chain: %v", err)
		}
		eventTime := time.Unix(1, 0).UTC()
		_, insertErr := db.Exec(`INSERT INTO events (time, time_key, kind, pid, backend, parent_chain, event_json) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
			eventTime.Format(time.RFC3339Nano), formatRecordingTimeKey(eventTime), EventStart, 1, BackendPoll, "2:parent <- 3")
		closeErr := db.Close()
		if insertErr != nil || closeErr != nil {
			t.Fatalf("store flattened legacy parent chain: insert=%v close=%v", insertErr, closeErr)
		}
		events, err := readSQLiteRecording(path)
		if err != nil {
			t.Fatalf("read flattened legacy parent chain: %v", err)
		}
		if len(events) != 1 || len(events[0].ParentChain) != 2 || events[0].ParentChain[0].PID != 2 || events[0].ParentChain[0].Command != "parent" || events[0].ParentChain[1].PID != 3 {
			t.Fatalf("expected decoded flattened legacy parent chain, received %#v", events)
		}
	})
	for _, value := range []string{"broken", "2:"} {
		t.Run("malformed flattened legacy "+value, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.sqlite")
			writeReaderTestRecording(t, path, nil)
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatalf("open recording for malformed flattened parent chain: %v", err)
			}
			eventTime := time.Unix(1, 0).UTC()
			_, insertErr := db.Exec(`INSERT INTO events (time, time_key, kind, pid, backend, parent_chain, event_json) VALUES (?, ?, ?, ?, ?, ?, NULL)`,
				eventTime.Format(time.RFC3339Nano), formatRecordingTimeKey(eventTime), EventStart, 1, BackendPoll, value)
			closeErr := db.Close()
			if insertErr != nil || closeErr != nil {
				t.Fatalf("store malformed flattened parent chain: insert=%v close=%v", insertErr, closeErr)
			}
			if _, err := readSQLiteRecording(path); err == nil || !strings.Contains(err.Error(), "flattened parent chain") {
				t.Fatalf("expected malformed flattened parent chain rejection for %q, received %v", value, err)
			}
		})
	}
}

func readRecordingWithLimit(path string, limit int) ([]Event, error) {
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	readErr := reader.Stream(Filter{EventMode: EventModeAll}, limit, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err := joinRecorderErrors(readErr, reader.Close()); err != nil {
		return nil, err
	}
	return events, nil
}
