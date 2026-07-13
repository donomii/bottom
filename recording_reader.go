package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

type jsonRecordingReader struct {
	decoder *json.Decoder
	file    *os.File
	path    string
	record  int
}

type jsonRecordingEventCursor struct {
	reader  *jsonRecordingReader
	filter  Filter
	event   *Event
	matched int
	limit   int
}

func openJSONRecordingReader(path string) (*jsonRecordingReader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open JSONL process recording %q for reading: %w", path, err)
	}
	return &jsonRecordingReader{decoder: json.NewDecoder(file), file: file, path: path}, nil
}

func (reader *jsonRecordingReader) Close() error {
	if reader == nil || reader.file == nil {
		return nil
	}
	err := reader.file.Close()
	reader.file = nil
	if err != nil {
		return fmt.Errorf("close JSONL process recording %q: %w", reader.path, err)
	}
	return nil
}

func (reader *jsonRecordingReader) Stream(filter Filter, limit int, visit func(Event) error) error {
	cursor, err := reader.newEventCursor(filter, limit)
	if err != nil {
		return err
	}
	for cursor.current() != nil {
		if err := visit(*cursor.current()); err != nil {
			return err
		}
		if err := cursor.advance(); err != nil {
			return err
		}
	}
	return nil
}

func (reader *jsonRecordingReader) newEventCursor(filter Filter, limit int) (*jsonRecordingEventCursor, error) {
	if limit < 0 {
		return nil, fmt.Errorf("JSONL recording event limit must not be negative, received %d", limit)
	}
	cursor := &jsonRecordingEventCursor{reader: reader, filter: filter, limit: limit}
	if err := cursor.advance(); err != nil {
		return nil, err
	}
	return cursor, nil
}

func (cursor *jsonRecordingEventCursor) current() *Event {
	if cursor == nil {
		return nil
	}
	return cursor.event
}

func (cursor *jsonRecordingEventCursor) advance() error {
	cursor.event = nil
	if cursor.limit > 0 && cursor.matched >= cursor.limit {
		return nil
	}
	for {
		event, found, err := cursor.reader.readRecord()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if cursor.filter.Accepts(event) {
			cursor.event = &event
			cursor.matched++
			return nil
		}
	}
}

func (cursor *jsonRecordingEventCursor) Close() error {
	return nil
}

func (reader *jsonRecordingReader) readRecord() (Event, bool, error) {
	for {
		var raw json.RawMessage
		if err := reader.decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return Event{}, false, nil
			}
			return Event{}, false, fmt.Errorf("decode JSONL process recording %q record %d: %w", reader.path, reader.record+1, err)
		}
		reader.record++
		event, found, err := decodeJSONRecordingRecord(raw, reader.path, reader.record)
		if err != nil {
			return Event{}, false, err
		}
		if found {
			return event, true, nil
		}
	}
}

