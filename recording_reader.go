package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type sqliteRecordingReader struct {
	db   *sql.DB
	path string
}

type sqliteRecordingRow struct {
	source       int
	encodedEvent string
	event        Event
	eventTime    string
	timeKey      string
	observedAt   string
	kind         string
	parentChain  string
	exitCode     sql.NullInt64
}

type sqliteRecordingCursor struct {
	path          string
	source        recordingQuerySource
	filter        Filter
	rows          *sql.Rows
	event         Event
	headTime      time.Time
	headSequence  uint64
	validationErr error
	ready         bool
}

type sqliteRecordingEventCursor struct {
	cursors []*sqliteRecordingCursor
}

func openSQLiteRecordingReader(path string) (*sqliteRecordingReader, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open process recording %q for reading: %w", path, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("open process recording %q for reading: expected a SQLite file, received a directory", path)
	}
	dsn, err := readOnlySQLiteDSN(path)
	if err != nil {
		return nil, fmt.Errorf("resolve process recording %q for read-only access: %w", path, err)
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open process recording database %q read-only: %w", path, err)
	}
	db.SetMaxOpenConns(len(recordingQuerySources))
	if _, err := db.Exec(`PRAGMA query_only = ON`); err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(
			fmt.Errorf("enable read-only queries for process recording %q: %w", path, err),
			formatSQLiteRecordingCloseError(path, closeErr),
		)
	}
	version, err := readSQLiteRecordingVersion(db, path)
	if err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(err, formatSQLiteRecordingCloseError(path, closeErr))
	}
	if version < recordingSchemaVersion {
		closeErr := db.Close()
		return nil, joinRecorderErrors(
			fmt.Errorf("read process recording %q: expected schema version %d, received %d; open it once with a current recorder to migrate it", path, recordingSchemaVersion, version),
			formatSQLiteRecordingCloseError(path, closeErr),
		)
	}
	if version > recordingSchemaVersion {
		closeErr := db.Close()
		return nil, joinRecorderErrors(
			fmt.Errorf("read process recording %q: expected schema version at most %d, received %d", path, recordingSchemaVersion, version),
			formatSQLiteRecordingCloseError(path, closeErr),
		)
	}
	return &sqliteRecordingReader{db: db, path: path}, nil
}

func readOnlySQLiteDSN(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	slashPath := filepath.ToSlash(absolute)
	if !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	location := url.URL{Scheme: "file", Path: slashPath}
	query := location.Query()
	query.Set("mode", "ro")
	location.RawQuery = query.Encode()
	return location.String(), nil
}

func (reader *sqliteRecordingReader) Close() error {
	if reader == nil || reader.db == nil {
		return nil
	}
	err := reader.db.Close()
	reader.db = nil
	return formatSQLiteRecordingCloseError(reader.path, err)
}

func formatSQLiteRecordingCloseError(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close process recording database %q: %w", path, err)
}

func (reader *sqliteRecordingReader) Stream(filter Filter, limit int, visit func(Event) error) error {
	eventCursor, err := reader.newEventCursor(filter, limit)
	if err != nil {
		return err
	}
	matched := 0
	for {
		selected := eventCursor.current()
		if selected == nil {
			break
		}
		if selected.validationErr != nil {
			return joinRecorderErrors(selected.validationErr, eventCursor.Close())
		}
		if err := visit(selected.event); err != nil {
			return joinRecorderErrors(err, eventCursor.Close())
		}
		matched++
		if limit > 0 && matched >= limit {
			break
		}
		if err := eventCursor.advance(); err != nil {
			return joinRecorderErrors(err, eventCursor.Close())
		}
	}
	return eventCursor.Close()
}

func (reader *sqliteRecordingReader) newEventCursor(filter Filter, limit int) (*sqliteRecordingEventCursor, error) {
	cursors := []*sqliteRecordingCursor{}
	for _, source := range recordingQuerySources {
		if !source.accepts(filter) {
			continue
		}
		rows, err := reader.querySourceRows(source, filter, limit, false)
		if err != nil {
			return nil, joinRecorderErrors(
				fmt.Errorf("query ordered %s from process recording %q: %w", source.name(), reader.path, err),
				closeSQLiteRecordingCursors(cursors),
			)
		}
		cursor := &sqliteRecordingCursor{path: reader.path, source: source, filter: filter, rows: rows}
		if err := cursor.advance(); err != nil {
			return nil, joinRecorderErrors(err, cursor.Close(), closeSQLiteRecordingCursors(cursors))
		}
		cursors = append(cursors, cursor)
	}
	return &sqliteRecordingEventCursor{cursors: cursors}, nil
}

func (cursor *sqliteRecordingEventCursor) current() *sqliteRecordingCursor {
	if cursor == nil {
		return nil
	}
	return nextSQLiteRecordingCursor(cursor.cursors)
}

func (cursor *sqliteRecordingEventCursor) advance() error {
	selected := cursor.current()
	if selected == nil {
		return nil
	}
	return selected.advance()
}

