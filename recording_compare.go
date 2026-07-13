package main

import (
	"flag"
	"fmt"
	"io"
	"sort"
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
	values [2]map[string]processEpisodeStats
}

func parseRecordingCompareConfig(args []string) (RecordingCompareConfig, error) {
	config := RecordingCompareConfig{BeforePath: "before.jsonl", AfterPath: "after.jsonl"}
	flagset := flag.NewFlagSet("bottom compare", flag.ContinueOnError)
	flagset.StringVar(&config.BeforePath, "before", config.BeforePath, "baseline JSONL recording")
	flagset.StringVar(&config.AfterPath, "after", config.AfterPath, "comparison JSONL recording")
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
	if err := aggregateJSONProcessEpisodes(config.BeforePath, comparisonBefore, aggregator); err != nil {
		return fmt.Errorf("read baseline recording: %w", err)
	}
	if err := aggregateJSONProcessEpisodes(config.AfterPath, comparisonAfter, aggregator); err != nil {
		return fmt.Errorf("read comparison recording: %w", err)
	}
	writer, closer, err := openOutput(config.OutputPath)
	if err != nil {
		return err
	}
	compareErr := aggregator.write(writer)
	var closeErr error
	if closer != nil {
		closeErr = closer.Close()
	}
	return joinRecorderErrors(compareErr, closeErr)
}

func newProcessEpisodeAggregator() (*processEpisodeAggregator, error) {
	return &processEpisodeAggregator{values: [2]map[string]processEpisodeStats{{}, {}}}, nil
}

func aggregateJSONProcessEpisodes(path string, side int, aggregator *processEpisodeAggregator) error {
	reader, err := openJSONRecordingReader(path)
	if err != nil {
		return err
	}
	readErr := reader.Stream(Filter{EventMode: EventModeAll}, 0, func(event Event) error {
		return aggregator.observe(side, event)
	})
	return joinRecorderErrors(readErr, reader.Close())
}

func (aggregator *processEpisodeAggregator) observe(side int, event Event) error {
	if side != comparisonBefore && side != comparisonAfter {
		return fmt.Errorf("aggregate recording comparison: expected side %d or %d, received %d", comparisonBefore, comparisonAfter, side)
	}
	if event.Kind != EventStart && event.Kind != EventExec && event.Kind != EventStop {
		return nil
	}
	key := processEpisodeFingerprint(event)
	if key == "" {
		return nil
	}
	stats := aggregator.values[side][key]
	switch event.Kind {
	case EventStart:
		stats.starts++
	case EventExec:
		stats.execs++
	case EventStop:
		stats.stops++
		stats.totalDuration += recordingDuration(event)
		if event.ExitCode != nil && *event.ExitCode != 0 {
			stats.failures++
		}
	}
	aggregator.values[side][key] = stats
	return nil
}

func (aggregator *processEpisodeAggregator) write(writer io.Writer) error {
	if _, err := io.WriteString(writer, "bottom recording comparison\n"); err != nil {
		return fmt.Errorf("write process recording comparison heading: %w", err)
	}
	if _, err := io.WriteString(writer, "delta_start delta_exec delta_stop delta_failure before_avg after_avg process_group\n"); err != nil {
		return fmt.Errorf("write process recording comparison columns: %w", err)
	}
	keys := make([]string, 0, len(aggregator.values[comparisonBefore])+len(aggregator.values[comparisonAfter]))
	seen := map[string]struct{}{}
	for _, side := range []int{comparisonBefore, comparisonAfter} {
		for key := range aggregator.values[side] {
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		before := aggregator.values[comparisonBefore][key]
		after := aggregator.values[comparisonAfter][key]
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
			return fmt.Errorf("write process recording comparison result for %q: %w", key, err)
		}
	}
	return nil
}

func (aggregator *processEpisodeAggregator) Close() error {
	return nil
}

func writeRecordingComparison(writer io.Writer, before []Event, after []Event) error {
	aggregator, err := newProcessEpisodeAggregator()
	if err != nil {
		return err
	}
	for _, event := range before {
		if err := aggregator.observe(comparisonBefore, event); err != nil {
			return err
		}
	}
	for _, event := range after {
		if err := aggregator.observe(comparisonAfter, event); err != nil {
			return err
		}
	}
	return aggregator.write(writer)
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
