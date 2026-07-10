package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteRecorder struct {
	db             *sql.DB
	path           string
	session        recordingSession
	batchSize      int
	mutex          sync.Mutex
	transaction    *sql.Tx
	eventStatement *sql.Stmt
	gapStatement   *sql.Stmt
	pending        int
	closed         bool
}

const sqliteInsertEvent = `INSERT INTO events (
	recording_session_id, session_id, schema_version, sequence, time, time_key, observed_at, kind, host, boot_id, process_id,
	pid, parent_pid, user, uid, command, exe, cwd, tty, process_session, cgroup, systemd_unit, container_id,
	duration_ms, exit_code, backend, count, window_ms, message, parent_chain, event_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

const sqliteInsertGap = `INSERT INTO gaps (
	recording_session_id, session_id, schema_version, sequence, time, time_key, observed_at, host, boot_id, backend, count, message, event_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func newSQLiteRecorder(path string) (Recorder, error) {
	session, err := newRecordingSession(BackendAuto)
	if err != nil {
		return nil, err
	}
	return newSQLiteRecorderWithOptions(path, session, defaultRecorderOptions())
}

func newSQLiteRecorderWithOptions(path string, session recordingSession, options recorderOptions) (*sqliteRecorder, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite output path must be non-empty")
	}
	options = normalizeRecorderOptions(options)
	if err := validateRecorderOptions(options); err != nil {
		return nil, err
	}
	if err := prepareSQLiteOutput(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	recorder := &sqliteRecorder{db: db, path: path, session: session, batchSize: options.sqliteBatchSize}
	if err := recorder.initialize(options.retention); err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(err, closeErr)
	}
	return recorder, nil
}

