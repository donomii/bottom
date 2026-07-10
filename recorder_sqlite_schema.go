package main

import (
	"database/sql"
	"fmt"
	"time"
)

const recordingTimeKeyLayout = "2006-01-02T15:04:05.000000000Z"

type sqliteMigration struct {
	version         int
	statements      []string
	transform       func(*sql.Tx, string) error
	finalStatements []string
}

var sqliteMigrations = []sqliteMigration{
	{
		version: 1,
		statements: []string{`CREATE TABLE IF NOT EXISTS events (
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
		)`},
	},
	{
		version: 2,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS sessions (
				id TEXT PRIMARY KEY,
				schema_version INTEGER NOT NULL,
				started_at TEXT NOT NULL,
				ended_at TEXT,
				hostname TEXT NOT NULL,
				boot_id TEXT,
				os TEXT NOT NULL,
				arch TEXT NOT NULL,
				backend TEXT NOT NULL
			)`,
			`ALTER TABLE events ADD COLUMN recording_session_id TEXT`,
			`CREATE TABLE IF NOT EXISTS gaps (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				recording_session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
				time TEXT NOT NULL,
				backend TEXT NOT NULL,
				message TEXT NOT NULL
			)`,
			`CREATE INDEX IF NOT EXISTS events_recording_session_time ON events(recording_session_id, time)`,
			`CREATE INDEX IF NOT EXISTS events_kind_time ON events(kind, time)`,
			`CREATE INDEX IF NOT EXISTS events_pid_time ON events(pid, time)`,
			`CREATE INDEX IF NOT EXISTS events_command ON events(command)`,
			`CREATE INDEX IF NOT EXISTS gaps_recording_session_time ON gaps(recording_session_id, time)`,
			`CREATE INDEX IF NOT EXISTS sessions_started_at ON sessions(started_at)`,
		},
	},
	{
		version: 3,
		statements: []string{
			`ALTER TABLE events ADD COLUMN session_id TEXT`,
			`ALTER TABLE events ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE events ADD COLUMN sequence INTEGER`,
			`ALTER TABLE events ADD COLUMN observed_at TEXT`,
			`ALTER TABLE events ADD COLUMN host TEXT`,
			`ALTER TABLE events ADD COLUMN boot_id TEXT`,
			`ALTER TABLE events ADD COLUMN process_id TEXT`,
			`ALTER TABLE events ADD COLUMN uid TEXT`,
			`ALTER TABLE events ADD COLUMN tty TEXT`,
			`ALTER TABLE events ADD COLUMN process_session TEXT`,
			`ALTER TABLE events ADD COLUMN cgroup TEXT`,
			`ALTER TABLE events ADD COLUMN systemd_unit TEXT`,
			`ALTER TABLE events ADD COLUMN container_id TEXT`,
			`ALTER TABLE events ADD COLUMN event_json TEXT`,
			`ALTER TABLE gaps ADD COLUMN session_id TEXT`,
			`ALTER TABLE gaps ADD COLUMN schema_version INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE gaps ADD COLUMN sequence INTEGER`,
			`ALTER TABLE gaps ADD COLUMN observed_at TEXT`,
			`ALTER TABLE gaps ADD COLUMN host TEXT`,
			`ALTER TABLE gaps ADD COLUMN boot_id TEXT`,
			`ALTER TABLE gaps ADD COLUMN count INTEGER`,
			`ALTER TABLE gaps ADD COLUMN event_json TEXT`,
			`CREATE INDEX IF NOT EXISTS events_session_sequence ON events(session_id, sequence)`,
			`CREATE INDEX IF NOT EXISTS events_time ON events(time)`,
			`CREATE INDEX IF NOT EXISTS events_process_time ON events(process_id, time)`,
			`CREATE INDEX IF NOT EXISTS events_parent_time ON events(parent_pid, time)`,
			`CREATE INDEX IF NOT EXISTS events_exe_time ON events(exe, time)`,
			`CREATE INDEX IF NOT EXISTS events_command_time ON events(command, time)`,
			`CREATE INDEX IF NOT EXISTS events_exit_time ON events(exit_code, time)`,
			`CREATE INDEX IF NOT EXISTS events_unit_time ON events(systemd_unit, time)`,
			`CREATE INDEX IF NOT EXISTS events_container_time ON events(container_id, time)`,
			`CREATE INDEX IF NOT EXISTS gaps_session_sequence ON gaps(session_id, sequence)`,
			`CREATE INDEX IF NOT EXISTS gaps_time ON gaps(time)`,
		},
	},
	{
		version: 4,
		statements: []string{
			`ALTER TABLE events ADD COLUMN time_key TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE gaps ADD COLUMN time_key TEXT NOT NULL DEFAULT ''`,
			`DROP INDEX IF EXISTS events_recording_session_time`,
			`DROP INDEX IF EXISTS events_time`,
			`DROP INDEX IF EXISTS events_kind_time`,
			`DROP INDEX IF EXISTS events_pid_time`,
			`DROP INDEX IF EXISTS events_process_time`,
			`DROP INDEX IF EXISTS events_parent_time`,
			`DROP INDEX IF EXISTS events_exe_time`,
			`DROP INDEX IF EXISTS events_command_time`,
			`DROP INDEX IF EXISTS events_exit_time`,
			`DROP INDEX IF EXISTS events_unit_time`,
			`DROP INDEX IF EXISTS events_container_time`,
			`DROP INDEX IF EXISTS gaps_recording_session_time`,
			`DROP INDEX IF EXISTS gaps_time`,
		},
		transform: backfillSQLiteTimeKeys,
		finalStatements: []string{
			`CREATE INDEX events_recording_session_time ON events(recording_session_id, time_key, sequence)`,
			`CREATE INDEX events_time ON events(time_key, sequence)`,
			`CREATE INDEX events_kind_time ON events(kind, time_key, sequence)`,
			`CREATE INDEX events_pid_time ON events(pid, time_key, sequence)`,
			`CREATE INDEX events_process_time ON events(process_id, time_key, sequence)`,
			`CREATE INDEX events_parent_time ON events(parent_pid, time_key, sequence)`,
			`CREATE INDEX events_exe_time ON events(exe, time_key, sequence)`,
			`CREATE INDEX events_command_time ON events(command, time_key, sequence)`,
			`CREATE INDEX events_exit_time ON events(exit_code, time_key, sequence)`,
			`CREATE INDEX events_unit_time ON events(systemd_unit, time_key, sequence)`,
			`CREATE INDEX events_container_time ON events(container_id, time_key, sequence)`,
			`CREATE INDEX gaps_recording_session_time ON gaps(recording_session_id, time_key, sequence)`,
			`CREATE INDEX gaps_time ON gaps(time_key, sequence)`,
			`CREATE INDEX events_legacy_time ON events(time_key, sequence) WHERE event_json IS NULL OR event_json = ''`,
			`CREATE INDEX gaps_legacy_time ON gaps(time_key, sequence) WHERE event_json IS NULL OR event_json = ''`,
		},
	},
}

func formatRecordingTimeKey(value time.Time) string {
	return value.UTC().Format(recordingTimeKeyLayout)
}

func (recorder *sqliteRecorder) createSchema() error {
	if _, err := recorder.db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create sqlite schema migration table in %q: %w", recorder.path, err)
	}
	currentVersion := 0
	if err := recorder.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&currentVersion); err != nil {
		return fmt.Errorf("read sqlite schema version from %q: %w", recorder.path, err)
	}
	if currentVersion > recordingSchemaVersion {
		return fmt.Errorf("open sqlite database %q: expected schema version at most %d, received %d", recorder.path, recordingSchemaVersion, currentVersion)
	}
	for _, migration := range sqliteMigrations {
		if migration.version <= currentVersion {
			continue
		}
		if err := recorder.applyMigration(migration); err != nil {
			return err
		}
	}
	return nil
}

func (recorder *sqliteRecorder) applyMigration(migration sqliteMigration) error {
	transaction, err := recorder.db.Begin()
	if err != nil {
		return fmt.Errorf("begin sqlite schema migration %d in %q: %w", migration.version, recorder.path, err)
	}
	statementNumber := 0
	for _, statement := range migration.statements {
		statementNumber++
		if _, err := transaction.Exec(statement); err != nil {
			return recorder.rollbackMigration(transaction, migration.version, statementNumber, err)
		}
	}
	if migration.transform != nil {
		if err := migration.transform(transaction, recorder.path); err != nil {
			rollbackErr := transaction.Rollback()
			return joinRecorderErrors(
				fmt.Errorf("transform sqlite schema migration %d in %q: %w", migration.version, recorder.path, err),
				formatSQLiteRollbackError(recorder.path, rollbackErr),
			)
		}
	}
	for _, statement := range migration.finalStatements {
		statementNumber++
		if _, err := transaction.Exec(statement); err != nil {
			return recorder.rollbackMigration(transaction, migration.version, statementNumber, err)
		}
	}
	if _, err := transaction.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		migration.version, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		rollbackErr := transaction.Rollback()
		return joinRecorderErrors(
			fmt.Errorf("record sqlite schema migration %d in %q: %w", migration.version, recorder.path, err),
			formatSQLiteRollbackError(recorder.path, rollbackErr),
		)
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("commit sqlite schema migration %d in %q: %w", migration.version, recorder.path, err)
	}
	return nil
}

func (recorder *sqliteRecorder) rollbackMigration(transaction *sql.Tx, version int, statementNumber int, statementErr error) error {
	rollbackErr := transaction.Rollback()
	return joinRecorderErrors(
		fmt.Errorf("apply sqlite schema migration %d statement %d in %q: %w", version, statementNumber, recorder.path, statementErr),
		formatSQLiteRollbackError(recorder.path, rollbackErr),
	)
}

type sqliteTimeKeyRow struct {
	id    int64
	value string
}

func backfillSQLiteTimeKeys(transaction *sql.Tx, path string) error {
	for _, table := range []string{"events", "gaps"} {
		if err := backfillSQLiteTableTimeKeys(transaction, path, table); err != nil {
			return err
		}
	}
	return nil
}

func backfillSQLiteTableTimeKeys(transaction *sql.Tx, path string, table string) error {
	lastID := int64(-1)
	for {
		rows, err := transaction.Query(`SELECT id, time FROM `+table+` WHERE id > ? AND time_key = '' ORDER BY id LIMIT 256`, lastID)
		if err != nil {
			return fmt.Errorf("read %s times to migrate sqlite recording %q: %w", table, path, err)
		}
		batch, err := readSQLiteTimeKeyBatch(rows, path, table)
		if err != nil {
			return err
		}
		if len(batch) == 0 {
			return nil
		}
		for _, row := range batch {
			if _, err := transaction.Exec(`UPDATE `+table+` SET time_key = ? WHERE id = ?`, row.value, row.id); err != nil {
				return fmt.Errorf("store normalized time key for %s row %d in sqlite recording %q: %w", table, row.id, path, err)
			}
		}
		lastID = batch[len(batch)-1].id
	}
}

func readSQLiteTimeKeyBatch(rows *sql.Rows, path string, table string) ([]sqliteTimeKeyRow, error) {
	batch := []sqliteTimeKeyRow{}
	for rows.Next() {
		var id int64
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			closeErr := rows.Close()
			return nil, joinRecorderErrors(fmt.Errorf("scan %s time to migrate sqlite recording %q: %w", table, path, err), closeErr)
		}
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			closeErr := rows.Close()
			return nil, joinRecorderErrors(
				fmt.Errorf("parse %s time %q for row %d while migrating sqlite recording %q: %w", table, value, id, path, err),
				closeErr,
			)
		}
		batch = append(batch, sqliteTimeKeyRow{id: id, value: formatRecordingTimeKey(parsed)})
	}
	iterateErr := rows.Err()
	if iterateErr != nil {
		iterateErr = fmt.Errorf("iterate %s times while migrating sqlite recording %q: %w", table, path, iterateErr)
	}
	return batch, joinRecorderErrors(iterateErr, rows.Close())
}