func (cursor *sqliteRecordingEventCursor) Close() error {
	if cursor == nil {
		return nil
	}
	err := closeSQLiteRecordingCursors(cursor.cursors)
	cursor.cursors = nil
	return err
}

func (cursor *sqliteRecordingCursor) advance() error {
	cursor.ready = false
	cursor.validationErr = nil
	for cursor.rows.Next() {
		row, err := scanSQLiteRecordingRow(cursor.rows)
		if err != nil {
			return fmt.Errorf("scan ordered %s from process recording %q: %w", cursor.source.name(), cursor.path, err)
		}
		eventTime, err := row.decodeOrderingTime(cursor.path)
		if err != nil {
			return err
		}
		cursor.headTime = eventTime
		cursor.headSequence = row.event.Sequence
		event, err := row.decode(cursor.path, eventTime)
		if err != nil {
			cursor.validationErr = err
			cursor.ready = true
			return nil
		}
		if !cursor.filter.Accepts(event) {
			continue
		}
		cursor.event = event
		cursor.ready = true
		return nil
	}
	if err := cursor.rows.Err(); err != nil {
		return fmt.Errorf("iterate ordered %s from process recording %q: %w", cursor.source.name(), cursor.path, err)
	}
	return cursor.Close()
}

func (cursor *sqliteRecordingCursor) Close() error {
	if cursor == nil || cursor.rows == nil {
		return nil
	}
	err := cursor.rows.Close()
	cursor.rows = nil
	if err != nil {
		return fmt.Errorf("close ordered %s rows from process recording %q: %w", cursor.source.name(), cursor.path, err)
	}
	return nil
}

func closeSQLiteRecordingCursors(cursors []*sqliteRecordingCursor) error {
	errs := []error{}
	for _, cursor := range cursors {
		if err := cursor.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return joinRecorderErrors(errs...)
}

func nextSQLiteRecordingCursor(cursors []*sqliteRecordingCursor) *sqliteRecordingCursor {
	var selected *sqliteRecordingCursor
	for _, cursor := range cursors {
		if !cursor.ready {
			continue
		}
		if selected == nil || recordingCursorLess(cursor, selected) {
			selected = cursor
		}
	}
	return selected
}

func recordingCursorLess(left *sqliteRecordingCursor, right *sqliteRecordingCursor) bool {
	if !left.headTime.Equal(right.headTime) {
		return left.headTime.Before(right.headTime)
	}
	if left.headSequence != right.headSequence {
		return left.headSequence < right.headSequence
	}
	return left.source.rank < right.source.rank
}

func scanSQLiteRecordingRow(rows *sql.Rows) (sqliteRecordingRow, error) {
	var row sqliteRecordingRow
	err := rows.Scan(
		&row.source, &row.encodedEvent, &row.event.SessionID, &row.event.SchemaVersion, &row.event.Sequence,
		&row.eventTime, &row.timeKey, &row.observedAt, &row.kind, &row.event.Host, &row.event.BootID, &row.event.ProcessID,
		&row.event.PID, &row.event.ParentPID, &row.event.User, &row.event.UID, &row.event.Command, &row.event.Exe,
		&row.event.Cwd, &row.event.TTY, &row.event.Session, &row.event.Cgroup, &row.event.SystemdUnit,
		&row.event.ContainerID, &row.event.DurationMillis, &row.exitCode, &row.event.Backend, &row.event.Count,
		&row.event.WindowMillis, &row.event.Message, &row.parentChain,
	)
	return row, err
}

func (row sqliteRecordingRow) decodeOrderingTime(path string) (time.Time, error) {
	eventTime, err := time.Parse(time.RFC3339Nano, row.eventTime)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse normalized %s time %q from process recording %q: %w", row.recordType(), row.eventTime, path, err)
	}
	if eventTime.IsZero() {
		return time.Time{}, fmt.Errorf("validate normalized %s time %q from process recording %q: expected a non-zero time", row.recordType(), row.eventTime, path)
	}
	expectedTimeKey := formatRecordingTimeKey(eventTime)
	if row.timeKey != expectedTimeKey {
		return time.Time{}, fmt.Errorf("validate normalized %s time in process recording %q: time %q requires key %q, received %q", row.recordType(), path, row.eventTime, expectedTimeKey, row.timeKey)
	}
	return eventTime, nil
}