func prepareSQLiteOutput(path string) error {
	if path == ":memory:" {
		return nil
	}
	if strings.HasPrefix(path, "file:") {
		return fmt.Errorf("sqlite URI %q cannot guarantee owner-only creation; expected a filesystem path or :memory:", path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if os.IsExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create owner-only sqlite output %q: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close newly created sqlite output %q: %w", path, err)
	}
	return nil
}

func (recorder *sqliteRecorder) initialize(retention time.Duration) error {
	if _, err := recorder.db.Exec(`PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys for %q: %w", recorder.path, err)
	}
	if err := recorder.createSchema(); err != nil {
		return err
	}
	if retention > 0 {
		if err := recorder.applyRetention(retention); err != nil {
			return err
		}
	}
	_, err := recorder.db.Exec(`INSERT INTO sessions (
		id, schema_version, started_at, hostname, boot_id, os, arch, backend
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, recorder.session.ID, recorder.session.SchemaVersion,
		recorder.session.StartedAt.Format(time.RFC3339Nano), recorder.session.Hostname, recorder.session.BootID,
		recorder.session.OS, recorder.session.Arch, recorder.session.Backend)
	if err != nil {
		return fmt.Errorf("insert sqlite recording session %q into %q: %w", recorder.session.ID, recorder.path, err)
	}
	return nil
}

func (recorder *sqliteRecorder) applyRetention(retention time.Duration) error {
	cutoff := formatRecordingTimeKey(time.Now().UTC().Add(-retention))
	transaction, err := recorder.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite retention cleanup in %q for sessions older than %s: %w", recorder.path, retention, err)
	}
	statements := []string{
		`DELETE FROM gaps WHERE time_key < ?`,
		`DELETE FROM events WHERE time_key < ?`,
		`DELETE FROM sessions
		 WHERE julianday(COALESCE(ended_at, started_at)) < julianday(?)
		   AND NOT EXISTS (SELECT 1 FROM events WHERE events.recording_session_id = sessions.id)
		   AND NOT EXISTS (SELECT 1 FROM gaps WHERE gaps.recording_session_id = sessions.id)`,
	}
	for index, statement := range statements {
		if _, err := transaction.Exec(statement, cutoff); err != nil {
			rollbackErr := transaction.Rollback()
			return joinRecorderErrors(
				fmt.Errorf("apply sqlite retention statement %d in %q with cutoff %s: %w", index+1, recorder.path, cutoff, err),
				formatSQLiteRollbackError(recorder.path, rollbackErr),
			)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite retention cleanup in %q with cutoff %s: %w", recorder.path, cutoff, err)
	}
	return nil
}

func (recorder *sqliteRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("record sqlite event kind=%s pid=%d in %q: %w", event.Kind, event.PID, recorder.path, errRecorderClosed)
	}
	if event.Time.IsZero() {
		return fmt.Errorf("record sqlite event kind=%s pid=%d in %q: expected a non-zero event time", event.Kind, event.PID, recorder.path)
	}
	if event.SchemaVersion == 0 {
		event.SchemaVersion = EventSchemaVersion
	}
	if event.SessionID == "" {
		event.SessionID = recorder.session.ID
	}
	if err := recorder.beginBatch(); err != nil {
		return err
	}
	var writeErr error
	if event.Kind == EventGap {
		writeErr = recorder.writeGap(event)
	} else {
		writeErr = recorder.writeEvent(event)
	}
	if writeErr != nil {
		rollbackErr := recorder.rollbackBatch()
		return joinRecorderErrors(writeErr, rollbackErr)
	}
	recorder.pending++
	if recorder.pending >= recorder.batchSize {
		return recorder.commitBatch()
	}
	return nil
}

func (recorder *sqliteRecorder) beginBatch() error {
	if recorder.transaction != nil {
		return nil
	}
	transaction, err := recorder.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite event batch in %q: %w", recorder.path, err)
	}
	eventStatement, err := transaction.Prepare(sqliteInsertEvent)
	if err != nil {
		rollbackErr := transaction.Rollback()
		return joinRecorderErrors(
			fmt.Errorf("prepare sqlite event insert in %q: %w", recorder.path, err),
			formatSQLiteRollbackError(recorder.path, rollbackErr),
		)
	}
	gapStatement, err := transaction.Prepare(sqliteInsertGap)
	if err != nil {
		statementErr := eventStatement.Close()
		rollbackErr := transaction.Rollback()
		return joinRecorderErrors(
			fmt.Errorf("prepare sqlite gap insert in %q: %w", recorder.path, err),
			formatSQLiteStatementCloseError(recorder.path, statementErr),
			formatSQLiteRollbackError(recorder.path, rollbackErr),
		)
	}
	recorder.transaction = transaction
	recorder.eventStatement = eventStatement
	recorder.gapStatement = gapStatement
	return nil
}

func (recorder *sqliteRecorder) writeEvent(event Event) error {
	exitCode := sql.NullInt64{}
	if event.ExitCode != nil {
		exitCode.Valid = true
		exitCode.Int64 = int64(*event.ExitCode)
	}
	parentChain, err := json.Marshal(event.ParentChain)
	if err != nil {
		return fmt.Errorf("encode parent chain for sqlite event kind=%s pid=%d in %q: %w", event.Kind, event.PID, recorder.path, err)
	}
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode sqlite event kind=%s pid=%d in %q: %w", event.Kind, event.PID, recorder.path, err)
	}
	_, err = recorder.eventStatement.Exec(
		recorder.session.ID, event.SessionID, event.SchemaVersion, event.Sequence, event.Time.UTC().Format(time.RFC3339Nano),
		formatRecordingTimeKey(event.Time), nullableRecordingTime(event.ObservedAt), string(event.Kind), event.Host, event.BootID, event.ProcessID,
		event.PID, event.ParentPID, event.User, event.UID, event.Command, event.Exe, event.Cwd, event.TTY, event.Session,
		event.Cgroup, event.SystemdUnit, event.ContainerID, event.DurationMillis, exitCode, event.Backend, event.Count,
		event.WindowMillis, event.Message, string(parentChain), string(eventJSON),
	)
	if err != nil {
		return fmt.Errorf("insert sqlite event kind=%s pid=%d session=%q sequence=%d command=%q in %q: %w",
			event.Kind, event.PID, event.SessionID, event.Sequence, event.Command, recorder.path, err)
	}
	return nil
}

