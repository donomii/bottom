package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

type Recorder interface {
	Write(Event) error
	Close() error
}

type textRecorder struct {
	writer io.Writer
}

type jsonlRecorder struct {
	encoder *json.Encoder
	closer  io.Closer
	session *recordingSessionState
}

type csvRecorder struct {
	writer  *csv.Writer
	closer  io.Closer
	session *recordingSessionState
}

func newRecorder(config Config) (Recorder, error) {
	return newRecorderWithOptions(config, recorderOptionsFromConfig(config))
}

func newRecorderWithOptions(config Config, options recorderOptions) (Recorder, error) {
	options = normalizeRecorderOptions(options)
	if err := validateRecorderOptions(options); err != nil {
		return nil, err
	}
	session, err := newRecordingSession(config.Backend)
	if err != nil {
		return nil, err
	}
	session = redactRecordingSession(session, options.redact)
	prepareSink := func(target Recorder) Recorder {
		target = newSessionRecorder(target, session)
		if len(options.redact) > 0 {
			target = newRedactingRecorder(target, options.redact)
		}
		return newFilteringRecorder(target, config.Filter)
	}
	if config.TUI && config.OutputPath == "" {
		return prepareSink(NewTUIRecorder(os.Stdout)), nil
	}
	outputRecorder, err := newOutputRecorder(config.Format, config.OutputPath, session, options)
	if err != nil {
		return nil, err
	}
	outputTarget := prepareSink(outputRecorder)
	if config.RingBuffer > 0 {
		trigger, err := newEventTrigger(config.Trigger)
		if err != nil {
			return nil, err
		}
		outputTarget = newTriggeredRecorder(outputTarget, config.RingBuffer, config.PostTrigger, trigger)
	}
	if options.bufferSize > 0 {
		outputTarget = newBufferedRecorder(outputTarget, options.bufferSize, options.flushInterval)
	}
	if !config.TUI {
		return outputTarget, nil
	}
	return newMultiRecorder(prepareSink(NewTUIRecorder(os.Stdout)), outputTarget), nil
}

func newOutputRecorder(format OutputFormat, path string, session recordingSession, options recorderOptions) (Recorder, error) {
	factory := func(segmentPath string) (Recorder, error) {
		return newOutputRecorderSegment(format, segmentPath, session, options)
	}
	if path != "" && options.rotation.enabled() {
		if format == FormatSQLite {
			return nil, fmt.Errorf("output rotation supports text, jsonl, and csv, received format %q", format)
		}
		return newRotatingRecorder(path, options.rotation, factory)
	}
	return factory(path)
}

func newOutputRecorderSegment(format OutputFormat, path string, session recordingSession, options recorderOptions) (Recorder, error) {
	switch format {
	case FormatText:
		writer, closer, err := openOutput(path)
		if err != nil {
			return nil, err
		}
		if closer != nil {
			return closingTextRecorder{recorder: textRecorder{writer: writer}, closer: closer}, nil
		}
		return textRecorder{writer: writer}, nil
	case FormatJSONL:
		return newJSONLRecorder(path, session)
	case FormatCSV:
		return newCSVRecorderWithSession(path, session)
	case FormatSQLite:
		return newSQLiteRecorderWithOptions(path, session, options)
	default:
		return nil, fmt.Errorf("format must be text, jsonl, csv, or sqlite, received %q", format)
	}
}

func newJSONLRecorder(path string, session recordingSession) (Recorder, error) {
	writer, closer, err := openOutput(path)
	if err != nil {
		return nil, err
	}
	recorder := jsonlRecorder{encoder: json.NewEncoder(writer), closer: closer, session: newRecordingSessionState(session)}
	if err := recorder.writeSession("start", session); err != nil {
		return nil, joinRecorderErrors(err, closeRecorderAfterSetupFailure(closer, "jsonl", path))
	}
	return recorder, nil
}

func openOutput(path string) (io.Writer, io.Closer, error) {
	if path == "" {
		return os.Stdout, nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open output file %q for appending events: %w", path, err)
	}
	return file, file, nil
}

type closingTextRecorder struct {
	recorder textRecorder
	closer   io.Closer
}

func (recorder closingTextRecorder) Write(event Event) error {
	return recorder.recorder.Write(event)
}

func (recorder closingTextRecorder) Close() error {
	return recorder.closer.Close()
}

func (recorder textRecorder) Write(event Event) error {
	_, err := fmt.Fprintln(recorder.writer, formatTextEvent(event))
	if err != nil {
		return fmt.Errorf("write text event: %w", err)
	}
	return nil
}

func (recorder textRecorder) Close() error {
	return nil
}

