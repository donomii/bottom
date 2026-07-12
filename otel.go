package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	otelExportTimeout  = 5 * time.Second
	otelErrorBodyLimit = 4096
)

type otelRecorder struct {
	endpoint      string
	client        *http.Client
	events        chan Event
	batchSize     int
	flushInterval time.Duration
	done          chan struct{}
	mutex         sync.Mutex
	closed        bool
	failure       error
}

type otelExportRequest struct {
	ResourceLogs []otelResourceLogs `json:"resourceLogs"`
}

type otelResourceLogs struct {
	Resource  otelResource    `json:"resource"`
	ScopeLogs []otelScopeLogs `json:"scopeLogs"`
}

type otelResource struct {
	Attributes []otelKeyValue `json:"attributes"`
}

type otelScopeLogs struct {
	Scope      otelScope       `json:"scope"`
	LogRecords []otelLogRecord `json:"logRecords"`
}

type otelScope struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type otelLogRecord struct {
	TimeUnixNano         string         `json:"timeUnixNano"`
	ObservedTimeUnixNano string         `json:"observedTimeUnixNano"`
	SeverityNumber       int            `json:"severityNumber"`
	SeverityText         string         `json:"severityText"`
	Body                 otelValue      `json:"body"`
	Attributes           []otelKeyValue `json:"attributes"`
}

type otelKeyValue struct {
	Key   string    `json:"key"`
	Value otelValue `json:"value"`
}

type otelValue struct {
	StringValue string `json:"stringValue,omitempty"`
	IntValue    string `json:"intValue,omitempty"`
}

type otelExportResponse struct {
	PartialSuccess otelPartialSuccess `json:"partialSuccess"`
}

type otelPartialSuccess struct {
	RejectedLogRecords string `json:"rejectedLogRecords"`
	ErrorMessage       string `json:"errorMessage"`
}

func newOTelRecorder(endpoint string, session recordingSession, options recorderOptions) (Recorder, error) {
	normalized, err := normalizeOTelEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	recorder := &otelRecorder{
		endpoint:      normalized,
		client:        &http.Client{Timeout: otelExportTimeout, Transport: &http.Transport{Proxy: nil}},
		events:        make(chan Event, options.bufferSize),
		batchSize:     options.sqliteBatchSize,
		flushInterval: options.flushInterval,
		done:          make(chan struct{}),
	}
	go recorder.export(session)
	return recorder, nil
}

func normalizeOTelEndpoint(endpoint string) (string, error) {
	if endpoint == "" {
		return "", nil
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("parse local OpenTelemetry endpoint %q: %w", endpoint, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("local OpenTelemetry endpoint must use http or https, received scheme %q in %q", parsed.Scheme, endpoint)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("local OpenTelemetry endpoint must not contain credentials, a query, or a fragment, received %q", endpoint)
	}
	hostname := strings.ToLower(parsed.Hostname())
	ip := net.ParseIP(hostname)
	if hostname != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", fmt.Errorf("OpenTelemetry export is local-only; expected localhost or a loopback address, received host %q", parsed.Hostname())
	}
	if parsed.Port() == "" {
		return "", fmt.Errorf("local OpenTelemetry endpoint must include the collector port, received %q", endpoint)
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/v1/logs"
	} else if parsed.Path != "/v1/logs" {
		return "", fmt.Errorf("local OpenTelemetry endpoint path must be /v1/logs, received %q", parsed.Path)
	}
	return parsed.String(), nil
}

func (recorder *otelRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.failure != nil {
		return recorder.failure
	}
	if recorder.closed {
		return errRecorderClosed
	}
	select {
	case recorder.events <- event:
		return nil
	default:
		return fmt.Errorf("queue OpenTelemetry event kind=%s pid=%d: %w", event.Kind, event.PID, errRecorderBackpressure)
	}
}

func (recorder *otelRecorder) Close() error {
	recorder.mutex.Lock()
	if !recorder.closed {
		recorder.closed = true
		close(recorder.events)
	}
	recorder.mutex.Unlock()
	<-recorder.done
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.failure
}

