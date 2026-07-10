package main

import (
	"database/sql"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	comparisonBefore = 0
	comparisonAfter  = 1
)

type processEpisodeStats struct {
	starts        int
	execs         int
	stops         int
	failures      int
	totalDuration time.Duration
}

type processEpisodeAggregator struct {
	db          *sql.DB
	transaction *sql.Tx
	statement   *sql.Stmt
	finished    bool
}

func parseRecordingCompareConfig(args []string) (RecordingCompareConfig, error) {
	config := RecordingCompareConfig{BeforePath: "before.sqlite", AfterPath: "after.sqlite"}
	flagset := flag.NewFlagSet("bottom compare", flag.ContinueOnError)
	flagset.StringVar(&config.BeforePath, "before", config.BeforePath, "baseline SQLite recording")
	flagset.StringVar(&config.AfterPath, "after", config.AfterPath, "comparison SQLite recording")
	flagset.StringVar(&config.OutputPath, "output", "", "append the comparison report to this owner-only file; empty writes to stdout")
	if err := flagset.Parse(args); err != nil {
		return RecordingCompareConfig{}, err
	}
	if flagset.NArg() != 0 {
		return RecordingCompareConfig{}, fmt.Errorf("bottom compare expected options only, received positional arguments %q", strings.Join(flagset.Args(), " "))
	}
	if config.BeforePath == "" || config.AfterPath == "" {
		return RecordingCompareConfig{}, fmt.Errorf("bottom compare requires non-empty before and after recording paths")
	}
	if config.BeforePath == config.AfterPath {
		return RecordingCompareConfig{}, fmt.Errorf("bottom compare requires different recordings, received %q for both", config.BeforePath)
	}
	return config, nil
}

func runRecordingCompare(config RecordingCompareConfig) error {
	sameInputs, err := recordingPathsReferToSameFile(config.BeforePath, config.AfterPath)
	if err != nil {
		return fmt.Errorf("bottom compare validate baseline %q and comparison recording %q: %w", config.BeforePath, config.AfterPath, err)
	}
	if sameInputs {
		return fmt.Errorf("bottom compare requires different recordings, received %q and %q resolving to the same file", config.BeforePath, config.AfterPath)
	}
	if err := rejectRecordingOutputAlias("bottom compare", config.OutputPath, config.BeforePath); err != nil {
		return err
	}
	if err := rejectRecordingOutputAlias("bottom compare", config.OutputPath, config.AfterPath); err != nil {
		return err
	}
	aggregator, err := newProcessEpisodeAggregator()
	if err != nil {
		return err
	}
	if err := aggregateSQLiteProcessEpisodes(config.BeforePath, comparisonBefore, aggregator); err != nil {
		return joinRecorderErrors(fmt.Errorf("read baseline recording: %w", err), aggregator.Close())
	}
	if err := aggregateSQLiteProcessEpisodes(config.AfterPath, comparisonAfter, aggregator); err != nil {
		return joinRecorderErrors(fmt.Errorf("read comparison recording: %w", err), aggregator.Close())
	}
	if err := aggregator.finish(); err != nil {
		return joinRecorderErrors(err, aggregator.Close())
	}
	writer, closer, err := openOutput(config.OutputPath)
	if err != nil {
		return joinRecorderErrors(err, aggregator.Close())
	}
	compareErr := aggregator.write(writer)
	var outputCloseErr error
	if closer != nil {
		outputCloseErr = closer.Close()
	}
	return joinRecorderErrors(compareErr, outputCloseErr, aggregator.Close())
}

