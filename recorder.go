package main

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
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
}

type csvRecorder struct {
	writer *csv.Writer
	closer io.Closer
}

type sqliteRecorder struct {
	db *sql.DB
}

func newRecorder(config Config) (Recorder, error) {
	if config.TUI {
		return NewTUIRecorder(os.Stdout), nil
	}
	switch config.Format {
	case FormatText:
		writer, closer, err := openOutput(config.OutputPath)
		if err != nil {
			return nil, err
		}
		if closer != nil {
			return closingTextRecorder{recorder: textRecorder{writer: writer}, closer: closer}, nil
		}
		return textRecorder{writer: writer}, nil
	case FormatJSONL:
		writer, closer, err := openOutput(config.OutputPath)
		if err != nil {
			return nil, err
		}
		return jsonlRecorder{encoder: json.NewEncoder(writer), closer: closer}, nil
	case FormatCSV:
		return newCSVRecorder(config.OutputPath)
	case FormatSQLite:
		return newSQLiteRecorder(config.OutputPath)
	default:
		return nil, fmt.Errorf("format must be text, jsonl, csv, or sqlite, received %q", config.Format)
	}
}

func openOutput(path string) (io.Writer, io.Closer, error) {
	if path == "" {
		return os.Stdout, nil, nil
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
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
	prefix := fmt.Sprintf("%s %-5s", event.Time.Format("15:04:05.000"), event.Kind)
	switch event.Kind {
	case EventStart:
		return fmt.Sprintf("%s pid=%d ppid=%d user=%s cmd=%q parent=%s", prefix, event.PID, event.ParentPID, event.User, event.Command, flattenParentChain(event.ParentChain))
	case EventStop:
		return fmt.Sprintf("%s pid=%d duration=%s exit=%s cmd=%q", prefix, event.PID, time.Duration(event.DurationMillis)*time.Millisecond, formatExitCode(event.ExitCode), event.Command)
	case EventChurn:
		return fmt.Sprintf("%s count=%d window=%s cmd=%q", prefix, event.Count, time.Duration(event.WindowMillis)*time.Millisecond, event.Command)
	default:
		return fmt.Sprintf("%s message=%q", prefix, event.Message)
	}
}

func (recorder jsonlRecorder) Write(event Event) error {
	if err := recorder.encoder.Encode(event); err != nil {
		return fmt.Errorf("write jsonl event: %w", err)
	}
	return nil
}

func (recorder jsonlRecorder) Close() error {
	if recorder.closer == nil {
		return nil
	}
	return recorder.closer.Close()
}

func newCSVRecorder(path string) (Recorder, error) {
	writer, closer, err := openOutput(path)
	if err != nil {
		return nil, err
	}
	csvWriter := csv.NewWriter(writer)
	if shouldWriteCSVHeader(path) {
		if err := csvWriter.Write(csvHeader()); err != nil {
			return nil, fmt.Errorf("write csv header: %w", err)
		}
		csvWriter.Flush()
	}
	return csvRecorder{writer: csvWriter, closer: closer}, nil
}

func shouldWriteCSVHeader(path string) bool {
	if path == "" {
		return true
	}
	info, err := os.Stat(path)
	if err != nil {
		return true
	}
	return info.Size() == 0
}

func csvHeader() []string {
	return []string{"time", "kind", "pid", "parent_pid", "user", "command", "exe", "cwd", "duration_ms", "exit_code", "backend", "count", "window_ms", "message", "parent_chain"}
}

func (recorder csvRecorder) Write(event Event) error {
	if err := recorder.writer.Write(csvEvent(event)); err != nil {
		return fmt.Errorf("write csv event: %w", err)
	}
	recorder.writer.Flush()
	if err := recorder.writer.Error(); err != nil {
		return fmt.Errorf("flush csv event: %w", err)
	}
	return nil
}

func (recorder csvRecorder) Close() error {
	recorder.writer.Flush()
	if err := recorder.writer.Error(); err != nil {
		return err
	}
	if recorder.closer == nil {
		return nil
	}
	return recorder.closer.Close()
}

func csvEvent(event Event) []string {
	return []string{
		event.Time.Format(time.RFC3339Nano),
		string(event.Kind),
		strconv.Itoa(event.PID),
		strconv.Itoa(event.ParentPID),
		event.User,
		event.Command,
		event.Exe,
		event.Cwd,
		strconv.FormatInt(event.DurationMillis, 10),
		formatExitCode(event.ExitCode),
		event.Backend,
		strconv.Itoa(event.Count),
		strconv.FormatInt(event.WindowMillis, 10),
		event.Message,
		flattenParentChain(event.ParentChain),
	}
}

func newSQLiteRecorder(path string) (Recorder, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database %q: %w", path, err)
	}
	recorder := sqliteRecorder{db: db}
	if err := recorder.createSchema(); err != nil {
		db.Close()
		return nil, err
	}
	return recorder, nil
}

func (recorder sqliteRecorder) createSchema() error {
	_, err := recorder.db.Exec(`CREATE TABLE IF NOT EXISTS events (
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
	)`)
	if err != nil {
		return fmt.Errorf("create sqlite events table: %w", err)
	}
	return nil
}

func (recorder sqliteRecorder) Write(event Event) error {
	exitCode := sql.NullInt64{}
	if event.ExitCode != nil {
		exitCode.Valid = true
		exitCode.Int64 = int64(*event.ExitCode)
	}
	_, err := recorder.db.Exec(`INSERT INTO events (
		time, kind, pid, parent_pid, user, command, exe, cwd, duration_ms, exit_code, backend, count, window_ms, message, parent_chain
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.Time.Format(time.RFC3339Nano),
		string(event.Kind),
		event.PID,
		event.ParentPID,
		event.User,
		event.Command,
		event.Exe,
		event.Cwd,
		event.DurationMillis,
		exitCode,
		event.Backend,
		event.Count,
		event.WindowMillis,
		event.Message,
		flattenParentChain(event.ParentChain),
	)
	if err != nil {
		return fmt.Errorf("insert sqlite event kind=%s pid=%d command=%q: %w", event.Kind, event.PID, event.Command, err)
	}
	return nil
}

func (recorder sqliteRecorder) Close() error {
	return recorder.db.Close()
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
			parts = append(parts, fmt.Sprintf("%d:%s", parent.PID, parent.Command))
		}
	}
	return strings.Join(parts, " <- ")
}
