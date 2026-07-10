package main

import (
	"database/sql"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

func runRecordingReport(config RecordingReadConfig) error {
	if err := rejectRecordingOutputAliases("bottom report", config.OutputPath, config.InputPaths); err != nil {
		return err
	}
	stream, err := openRecordingFileEventStream("bottom report", config.InputPaths, config.Filter, config.Limit)
	if err != nil {
		return err
	}
	summary, err := newRecordingReportSummary()
	if err != nil {
		return joinRecorderErrors(err, stream.Close())
	}
	readErr := stream.Stream(summary.observe)
	if readErr != nil {
		return joinRecorderErrors(readErr, summary.Close())
	}
	if err := summary.finish(); err != nil {
		return joinRecorderErrors(err, summary.Close())
	}
	writer, closer, err := openOutput(config.OutputPath)
	if err != nil {
		return joinRecorderErrors(err, summary.Close())
	}
	reportErr := summary.write(writer)
	var outputCloseErr error
	if closer != nil {
		outputCloseErr = closer.Close()
	}
	return joinRecorderErrors(reportErr, outputCloseErr, summary.Close())
}

func openTemporaryAggregationDB(operation string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "")
	if err != nil {
		return nil, fmt.Errorf("open temporary SQLite database for %s: %w", operation, err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA temp_store = FILE`); err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("configure file-backed temporary SQLite storage for %s: %w", operation, err), closeErr)
	}
	return db, nil
}

type recordingReportSummary struct {
	db               *sql.DB
	transaction      *sql.Tx
	countStatement   *sql.Stmt
	sessionStatement *sql.Stmt
	finished         bool
	eventCount       int
	startCount       int
	execCount        int
	stopCount        int
	churnCount       int
	gapCount         int
	failures         int
	shortest         []Event
	first            time.Time
	last             time.Time
}

func newRecordingReportSummary() (*recordingReportSummary, error) {
	db, err := openTemporaryAggregationDB("recording report aggregation")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE report_counts (
		category TEXT NOT NULL,
		name TEXT NOT NULL,
		count INTEGER NOT NULL,
		PRIMARY KEY (category, name)
	) WITHOUT ROWID;
	CREATE TABLE report_sessions (id TEXT PRIMARY KEY) WITHOUT ROWID`)
	if err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("create temporary recording report tables: %w", err), closeErr)
	}
	transaction, err := db.Begin()
	if err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("begin temporary recording report aggregation: %w", err), closeErr)
	}
	countStatement, err := transaction.Prepare(`INSERT INTO report_counts (category, name, count) VALUES (?, ?, 1)
		ON CONFLICT (category, name) DO UPDATE SET count = count + 1`)
	if err != nil {
		rollbackErr := transaction.Rollback()
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("prepare temporary recording report count update: %w", err), rollbackErr, closeErr)
	}
	sessionStatement, err := transaction.Prepare(`INSERT OR IGNORE INTO report_sessions (id) VALUES (?)`)
	if err != nil {
		statementErr := countStatement.Close()
		rollbackErr := transaction.Rollback()
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("prepare temporary recording report session update: %w", err), statementErr, rollbackErr, closeErr)
	}
	return &recordingReportSummary{
		db: db, transaction: transaction, countStatement: countStatement, sessionStatement: sessionStatement, shortest: []Event{},
	}, nil
}

func (summary *recordingReportSummary) observe(event Event) error {
	if summary.finished {
		return fmt.Errorf("aggregate recording report event kind=%s pid=%d: aggregation is already finished", event.Kind, event.PID)
	}
	if _, err := summary.sessionStatement.Exec(event.SessionID); err != nil {
		return fmt.Errorf("aggregate recording report session %q: %w", event.SessionID, err)
	}
	if event.Exe != "" && (event.Kind == EventStart || event.Kind == EventExec) {
		if err := summary.increment("executable", event.Exe); err != nil {
			return err
		}
	}
	if event.Kind == EventStart {
		parent := reportParent(event)
		if err := summary.increment("parent", parent); err != nil {
			return err
		}
		child := reportExecutable(event)
		for _, ancestor := range reportAncestry(event) {
			if err := summary.increment("edge", ancestor+" -> "+child); err != nil {
				return err
			}
			child = ancestor
		}
	}
	summary.eventCount++
	summary.incrementKind(event.Kind)
	if summary.first.IsZero() {
		summary.first = event.Time
	}
	summary.last = event.Time
	if event.Kind == EventStop {
		summary.shortest = append(summary.shortest, event)
		sort.SliceStable(summary.shortest, func(left int, right int) bool {
			return summary.shortest[left].DurationMillis < summary.shortest[right].DurationMillis
		})
		if len(summary.shortest) > 10 {
			summary.shortest = summary.shortest[:10]
		}
		if event.ExitCode != nil && *event.ExitCode != 0 {
			summary.failures++
		}
	}
	return nil
}

func (summary *recordingReportSummary) increment(category string, name string) error {
	if _, err := summary.countStatement.Exec(category, name); err != nil {
		return fmt.Errorf("aggregate recording report %s %q: %w", category, name, err)
	}
	return nil
}

func (summary *recordingReportSummary) incrementKind(kind EventKind) {
	switch kind {
	case EventStart:
		summary.startCount++
	case EventExec:
		summary.execCount++
	case EventStop:
		summary.stopCount++
	case EventChurn:
		summary.churnCount++
	case EventGap:
		summary.gapCount++
	}
}

func writeRecordingReport(writer io.Writer, events []Event) error {
	summary, err := newRecordingReportSummary()
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := summary.observe(event); err != nil {
			return joinRecorderErrors(err, summary.Close())
		}
	}
	writeErr := summary.write(writer)
	return joinRecorderErrors(writeErr, summary.Close())
}