func newProcessEpisodeAggregator() (*processEpisodeAggregator, error) {
	db, err := openTemporaryAggregationDB("recording comparison aggregation")
	if err != nil {
		return nil, err
	}
	_, err = db.Exec(`CREATE TABLE process_episode_stats (
		side INTEGER NOT NULL,
		process_group TEXT NOT NULL,
		starts INTEGER NOT NULL,
		execs INTEGER NOT NULL,
		stops INTEGER NOT NULL,
		failures INTEGER NOT NULL,
		total_duration_ms INTEGER NOT NULL,
		PRIMARY KEY (side, process_group)
	) WITHOUT ROWID`)
	if err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("create temporary recording comparison table: %w", err), closeErr)
	}
	transaction, err := db.Begin()
	if err != nil {
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("begin temporary recording comparison aggregation: %w", err), closeErr)
	}
	statement, err := transaction.Prepare(`INSERT INTO process_episode_stats (
		side, process_group, starts, execs, stops, failures, total_duration_ms
	) VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT (side, process_group) DO UPDATE SET
		starts = starts + excluded.starts,
		execs = execs + excluded.execs,
		stops = stops + excluded.stops,
		failures = failures + excluded.failures,
		total_duration_ms = total_duration_ms + excluded.total_duration_ms`)
	if err != nil {
		rollbackErr := transaction.Rollback()
		closeErr := db.Close()
		return nil, joinRecorderErrors(fmt.Errorf("prepare temporary recording comparison update: %w", err), rollbackErr, closeErr)
	}
	return &processEpisodeAggregator{db: db, transaction: transaction, statement: statement}, nil
}

func aggregateSQLiteProcessEpisodes(path string, side int, aggregator *processEpisodeAggregator) error {
	reader, err := openSQLiteRecordingReader(path)
	if err != nil {
		return err
	}
	readErr := reader.Stream(Filter{EventMode: EventModeAll}, 0, func(event Event) error {
		return aggregator.observe(side, event)
	})
	return joinRecorderErrors(readErr, reader.Close())
}

func (aggregator *processEpisodeAggregator) observe(side int, event Event) error {
	if event.Kind != EventStart && event.Kind != EventExec && event.Kind != EventStop {
		return nil
	}
	key := processEpisodeFingerprint(event)
	if key == "" {
		return nil
	}
	starts := 0
	execs := 0
	stops := 0
	failures := 0
	durationMillis := int64(0)
	switch event.Kind {
	case EventStart:
		starts = 1
	case EventExec:
		execs = 1
	case EventStop:
		stops = 1
		durationMillis = event.DurationMillis
		if event.ExitCode != nil && *event.ExitCode != 0 {
			failures = 1
		}
	}
	if _, err := aggregator.statement.Exec(side, key, starts, execs, stops, failures, durationMillis); err != nil {
		return fmt.Errorf("aggregate recording comparison side=%d process_group=%q kind=%s: %w", side, key, event.Kind, err)
	}
	return nil
}

func (aggregator *processEpisodeAggregator) finish() error {
	if aggregator.finished {
		return nil
	}
	statementErr := aggregator.statement.Close()
	if statementErr != nil {
		rollbackErr := aggregator.transaction.Rollback()
		aggregator.finished = true
		return joinRecorderErrors(statementErr, rollbackErr)
	}
	if err := aggregator.transaction.Commit(); err != nil {
		rollbackErr := aggregator.transaction.Rollback()
		aggregator.finished = true
		return joinRecorderErrors(fmt.Errorf("commit temporary recording comparison aggregation: %w", err), rollbackErr)
	}
	aggregator.finished = true
	return nil
}