func formatTextEvent(event Event) string {
	prefix := fmt.Sprintf("%s %-5s session=%s seq=%d backend=%s", event.Time.Format(time.RFC3339Nano), event.Kind, event.SessionID, event.Sequence, event.Backend)
	switch event.Kind {
	case EventStart, EventExec:
		return fmt.Sprintf("%s process=%s pid=%d ppid=%d uid=%s user=%s exe=%q cwd=%q unit=%q container=%q cmd=%q parent=%s", prefix, event.ProcessID, event.PID, event.ParentPID, event.UID, event.User, event.Exe, event.Cwd, event.SystemdUnit, event.ContainerID, event.Command, flattenParentChain(event.ParentChain))
	case EventStop:
		return fmt.Sprintf("%s process=%s pid=%d ppid=%d duration=%s exit=%s uid=%s user=%s exe=%q unit=%q container=%q cmd=%q", prefix, event.ProcessID, event.PID, event.ParentPID, time.Duration(event.DurationMillis)*time.Millisecond, formatExitCode(event.ExitCode), event.UID, event.User, event.Exe, event.SystemdUnit, event.ContainerID, event.Command)
	case EventChurn:
		return fmt.Sprintf("%s count=%d window=%s exe=%q unit=%q container=%q cmd=%q", prefix, event.Count, time.Duration(event.WindowMillis)*time.Millisecond, event.Exe, event.SystemdUnit, event.ContainerID, event.Command)
	case EventGap:
		return fmt.Sprintf("%s message=%q", prefix, event.Message)
	default:
		return fmt.Sprintf("%s message=%q", prefix, event.Message)
	}
}

func (recorder jsonlRecorder) Write(event Event) error {
	if recorder.session == nil {
		if err := recorder.encoder.Encode(event); err != nil {
			return fmt.Errorf("write jsonl event: %w", err)
		}
		return nil
	}
	record := jsonRecordingRecord{
		RecordType:             recordingRecordType(event),
		RecordingSchemaVersion: recorder.session.metadata.SchemaVersion,
		RecordingSessionID:     recorder.session.metadata.ID,
		Event:                  &event,
	}
	if err := recorder.encoder.Encode(record); err != nil {
		return fmt.Errorf("write jsonl %s record for recording session %q: %w", record.RecordType, record.RecordingSessionID, err)
	}
	return nil
}

func (recorder jsonlRecorder) writeSession(state string, session recordingSession) error {
	record := jsonRecordingRecord{
		RecordType:             recordTypeSession,
		RecordingSchemaVersion: session.SchemaVersion,
		RecordingSessionID:     session.ID,
		SessionState:           state,
		Session:                &session,
	}
	if err := recorder.encoder.Encode(record); err != nil {
		return fmt.Errorf("write jsonl session %s record for session %q: %w", state, session.ID, err)
	}
	return nil
}

func (recorder jsonlRecorder) Close() error {
	var recordErr error
	if recorder.session != nil {
		if session, ok := recorder.session.end(); ok {
			recordErr = recorder.writeSession("end", session)
		}
	}
	if recorder.closer == nil {
		return recordErr
	}
	closeErr := recorder.closer.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close jsonl output: %w", closeErr)
	}
	return joinRecorderErrors(recordErr, closeErr)
}

func newCSVRecorder(path string) (Recorder, error) {
	return newCSVRecorderWithSession(path, recordingSession{})
}

func newCSVRecorderWithSession(path string, session recordingSession) (Recorder, error) {
	writeHeader, err := inspectCSVOutput(path)
	if err != nil {
		return nil, err
	}
	writer, closer, err := openOutput(path)
	if err != nil {
		return nil, err
	}
	csvWriter := csv.NewWriter(writer)
	if writeHeader {
		if err := csvWriter.Write(csvHeader()); err != nil {
			return nil, joinRecorderErrors(
				fmt.Errorf("write csv header: %w", err),
				closeRecorderAfterSetupFailure(closer, "csv", path),
			)
		}
		csvWriter.Flush()
		if err := csvWriter.Error(); err != nil {
			return nil, joinRecorderErrors(
				fmt.Errorf("flush csv header: %w", err),
				closeRecorderAfterSetupFailure(closer, "csv", path),
			)
		}
	}
	recorder := csvRecorder{writer: csvWriter, closer: closer}
	if session.ID != "" {
		recorder.session = newRecordingSessionState(session)
		if err := recorder.writeSession("start", session); err != nil {
			return nil, joinRecorderErrors(err, closeRecorderAfterSetupFailure(closer, "csv", path))
		}
	}
	return recorder, nil
}

func closeRecorderAfterSetupFailure(closer io.Closer, format string, path string) error {
	if closer == nil {
		return nil
	}
	if err := closer.Close(); err != nil {
		return fmt.Errorf("close %s output %q after recorder setup failed: %w", format, path, err)
	}
	return nil
}