func decodeJSONRecordingRecord(raw json.RawMessage, path string, recordNumber int) (Event, bool, error) {
	var kind struct {
		RecordType string `json:"record_type"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return Event{}, false, fmt.Errorf("decode JSONL process recording %q record %d: %w", path, recordNumber, err)
	}
	if kind.RecordType == "" {
		var event Event
		if err := json.Unmarshal(raw, &event); err != nil {
			return Event{}, false, fmt.Errorf("decode JSONL event in %q record %d: %w", path, recordNumber, err)
		}
		if err := validateRecordedEvent(event, path, recordNumber); err != nil {
			return Event{}, false, err
		}
		return event, true, nil
	}
	var record jsonRecordingRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		return Event{}, false, fmt.Errorf("decode versioned JSONL record in %q record %d: %w", path, recordNumber, err)
	}
	if record.RecordingSchemaVersion != recordingSchemaVersion {
		return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: expected recording schema version %d, received %d", path, recordNumber, recordingSchemaVersion, record.RecordingSchemaVersion)
	}
	switch record.RecordType {
	case recordTypeSession:
		if record.Session == nil || (record.SessionState != "start" && record.SessionState != "end") {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: expected a session object with state start or end", path, recordNumber)
		}
		if record.RecordingSessionID == "" || record.Session.ID != record.RecordingSessionID {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: wrapper session %q differs from session object %q", path, recordNumber, record.RecordingSessionID, record.Session.ID)
		}
		if record.Session.SchemaVersion != recordingSchemaVersion {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: expected session schema version %d, received %d", path, recordNumber, recordingSchemaVersion, record.Session.SchemaVersion)
		}
		return Event{}, false, nil
	case recordTypeEvent, recordTypeGap:
		if record.Event == nil {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: expected an event object", path, recordNumber)
		}
		if record.RecordingSessionID == "" || record.RecordingSessionID != record.Event.SessionID {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: wrapper session %q differs from event session %q", path, recordNumber, record.RecordingSessionID, record.Event.SessionID)
		}
		if record.RecordType == recordTypeGap && record.Event.Kind != EventGap {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: gap record contains event kind %q", path, recordNumber, record.Event.Kind)
		}
		if record.RecordType == recordTypeEvent && record.Event.Kind == EventGap {
			return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: event record contains gap kind", path, recordNumber)
		}
		if err := validateRecordedEvent(*record.Event, path, recordNumber); err != nil {
			return Event{}, false, err
		}
		return *record.Event, true, nil
	default:
		return Event{}, false, fmt.Errorf("read JSONL process recording %q record %d: unknown record type %q", path, recordNumber, record.RecordType)
	}
}

func validateRecordedEvent(event Event, path string, recordNumber int) error {
	if event.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("read JSONL process recording %q record %d: expected event schema version %d, received %d", path, recordNumber, EventSchemaVersion, event.SchemaVersion)
	}
	if !validStoredEventKind(event.Kind) {
		return fmt.Errorf("read JSONL process recording %q record %d: expected a valid event kind, received %q", path, recordNumber, event.Kind)
	}
	if event.Time.IsZero() {
		return fmt.Errorf("read JSONL process recording %q record %d: expected a non-zero event time", path, recordNumber)
	}
	if event.Backend == "" {
		return fmt.Errorf("read JSONL process recording %q record %d: expected a non-empty backend", path, recordNumber)
	}
	return nil
}

func validStoredEventKind(kind EventKind) bool {
	switch kind {
	case EventStart, EventExec, EventStop, EventChurn, EventRestart, EventGap:
		return true
	default:
		return false
	}
}

func readJSONRecording(path string) ([]Event, error) {
	reader, err := openJSONRecordingReader(path)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	readErr := reader.Stream(Filter{EventMode: EventModeAll}, 0, func(event Event) error {
		events = append(events, event)
		return nil
	})
	return events, joinRecorderErrors(readErr, reader.Close())
}

func recordingPathsReferToSameFile(first string, second string) (bool, error) {
	canonicalFirst, err := canonicalTracePath(first)
	if err != nil {
		return false, fmt.Errorf("resolve recording path %q: %w", first, err)
	}
	canonicalSecond, err := canonicalTracePath(second)
	if err != nil {
		return false, fmt.Errorf("resolve recording path %q: %w", second, err)
	}
	if sameTracePath(canonicalFirst, canonicalSecond) {
		return true, nil
	}
	firstInfo, firstErr := os.Stat(canonicalFirst)
	secondInfo, secondErr := os.Stat(canonicalSecond)
	if firstErr != nil && !os.IsNotExist(firstErr) {
		return false, fmt.Errorf("inspect recording path %q: %w", first, firstErr)
	}
	if secondErr != nil && !os.IsNotExist(secondErr) {
		return false, fmt.Errorf("inspect recording path %q: %w", second, secondErr)
	}
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo), nil
}

func rejectRecordingOutputAlias(command string, outputPath string, inputPath string) error {
	if outputPath == "" {
		return nil
	}
	same, err := recordingPathsReferToSameFile(outputPath, inputPath)
	if err != nil {
		return fmt.Errorf("%s validate output path %q against input recording %q: %w", command, outputPath, inputPath, err)
	}
	if same {
		return fmt.Errorf("%s output path %q resolves to input recording %q", command, outputPath, inputPath)
	}
	return nil
}

func recordingEventLess(left Event, right Event) bool {
	if !left.Time.Equal(right.Time) {
		return left.Time.Before(right.Time)
	}
	return left.Sequence < right.Sequence
}

func recordingDuration(event Event) time.Duration {
	return time.Duration(event.DurationMillis) * time.Millisecond
}