func (aggregator *processEpisodeAggregator) write(writer io.Writer) error {
	if err := aggregator.finish(); err != nil {
		return err
	}
	if _, err := io.WriteString(writer, "bottom recording comparison\n"); err != nil {
		return fmt.Errorf("write process recording comparison heading: %w", err)
	}
	if _, err := io.WriteString(writer, "delta_start delta_exec delta_stop delta_failure before_avg after_avg process_group\n"); err != nil {
		return fmt.Errorf("write process recording comparison columns: %w", err)
	}
	rows, err := aggregator.db.Query(`SELECT
		process_group,
		COALESCE(MAX(CASE WHEN side = 0 THEN starts END), 0),
		COALESCE(MAX(CASE WHEN side = 0 THEN execs END), 0),
		COALESCE(MAX(CASE WHEN side = 0 THEN stops END), 0),
		COALESCE(MAX(CASE WHEN side = 0 THEN failures END), 0),
		COALESCE(MAX(CASE WHEN side = 0 THEN total_duration_ms END), 0),
		COALESCE(MAX(CASE WHEN side = 1 THEN starts END), 0),
		COALESCE(MAX(CASE WHEN side = 1 THEN execs END), 0),
		COALESCE(MAX(CASE WHEN side = 1 THEN stops END), 0),
		COALESCE(MAX(CASE WHEN side = 1 THEN failures END), 0),
		COALESCE(MAX(CASE WHEN side = 1 THEN total_duration_ms END), 0)
	FROM process_episode_stats
	GROUP BY process_group
	ORDER BY process_group`)
	if err != nil {
		return fmt.Errorf("query temporary recording comparison results: %w", err)
	}
	for rows.Next() {
		var key string
		var before processEpisodeStats
		var after processEpisodeStats
		var beforeDurationMillis int64
		var afterDurationMillis int64
		if err := rows.Scan(
			&key,
			&before.starts, &before.execs, &before.stops, &before.failures, &beforeDurationMillis,
			&after.starts, &after.execs, &after.stops, &after.failures, &afterDurationMillis,
		); err != nil {
			closeErr := rows.Close()
			return joinRecorderErrors(fmt.Errorf("scan temporary recording comparison result: %w", err), closeErr)
		}
		before.totalDuration = time.Duration(beforeDurationMillis) * time.Millisecond
		after.totalDuration = time.Duration(afterDurationMillis) * time.Millisecond
		if before == after {
			continue
		}
		if _, err := fmt.Fprintf(writer, "%+11d %+10d %+10d %+13d %10s %9s %s\n",
			after.starts-before.starts,
			after.execs-before.execs,
			after.stops-before.stops,
			after.failures-before.failures,
			averageEpisodeDuration(before),
			averageEpisodeDuration(after),
			sanitizeTerminalText(key),
		); err != nil {
			closeErr := rows.Close()
			return joinRecorderErrors(fmt.Errorf("write process recording comparison result for %q: %w", key, err), closeErr)
		}
	}
	iterateErr := rows.Err()
	if iterateErr != nil {
		iterateErr = fmt.Errorf("iterate temporary recording comparison results: %w", iterateErr)
	}
	return joinRecorderErrors(iterateErr, rows.Close())
}

func (aggregator *processEpisodeAggregator) Close() error {
	if aggregator == nil || aggregator.db == nil {
		return nil
	}
	var finishErr error
	if !aggregator.finished {
		statementErr := aggregator.statement.Close()
		rollbackErr := aggregator.transaction.Rollback()
		finishErr = joinRecorderErrors(statementErr, rollbackErr)
		aggregator.finished = true
	}
	closeErr := aggregator.db.Close()
	aggregator.db = nil
	if closeErr != nil {
		closeErr = fmt.Errorf("close temporary recording comparison database: %w", closeErr)
	}
	return joinRecorderErrors(finishErr, closeErr)
}

func writeRecordingComparison(writer io.Writer, before []Event, after []Event) error {
	aggregator, err := newProcessEpisodeAggregator()
	if err != nil {
		return err
	}
	for _, event := range before {
		if err := aggregator.observe(comparisonBefore, event); err != nil {
			return joinRecorderErrors(err, aggregator.Close())
		}
	}
	for _, event := range after {
		if err := aggregator.observe(comparisonAfter, event); err != nil {
			return joinRecorderErrors(err, aggregator.Close())
		}
	}
	writeErr := aggregator.write(writer)
	return joinRecorderErrors(writeErr, aggregator.Close())
}

func processEpisodeFingerprint(event Event) string {
	executable := event.Exe
	if executable == "" {
		fields := strings.Fields(event.Command)
		if len(fields) > 0 {
			executable = fields[0]
		}
	}
	if executable == "" {
		return ""
	}
	return executable + " <- " + strings.Join(reportAncestry(event), " <- ")
}

func averageEpisodeDuration(stats processEpisodeStats) time.Duration {
	if stats.stops == 0 {
		return 0
	}
	return stats.totalDuration / time.Duration(stats.stops)
}