func inspectCSVOutput(path string) (bool, error) {
	if path == "" {
		return true, nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect csv output %q before appending records: %w", path, err)
	}
	if info.Size() == 0 {
		return true, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open csv output %q to verify its schema: %w", path, err)
	}
	actualHeader, readErr := csv.NewReader(file).Read()
	closeErr := file.Close()
	if readErr != nil {
		readErr = fmt.Errorf("read csv output header from %q: %w", path, readErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close csv output %q after verifying its schema: %w", path, closeErr)
	}
	if err := joinRecorderErrors(readErr, closeErr); err != nil {
		return false, err
	}
	expectedHeader := csvHeader()
	if !equalCSVRows(actualHeader, expectedHeader) {
		return false, fmt.Errorf("append csv output %q: expected header %q, received %q", path, expectedHeader, actualHeader)
	}
	return false, nil
}

func shouldWriteCSVHeader(path string) bool {
	writeHeader, err := inspectCSVOutput(path)
	return err == nil && writeHeader
}

func equalCSVRows(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func csvHeader() []string {
	return []string{
		"record_type", "recording_schema_version", "recording_session_id", "session_state", "session_started_at",
		"session_ended_at", "hostname", "recording_boot_id", "os", "arch", "session_backend", "schema_version",
		"session_id", "sequence", "host", "boot_id", "time", "observed_at", "kind", "process_id", "pid",
		"parent_pid", "user", "uid", "command", "exe", "cwd", "tty", "process_session", "cgroup", "systemd_unit",
		"container_id", "duration_ms", "exit_code", "backend", "count", "window_ms", "message", "parent_chain",
	}
}

func (recorder csvRecorder) Write(event Event) error {
	if err := recorder.writer.Write(csvEventWithSession(event, recorder.session)); err != nil {
		return fmt.Errorf("write csv %s event: %w", event.Kind, err)
	}
	recorder.writer.Flush()
	if err := recorder.writer.Error(); err != nil {
		return fmt.Errorf("flush csv %s event: %w", event.Kind, err)
	}
	return nil
}

func (recorder csvRecorder) writeSession(state string, session recordingSession) error {
	if err := recorder.writer.Write(csvSessionRecord(state, session)); err != nil {
		return fmt.Errorf("write csv session %s record for session %q: %w", state, session.ID, err)
	}
	recorder.writer.Flush()
	if err := recorder.writer.Error(); err != nil {
		return fmt.Errorf("flush csv session %s record for session %q: %w", state, session.ID, err)
	}
	return nil
}

func (recorder csvRecorder) Close() error {
	var recordErr error
	if recorder.session != nil {
		if session, ok := recorder.session.end(); ok {
			recordErr = recorder.writeSession("end", session)
		}
	}
	recorder.writer.Flush()
	flushErr := recorder.writer.Error()
	if flushErr != nil {
		flushErr = fmt.Errorf("flush csv output: %w", flushErr)
	}
	var closeErr error
	if recorder.closer != nil {
		closeErr = recorder.closer.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close csv output: %w", closeErr)
		}
	}
	return joinRecorderErrors(recordErr, flushErr, closeErr)
}

func csvEvent(event Event) []string {
	return csvEventWithSession(event, nil)
}

func csvEventWithSession(event Event, state *recordingSessionState) []string {
	recordType := recordingRecordType(event)
	recordingSchemaVersion := ""
	recordingSessionID := ""
	if state != nil {
		recordingSchemaVersion = strconv.Itoa(state.metadata.SchemaVersion)
		recordingSessionID = state.metadata.ID
	}
	return []string{
		recordType,
		recordingSchemaVersion,
		recordingSessionID,
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		strconv.Itoa(event.SchemaVersion),
		event.SessionID,
		strconv.FormatUint(event.Sequence, 10),
		event.Host,
		event.BootID,
		event.Time.UTC().Format(time.RFC3339Nano),
		formatOptionalTime(event.ObservedAt),
		string(event.Kind),
		event.ProcessID,
		strconv.Itoa(event.PID),
		strconv.Itoa(event.ParentPID),
		event.User,
		event.UID,
		event.Command,
		event.Exe,
		event.Cwd,
		event.TTY,
		event.Session,
		event.Cgroup,
		event.SystemdUnit,
		event.ContainerID,
		strconv.FormatInt(event.DurationMillis, 10),
		formatExitCode(event.ExitCode),
		event.Backend,
		strconv.Itoa(event.Count),
		strconv.FormatInt(event.WindowMillis, 10),
		event.Message,
		encodeParentChain(event.ParentChain),
	}
}

func csvSessionRecord(state string, session recordingSession) []string {
	endedAt := ""
	if session.EndedAt != nil {
		endedAt = session.EndedAt.Format(time.RFC3339Nano)
	}
	return []string{
		recordTypeSession,
		strconv.Itoa(session.SchemaVersion),
		session.ID,
		state,
		session.StartedAt.Format(time.RFC3339Nano),
		endedAt,
		session.Hostname,
		session.BootID,
		session.OS,
		session.Arch,
		session.Backend,
		"", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "", "",
	}
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func encodeParentChain(chain []ProcessSummary) string {
	encoded, err := json.Marshal(chain)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

func formatExitCode(exitCode *int) string {
	if exitCode == nil {
		return ""
	}
	return strconv.Itoa(*exitCode)
}

func flattenParentChain(chain []ProcessSummary) string {
	parts := []string{}
	for _, parent := range chain {
		if parent.Command == "" {
			parts = append(parts, strconv.Itoa(parent.PID))
		} else {
			parts = append(parts, fmt.Sprintf("%d:%s", parent.PID, sanitizeTerminalText(parent.Command)))
		}
	}
	return strings.Join(parts, " <- ")
}