func (recorder *otelRecorder) export(session recordingSession) {
	defer close(recorder.done)
	timer := time.NewTimer(recorder.flushInterval)
	defer timer.Stop()
	batch := make([]Event, 0, recorder.batchSize)
	for {
		select {
		case event, ok := <-recorder.events:
			if !ok {
				recorder.sendBatch(session, batch)
				return
			}
			batch = append(batch, event)
			if len(batch) >= recorder.batchSize {
				if !recorder.sendBatch(session, batch) {
					recorder.drain()
					return
				}
				batch = batch[:0]
				resetOTelTimer(timer, recorder.flushInterval)
			}
		case <-timer.C:
			if len(batch) > 0 {
				if !recorder.sendBatch(session, batch) {
					recorder.drain()
					return
				}
				batch = batch[:0]
			}
			timer.Reset(recorder.flushInterval)
		}
	}
}

func resetOTelTimer(timer *time.Timer, interval time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(interval)
}

func (recorder *otelRecorder) drain() {
	for range recorder.events {
	}
}

func (recorder *otelRecorder) sendBatch(session recordingSession, events []Event) bool {
	if len(events) == 0 {
		return true
	}
	payload := newOTelExportRequest(session, events)
	encoded, err := json.Marshal(payload)
	if err != nil {
		recorder.setFailure(fmt.Errorf("encode %d process events for OpenTelemetry export: %w", len(events), err))
		return false
	}
	request, err := http.NewRequest(http.MethodPost, recorder.endpoint, bytes.NewReader(encoded))
	if err != nil {
		recorder.setFailure(fmt.Errorf("create OpenTelemetry export request for %q: %w", recorder.endpoint, err))
		return false
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := recorder.client.Do(request)
	if err != nil {
		recorder.setFailure(fmt.Errorf("export %d process events to local OpenTelemetry endpoint %q: %w", len(events), recorder.endpoint, err))
		return false
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, otelErrorBodyLimit))
	closeErr := response.Body.Close()
	if readErr != nil {
		recorder.setFailure(fmt.Errorf("read OpenTelemetry response from %q with status %s: %w", recorder.endpoint, response.Status, readErr))
		return false
	}
	if closeErr != nil {
		recorder.setFailure(fmt.Errorf("close OpenTelemetry response from %q with status %s: %w", recorder.endpoint, response.Status, closeErr))
		return false
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		recorder.setFailure(fmt.Errorf("export %d process events to local OpenTelemetry endpoint %q: expected HTTP 2xx, received %s body=%q", len(events), recorder.endpoint, response.Status, strings.TrimSpace(string(body))))
		return false
	}
	if len(bytes.TrimSpace(body)) > 0 {
		var result otelExportResponse
		if err := json.Unmarshal(body, &result); err != nil {
			recorder.setFailure(fmt.Errorf("decode successful OpenTelemetry response from %q: expected OTLP/HTTP JSON or an empty body, received %q: %w", recorder.endpoint, strings.TrimSpace(string(body)), err))
			return false
		}
		rejected, err := strconv.ParseUint(result.PartialSuccess.RejectedLogRecords, 10, 64)
		if result.PartialSuccess.RejectedLogRecords != "" && err != nil {
			recorder.setFailure(fmt.Errorf("decode successful OpenTelemetry response from %q: expected rejectedLogRecords as a decimal integer string, received %q: %w", recorder.endpoint, result.PartialSuccess.RejectedLogRecords, err))
			return false
		}
		if rejected > 0 {
			recorder.setFailure(fmt.Errorf("export %d process events to local OpenTelemetry endpoint %q: collector rejected %d log records reason=%q", len(events), recorder.endpoint, rejected, result.PartialSuccess.ErrorMessage))
			return false
		}
	}
	return true
}

func (recorder *otelRecorder) setFailure(err error) {
	recorder.mutex.Lock()
	if recorder.failure == nil {
		recorder.failure = err
	}
	recorder.mutex.Unlock()
}