func (summary *recordingReportSummary) write(writer io.Writer) error {
	if err := summary.finish(); err != nil {
		return err
	}
	var sessionCount int
	if err := summary.db.QueryRow(`SELECT COUNT(*) FROM report_sessions`).Scan(&sessionCount); err != nil {
		return fmt.Errorf("count temporary recording report sessions: %w", err)
	}
	var builder strings.Builder
	builder.WriteString("bottom recording report\n")
	builder.WriteString(fmt.Sprintf("events=%d sessions=%d gaps=%d failed_exits=%d\n", summary.eventCount, sessionCount, summary.gapCount, summary.failures))
	if summary.eventCount > 0 {
		builder.WriteString(fmt.Sprintf("from=%s until=%s\n", summary.first.Format(time.RFC3339Nano), summary.last.Format(time.RFC3339Nano)))
	}
	builder.WriteString("\nEvent kinds\n")
	for _, item := range []struct {
		kind  EventKind
		count int
	}{
		{kind: EventStart, count: summary.startCount},
		{kind: EventExec, count: summary.execCount},
		{kind: EventStop, count: summary.stopCount},
		{kind: EventChurn, count: summary.churnCount},
		{kind: EventGap, count: summary.gapCount},
	} {
		builder.WriteString(fmt.Sprintf("%8d  %s\n", item.count, item.kind))
	}
	if err := summary.writeCounts(&builder, "Top executables", "executable", 10); err != nil {
		return err
	}
	if err := summary.writeCounts(&builder, "Top parents", "parent", 10); err != nil {
		return err
	}
	if err := summary.writeCounts(&builder, "Process ancestry edges", "edge", 20); err != nil {
		return err
	}
	builder.WriteString("\nShortest lifetimes\n")
	for _, event := range summary.shortest {
		builder.WriteString(fmt.Sprintf("%8s  pid=%d exe=%q cmd=%q\n", time.Duration(event.DurationMillis)*time.Millisecond, event.PID, event.Exe, event.Command))
	}
	if _, err := io.WriteString(writer, builder.String()); err != nil {
		return fmt.Errorf("write process recording report: %w", err)
	}
	return nil
}

func (summary *recordingReportSummary) writeCounts(builder *strings.Builder, title string, category string, limit int) error {
	rows, err := summary.db.Query(`SELECT name, count FROM report_counts WHERE category = ? ORDER BY count DESC, name LIMIT ?`, category, limit)
	if err != nil {
		return fmt.Errorf("query temporary recording report %s counts: %w", category, err)
	}
	builder.WriteString("\n" + title + "\n")
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			closeErr := rows.Close()
			return joinRecorderErrors(fmt.Errorf("scan temporary recording report %s count: %w", category, err), closeErr)
		}
		builder.WriteString(fmt.Sprintf("%8d  %s\n", count, sanitizeTerminalText(name)))
	}
	iterateErr := rows.Err()
	if iterateErr != nil {
		iterateErr = fmt.Errorf("iterate temporary recording report %s counts: %w", category, iterateErr)
	}
	return joinRecorderErrors(iterateErr, rows.Close())
}

func (summary *recordingReportSummary) finish() error {
	if summary.finished {
		return nil
	}
	countStatementErr := summary.countStatement.Close()
	sessionStatementErr := summary.sessionStatement.Close()
	if countStatementErr != nil || sessionStatementErr != nil {
		rollbackErr := summary.transaction.Rollback()
		summary.finished = true
		return joinRecorderErrors(countStatementErr, sessionStatementErr, rollbackErr)
	}
	if err := summary.transaction.Commit(); err != nil {
		rollbackErr := summary.transaction.Rollback()
		summary.finished = true
		return joinRecorderErrors(fmt.Errorf("commit temporary recording report aggregation: %w", err), rollbackErr)
	}
	summary.finished = true
	return nil
}

func (summary *recordingReportSummary) Close() error {
	if summary == nil || summary.db == nil {
		return nil
	}
	var finishErr error
	if !summary.finished {
		countStatementErr := summary.countStatement.Close()
		sessionStatementErr := summary.sessionStatement.Close()
		rollbackErr := summary.transaction.Rollback()
		finishErr = joinRecorderErrors(countStatementErr, sessionStatementErr, rollbackErr)
		summary.finished = true
	}
	closeErr := summary.db.Close()
	summary.db = nil
	if closeErr != nil {
		closeErr = fmt.Errorf("close temporary recording report database: %w", closeErr)
	}
	return joinRecorderErrors(finishErr, closeErr)
}

func reportParent(event Event) string {
	if len(event.ParentChain) > 0 {
		return reportProcessSummary(event.ParentChain[0])
	}
	return strconv.Itoa(event.ParentPID)
}

func reportAncestry(event Event) []string {
	if len(event.ParentChain) == 0 {
		return []string{strconv.Itoa(event.ParentPID)}
	}
	ancestry := make([]string, 0, len(event.ParentChain))
	for _, parent := range event.ParentChain {
		ancestry = append(ancestry, reportProcessSummary(parent))
	}
	return ancestry
}

func reportProcessSummary(process ProcessSummary) string {
	if process.Exe != "" {
		return process.Exe
	}
	if process.Command != "" {
		return process.Command
	}
	return strconv.Itoa(process.PID)
}

func reportExecutable(event Event) string {
	if event.Exe != "" {
		return event.Exe
	}
	fields := strings.Fields(event.Command)
	if len(fields) > 0 {
		return fields[0]
	}
	return strconv.Itoa(event.PID)
}