func (recorder *sqliteRecorder) writeGap(event Event) error {
	eventJSON, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode sqlite gap session=%q sequence=%d in %q: %w", event.SessionID, event.Sequence, recorder.path, err)
	}
	_, err = recorder.gapStatement.Exec(
		recorder.session.ID, event.SessionID, event.SchemaVersion, event.Sequence, event.Time.UTC().Format(time.RFC3339Nano),
		formatRecordingTimeKey(event.Time), nullableRecordingTime(event.ObservedAt), event.Host, event.BootID, event.Backend, event.Count, event.Message, string(eventJSON),
	)
	if err != nil {
		return fmt.Errorf("insert sqlite gap session=%q sequence=%d backend=%q message=%q in %q: %w",
			event.SessionID, event.Sequence, event.Backend, event.Message, recorder.path, err)
	}
	return nil
}

func nullableRecordingTime(value time.Time) sql.NullString {
	if value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: value.UTC().Format(time.RFC3339Nano), Valid: true}
}

func (recorder *sqliteRecorder) Flush() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("flush sqlite output %q: %w", recorder.path, errRecorderClosed)
	}
	return recorder.commitBatch()
}

func (recorder *sqliteRecorder) commitBatch() error {
	if recorder.transaction == nil {
		return nil
	}
	eventStatementErr := recorder.eventStatement.Close()
	gapStatementErr := recorder.gapStatement.Close()
	if eventStatementErr != nil || gapStatementErr != nil {
		rollbackErr := recorder.transaction.Rollback()
		recorder.clearBatch()
		return joinRecorderErrors(
			formatSQLiteStatementCloseError(recorder.path, eventStatementErr),
			formatSQLiteStatementCloseError(recorder.path, gapStatementErr),
			formatSQLiteRollbackError(recorder.path, rollbackErr),
		)
	}
	commitErr := recorder.transaction.Commit()
	recorder.clearBatch()
	if commitErr != nil {
		return fmt.Errorf("commit sqlite event batch in %q: %w", recorder.path, commitErr)
	}
	return nil
}

func (recorder *sqliteRecorder) rollbackBatch() error {
	if recorder.transaction == nil {
		return nil
	}
	eventStatementErr := recorder.eventStatement.Close()
	gapStatementErr := recorder.gapStatement.Close()
	rollbackErr := recorder.transaction.Rollback()
	recorder.clearBatch()
	return joinRecorderErrors(
		formatSQLiteStatementCloseError(recorder.path, eventStatementErr),
		formatSQLiteStatementCloseError(recorder.path, gapStatementErr),
		formatSQLiteRollbackError(recorder.path, rollbackErr),
	)
}

func (recorder *sqliteRecorder) clearBatch() {
	recorder.transaction = nil
	recorder.eventStatement = nil
	recorder.gapStatement = nil
	recorder.pending = 0
}

func (recorder *sqliteRecorder) Close() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return nil
	}
	recorder.closed = true
	flushErr := recorder.commitBatch()
	endedAt := time.Now().UTC()
	_, sessionErr := recorder.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`,
		endedAt.Format(time.RFC3339Nano), recorder.session.ID)
	if sessionErr != nil {
		sessionErr = fmt.Errorf("finish sqlite recording session %q in %q: %w", recorder.session.ID, recorder.path, sessionErr)
	}
	closeErr := recorder.db.Close()
	if closeErr != nil {
		closeErr = fmt.Errorf("close sqlite output %q: %w", recorder.path, closeErr)
	}
	return joinRecorderErrors(flushErr, sessionErr, closeErr)
}

func formatSQLiteStatementCloseError(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("close prepared sqlite statement in %q: %w", path, err)
}

func formatSQLiteRollbackError(path string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("roll back sqlite transaction in %q: %w", path, err)
}