func newOTelExportRequest(session recordingSession, events []Event) otelExportRequest {
	resolvedVersion, _ := sourceBuildIdentity()
	logs := make([]otelLogRecord, 0, len(events))
	for _, event := range events {
		logs = append(logs, otelLogRecordForEvent(event))
	}
	resource := otelResource{Attributes: []otelKeyValue{
		otelStringAttribute("service.name", "bottom"),
		otelStringAttribute("service.version", resolvedVersion),
		otelStringAttribute("host.name", session.Hostname),
		otelStringAttribute("os.type", session.OS),
		otelStringAttribute("host.arch", session.Arch),
	}}
	return otelExportRequest{ResourceLogs: []otelResourceLogs{{
		Resource: resource,
		ScopeLogs: []otelScopeLogs{{
			Scope:      otelScope{Name: "github.com/donomii/bottom", Version: resolvedVersion},
			LogRecords: logs,
		}},
	}}}
}

func otelLogRecordForEvent(event Event) otelLogRecord {
	observedAt := event.ObservedAt
	if observedAt.IsZero() {
		observedAt = event.Time
	}
	attributes := []otelKeyValue{
		otelStringAttribute("bottom.event.kind", string(event.Kind)),
		otelStringAttribute("bottom.backend", event.Backend),
		otelIntAttribute("bottom.process.pid", int64(event.PID)),
		otelIntAttribute("bottom.process.parent_pid", int64(event.ParentPID)),
	}
	attributes = appendOTelStringAttribute(attributes, "bottom.session.id", event.SessionID)
	attributes = appendOTelStringAttribute(attributes, "bottom.process.id", event.ProcessID)
	attributes = appendOTelStringAttribute(attributes, "process.command_line", event.Command)
	attributes = appendOTelStringAttribute(attributes, "process.executable.path", event.Exe)
	attributes = appendOTelStringAttribute(attributes, "process.working_directory", event.Cwd)
	attributes = appendOTelStringAttribute(attributes, "user.name", event.User)
	attributes = appendOTelStringAttribute(attributes, "user.id", event.UID)
	attributes = appendOTelStringAttribute(attributes, "bottom.systemd.unit", event.SystemdUnit)
	attributes = appendOTelStringAttribute(attributes, "container.id", event.ContainerID)
	attributes = appendOTelStringAttribute(attributes, "bottom.message", event.Message)
	if event.DurationMillis != 0 {
		attributes = append(attributes, otelIntAttribute("bottom.process.duration_ms", event.DurationMillis))
	}
	if event.ExitCode != nil {
		attributes = append(attributes, otelIntAttribute("process.exit.code", int64(*event.ExitCode)))
	}
	if event.Count != 0 {
		attributes = append(attributes, otelIntAttribute("bottom.event.count", int64(event.Count)))
	}
	return otelLogRecord{
		TimeUnixNano:         strconv.FormatInt(event.Time.UnixNano(), 10),
		ObservedTimeUnixNano: strconv.FormatInt(observedAt.UnixNano(), 10),
		SeverityNumber:       otelSeverityNumber(event),
		SeverityText:         otelSeverityText(event),
		Body:                 otelValue{StringValue: formatTextEvent(event)},
		Attributes:           attributes,
	}
}

func otelSeverityNumber(event Event) int {
	if event.Kind == EventGap || event.Kind == EventStop && event.ExitCode != nil && *event.ExitCode != 0 {
		return 13
	}
	return 9
}

func otelSeverityText(event Event) string {
	if otelSeverityNumber(event) >= 13 {
		return "WARN"
	}
	return "INFO"
}

func otelStringAttribute(key string, value string) otelKeyValue {
	return otelKeyValue{Key: key, Value: otelValue{StringValue: value}}
}

func otelIntAttribute(key string, value int64) otelKeyValue {
	return otelKeyValue{Key: key, Value: otelValue{IntValue: strconv.FormatInt(value, 10)}}
}

func appendOTelStringAttribute(attributes []otelKeyValue, key string, value string) []otelKeyValue {
	if value == "" {
		return attributes
	}
	return append(attributes, otelStringAttribute(key, value))
}