func (row sqliteRecordingRow) decode(path string, eventTime time.Time) (Event, error) {
	versioned := row.encodedEvent != ""
	if versioned {
		if err := validateVersionedEventJSON(row.encodedEvent, path); err != nil {
			return Event{}, err
		}
		if row.event.SchemaVersion != EventSchemaVersion {
			return Event{}, fmt.Errorf("validate normalized %s schema version from process recording %q: expected %d, received %d", row.recordType(), path, EventSchemaVersion, row.event.SchemaVersion)
		}
	}
	if strings.TrimSpace(row.event.Backend) == "" {
		return Event{}, fmt.Errorf("validate normalized %s backend from process recording %q: expected a non-empty backend", row.recordType(), path)
	}
	row.event.Kind = EventKind(row.kind)
	if err := row.validateNormalizedKind(path); err != nil {
		return Event{}, err
	}
	row.event.Time = eventTime
	if row.observedAt != "" {
		observedAt, err := time.Parse(time.RFC3339Nano, row.observedAt)
		if err != nil {
			return Event{}, fmt.Errorf("parse normalized %s observed time %q from process recording %q: %w", row.recordType(), row.observedAt, path, err)
		}
		row.event.ObservedAt = observedAt
	}
	if row.exitCode.Valid {
		value := int(row.exitCode.Int64)
		row.event.ExitCode = &value
	}
	parentChain, err := row.decodeParentChain(path, versioned)
	if err != nil {
		return Event{}, err
	}
	row.event.ParentChain = parentChain
	return row.event, nil
}

func validateVersionedEventJSON(value string, path string) error {
	var event *Event
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &event); err != nil {
		return fmt.Errorf("decode versioned event from process recording %q: %w", path, err)
	}
	if event == nil {
		return fmt.Errorf("validate versioned event from process recording %q: expected an Event object, received null", path)
	}
	if event.SchemaVersion != EventSchemaVersion {
		return fmt.Errorf("validate versioned event from process recording %q: expected schema version %d, received %d", path, EventSchemaVersion, event.SchemaVersion)
	}
	if !validStoredEventKind(event.Kind) {
		return fmt.Errorf("validate versioned event from process recording %q: expected a valid event kind, received %q", path, event.Kind)
	}
	if event.Time.IsZero() {
		return fmt.Errorf("validate versioned event from process recording %q: expected a non-zero event time", path)
	}
	if strings.TrimSpace(event.Backend) == "" {
		return fmt.Errorf("validate versioned event from process recording %q: expected a non-empty backend", path)
	}
	return nil
}

func validStoredEventKind(kind EventKind) bool {
	switch kind {
	case EventStart, EventExec, EventStop, EventChurn, EventGap:
		return true
	default:
		return false
	}
}

func (row sqliteRecordingRow) validateNormalizedKind(path string) error {
	kind := EventKind(row.kind)
	if row.source == 1 {
		if kind != EventGap {
			return fmt.Errorf("validate normalized gap kind from process recording %q: expected %q, received %q", path, EventGap, kind)
		}
		return nil
	}
	if kind == EventGap {
		return fmt.Errorf("validate normalized event kind from process recording %q: gap kind is not valid in the events table", path)
	}
	if !validStoredEventKind(kind) {
		return fmt.Errorf("validate normalized event kind from process recording %q: expected start, exec, stop, or churn, received %q", path, kind)
	}
	return nil
}

func (row sqliteRecordingRow) decodeParentChain(path string, versioned bool) ([]ProcessSummary, error) {
	value := strings.TrimSpace(row.parentChain)
	if value == "" {
		return nil, nil
	}
	if versioned || strings.HasPrefix(value, "[") || value == "null" {
		var chain []ProcessSummary
		if err := json.Unmarshal([]byte(value), &chain); err != nil {
			return nil, fmt.Errorf("decode normalized %s parent chain from process recording %q as JSON: %w", row.recordType(), path, err)
		}
		return chain, nil
	}
	return decodeFlattenedParentChain(value, path)
}

func decodeFlattenedParentChain(value string, path string) ([]ProcessSummary, error) {
	segments := strings.Split(value, " <- ")
	chain := make([]ProcessSummary, 0, len(segments))
	for _, encodedSegment := range segments {
		segment := strings.TrimSpace(encodedSegment)
		pidText, command, hasCommand := strings.Cut(segment, ":")
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid <= 0 {
			return nil, fmt.Errorf("decode flattened parent chain from process recording %q: expected a positive PID in segment %q from %q", path, segment, value)
		}
		if hasCommand && command == "" {
			return nil, fmt.Errorf("decode flattened parent chain from process recording %q: expected a command after PID %d in segment %q from %q", path, pid, segment, value)
		}
		chain = append(chain, ProcessSummary{PID: pid, Command: command})
	}
	return chain, nil
}

func (row sqliteRecordingRow) recordType() string {
	if row.source == 1 {
		return "gap"
	}
	return "event"
}

func readSQLiteRecording(path string) ([]Event, error) {
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		return nil, err
	}
	events := []Event{}
	readErr := reader.Stream(Filter{EventMode: EventModeAll}, 0, func(event Event) error {
		events = append(events, event)
		return nil
	})
	closeErr := reader.Close()
	if err := joinRecorderErrors(readErr, closeErr); err != nil {
		return nil, err
	}
	return events, nil
}

func readSQLiteRecordingVersion(db *sql.DB, path string) (int, error) {
	var version int
	if err := db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read process recording schema version from %q: %w", path, err)
	}
	return version, nil
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
