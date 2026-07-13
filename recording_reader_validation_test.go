package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLRecordingRejectsInvalidEvents(t *testing.T) {
	validTime := time.Unix(1, 0).UTC().Format(time.RFC3339Nano)
	for _, test := range []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "malformed JSON", raw: `{`, expected: "decode JSONL"},
		{name: "empty object", raw: `{}`, expected: "expected event schema version 1"},
		{name: "future schema", raw: `{"schema_version":2,"kind":"start","time":"` + validTime + `","backend":"poll"}`, expected: "expected event schema version 1"},
		{name: "unknown kind", raw: `{"schema_version":1,"kind":"unknown","time":"` + validTime + `","backend":"poll"}`, expected: "expected a valid event kind"},
		{name: "zero time", raw: `{"schema_version":1,"kind":"start","time":"0001-01-01T00:00:00Z","backend":"poll"}`, expected: "expected a non-zero event time"},
		{name: "missing backend", raw: `{"schema_version":1,"kind":"start","time":"` + validTime + `"}`, expected: "expected a non-empty backend"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "recording.jsonl")
			if err := os.WriteFile(path, []byte(test.raw+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readJSONRecording(path); err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("expected error containing %q, received %v", test.expected, err)
			}
		})
	}
}

func TestJSONLRecordingValidatesVersionedEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.jsonl")
	raw := `{"record_type":"event","recording_schema_version":4,"recording_session_id":"outer","schema_version":1,"session_id":"inner","kind":"start","time":"2026-07-09T12:00:00Z","backend":"poll"}`
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readJSONRecording(path); err == nil || !strings.Contains(err.Error(), "wrapper session") {
		t.Fatalf("expected mismatched session rejection, received %v", err)
	}
}

func TestJSONLRecordingRejectsUnknownRecordType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recording.jsonl")
	raw := `{"record_type":"surprise","recording_schema_version":4}`
	if err := os.WriteFile(path, []byte(raw+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readJSONRecording(path); err == nil || !strings.Contains(err.Error(), "unknown record type") {
		t.Fatalf("expected unknown record type rejection, received %v", err)
	}
}
