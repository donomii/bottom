package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNormalizeOTelEndpointRequiresExplicitLocalCollector(t *testing.T) {
	cases := []struct {
		input    string
		expected string
		valid    bool
	}{
		{input: "", expected: "", valid: true},
		{input: "http://127.0.0.1:4318", expected: "http://127.0.0.1:4318/v1/logs", valid: true},
		{input: "http://[::1]:4318/v1/logs", expected: "http://[::1]:4318/v1/logs", valid: true},
		{input: "https://localhost:4318/v1/logs", expected: "https://localhost:4318/v1/logs", valid: true},
		{input: "http://collector.example:4318", valid: false},
		{input: "http://127.0.0.1", valid: false},
		{input: "http://127.0.0.1:4318/v1/traces", valid: false},
		{input: "file:///tmp/events", valid: false},
	}
	for _, test := range cases {
		received, err := normalizeOTelEndpoint(test.input)
		if test.valid && (err != nil || received != test.expected) {
			t.Fatalf("expected endpoint %q to normalize to %q, received %q error=%v", test.input, test.expected, received, err)
		}
		if !test.valid && err == nil {
			t.Fatalf("expected endpoint %q to be rejected", test.input)
		}
	}
}

func TestOTelRequestUsesLogSignalAndStringEncodedIntegers(t *testing.T) {
	exitCode := 7
	event := Event{
		Kind: EventStop, Time: time.Unix(10, 25), ObservedAt: time.Unix(11, 26), SessionID: "session-1",
		ProcessID: "42:1", PID: 42, ParentPID: 7, Command: "worker --once", Exe: "/bin/worker",
		DurationMillis: 250, ExitCode: &exitCode, Backend: BackendPoll,
	}
	payload := newOTelExportRequest(recordingSession{Hostname: "host", OS: "linux", Arch: "amd64"}, []Event{event})
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("encode OTLP request: %v", err)
	}
	text := string(encoded)
	for _, expected := range []string{`"resourceLogs"`, `"scopeLogs"`, `"logRecords"`, `"timeUnixNano":"10000000025"`, `"process.exit.code"`, `"intValue":"7"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("expected OTLP request %s to contain %s", text, expected)
		}
	}
}

func TestParseConfigKeepsOpenTelemetryDisabledByDefault(t *testing.T) {
	config, err := parseConfig(nil)
	if err != nil {
		t.Fatalf("parse default config: %v", err)
	}
	if config.OTelEndpoint != "" {
		t.Fatalf("expected no default OpenTelemetry endpoint, received %q", config.OTelEndpoint)
	}
	config, err = parseConfig([]string{"-otel-endpoint", "http://localhost:4318"})
	if err != nil || config.OTelEndpoint != "http://localhost:4318" {
		t.Fatalf("expected explicit local OpenTelemetry endpoint, received %#v error=%v", config, err)
	}
}
