package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type recordingFilterFlags struct {
	include      listFlag
	exclude      listFlag
	includeRegex listFlag
	excludeRegex listFlag
	since        string
	until        string
	exitCode     optionalIntFlag
}

type optionalIntFlag struct {
	set   bool
	value int
}

func (value *optionalIntFlag) String() string {
	if !value.set {
		return ""
	}
	return strconv.Itoa(value.value)
}

func (value *optionalIntFlag) Set(text string) error {
	parsed, err := strconv.Atoi(text)
	if err != nil {
		return fmt.Errorf("expected an integer exit code, received %q", text)
	}
	value.set = true
	value.value = parsed
	return nil
}

func parseRecordingReadConfig(command string, args []string, now time.Time) (RecordingReadConfig, error) {
	config := RecordingReadConfig{
		InputPath: "bottom.sqlite",
		Format:    FormatText,
		Speed:     1,
		MaxDelay:  time.Second,
		Filter:    Filter{EventMode: EventModeAll},
	}
	filterFlags := recordingFilterFlags{}
	format := string(config.Format)
	flagset := flag.NewFlagSet("bottom "+command, flag.ContinueOnError)
	flagset.StringVar(&config.InputPath, "input", config.InputPath, "SQLite recording to read")
	flagset.IntVar(&config.Limit, "limit", 0, "maximum matching events to read; zero reads all matching events")
	switch command {
	case "query":
		flagset.StringVar(&config.OutputPath, "output", "", "append query output to this owner-only file; empty writes to stdout")
		flagset.StringVar(&format, "format", format, "query output format: text, jsonl, or csv")
	case "report":
		flagset.StringVar(&config.OutputPath, "output", "", "append report output to this owner-only file; empty writes to stdout")
	case "replay":
		flagset.Float64Var(&config.Speed, "speed", config.Speed, "replay speed multiplier; 2 replays twice as fast")
		flagset.DurationVar(&config.MaxDelay, "max-delay", config.MaxDelay, "maximum real delay between replayed events; zero preserves the full recorded delay")
		flagset.BoolVar(&config.TUI, "tui", false, "replay through the interactive terminal timeline")
	default:
		return RecordingReadConfig{}, fmt.Errorf("recording reader command must be query, report, or replay, received %q", command)
	}
	bindRecordingFilterFlags(flagset, &config.Filter, &filterFlags)
	if err := flagset.Parse(args); err != nil {
		return RecordingReadConfig{}, err
	}
	if flagset.NArg() != 0 {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s expected options only, received positional arguments %q", command, strings.Join(flagset.Args(), " "))
	}
	config.Format = OutputFormat(format)
	if command == "query" && config.Format != FormatText && config.Format != FormatJSONL && config.Format != FormatCSV {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s format must be text, jsonl, or csv, received %q", command, config.Format)
	}
	if config.InputPath == "" {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s input path must be non-empty", command)
	}
	if config.Limit < 0 {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s limit must not be negative, received %d", command, config.Limit)
	}
	if config.Speed <= 0 {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s speed must be positive, received %g", command, config.Speed)
	}
	if config.MaxDelay < 0 {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s max delay must not be negative, received %s", command, config.MaxDelay)
	}
	if err := finalizeRecordingFilter(&config.Filter, filterFlags, now); err != nil {
		return RecordingReadConfig{}, fmt.Errorf("bottom %s: %w", command, err)
	}
	return config, nil
}

func bindRecordingFilterFlags(flagset *flag.FlagSet, filter *Filter, values *recordingFilterFlags) {
	flagset.Var(&values.include, "include", "keep events whose searchable fields contain this text; may be repeated")
	flagset.Var(&values.exclude, "exclude", "remove events whose searchable fields contain this text; may be repeated")
	flagset.Var(&values.includeRegex, "include-regex", "keep events whose searchable fields match this regular expression; may be repeated")
	flagset.Var(&values.excludeRegex, "exclude-regex", "remove events whose searchable fields match this regular expression; may be repeated")
	flagset.StringVar(&filter.User, "user", "", "keep events for this user name or numeric id")
	flagset.StringVar(&filter.CwdContains, "cwd", "", "keep events whose current directory contains this text")
	flagset.StringVar(&filter.ExeContains, "exe", "", "keep events whose executable path contains this text")
	flagset.StringVar(&filter.ContainerContains, "container", "", "keep events whose container id contains this text")
	flagset.StringVar(&filter.UnitContains, "unit", "", "keep events whose system service unit contains this text")
	flagset.IntVar(&filter.ParentPID, "ppid", 0, "keep events with this immediate parent process id")
	flagset.IntVar(&filter.AncestorPID, "ancestor-pid", 0, "keep events descended from this process id")
	flagset.StringVar(&filter.EventMode, "events", filter.EventMode, "event kind: start, exec, stop, churn, gap, all, or both")
	flagset.DurationVar(&filter.MinDuration, "min-duration", 0, "keep stop events with at least this lifetime")
	flagset.DurationVar(&filter.MaxDuration, "max-duration", 0, "keep stop events with no more than this lifetime")
	flagset.StringVar(&values.since, "since", "", "keep events after this RFC3339 timestamp or duration before now, such as 15m")
	flagset.StringVar(&values.until, "until", "", "keep events before this RFC3339 timestamp or duration before now, such as 5m")
	flagset.Var(&values.exitCode, "exit-code", "keep stop events with this exit code")
}

func finalizeRecordingFilter(filter *Filter, values recordingFilterFlags, now time.Time) error {
	filter.Include = []string(values.include)
	filter.Exclude = []string(values.exclude)
	filter.IncludeRegex = []string(values.includeRegex)
	filter.ExcludeRegex = []string(values.excludeRegex)
	filter.HasExitCode = values.exitCode.set
	filter.ExitCode = values.exitCode.value
	var err error
	filter.Since, err = parseRecordingTime(values.since, now)
	if err != nil {
		return fmt.Errorf("parse since value: %w", err)
	}
	filter.Until, err = parseRecordingTime(values.until, now)
	if err != nil {
		return fmt.Errorf("parse until value: %w", err)
	}
	if !filter.Since.IsZero() && !filter.Until.IsZero() && filter.Since.After(filter.Until) {
		return fmt.Errorf("since time %s must not be after until time %s", filter.Since.Format(time.RFC3339Nano), filter.Until.Format(time.RFC3339Nano))
	}
	if filter.MinDuration < 0 || filter.MaxDuration < 0 {
		return fmt.Errorf("duration filters must not be negative, received min=%s max=%s", filter.MinDuration, filter.MaxDuration)
	}
	if filter.MaxDuration > 0 && filter.MinDuration > filter.MaxDuration {
		return fmt.Errorf("minimum duration %s must not exceed maximum duration %s", filter.MinDuration, filter.MaxDuration)
	}
	if !validEventMode(filter.EventMode) {
		return fmt.Errorf("events must be start, exec, stop, churn, gap, all, or both, received %q", filter.EventMode)
	}
	for _, expression := range append(append([]string{}, filter.IncludeRegex...), filter.ExcludeRegex...) {
		if _, err := regexp.Compile(expression); err != nil {
			return fmt.Errorf("compile event filter regular expression %q: %w", expression, err)
		}
	}
	return nil
}

func parseRecordingTime(value string, now time.Time) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	if duration, err := time.ParseDuration(value); err == nil {
		if duration < 0 {
			return time.Time{}, fmt.Errorf("relative duration must not be negative, received %s", duration)
		}
		return now.Add(-duration), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expected RFC3339 timestamp or duration before now, received %q", value)
	}
	return parsed, nil
}

func runRecordingQuery(config RecordingReadConfig) error {
	reader, err := openSQLiteRecordingReader(config.InputPath)
	if err != nil {
		return err
	}
	if err := rejectRecordingOutputAlias("bottom query", config.OutputPath, config.InputPath); err != nil {
		return joinRecorderErrors(err, reader.Close())
	}
	recorder, err := newQueryOutputRecorder(config)
	if err != nil {
		return joinRecorderErrors(err, reader.Close())
	}
	readErr := reader.Stream(config.Filter, config.Limit, recorder.Write)
	readerCloseErr := reader.Close()
	recorderCloseErr := recorder.Close()
	return joinRecorderErrors(readErr, readerCloseErr, recorderCloseErr)
}

func newQueryOutputRecorder(config RecordingReadConfig) (Recorder, error) {
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
	default:
		return nil, fmt.Errorf("query output format must be text, jsonl, or csv, received %q", config.Format)
	}
}

func filteredRecordingEvents(config RecordingReadConfig) ([]Event, error) {
	reader, err := openSQLiteRecordingReader(config.InputPath)
	if err != nil {
		return nil, err
	}
	filtered := []Event{}
	readErr := reader.Stream(config.Filter, config.Limit, func(event Event) error {
		filtered = append(filtered, event)
		return nil
	})
	closeErr := reader.Close()
	if err := joinRecorderErrors(readErr, closeErr); err != nil {
		return nil, err
	}
	return filtered, nil
}

func runRecordingReplay(ctx context.Context, config RecordingReadConfig) error {
	reader, err := openSQLiteRecordingReader(config.InputPath)
	if err != nil {
		return err
	}
	var recorder Recorder = textRecorder{writer: os.Stdout}
	if config.TUI {
		recorder = NewTUIRecorder(os.Stdout)
	}
	var previous time.Time
	readErr := reader.Stream(config.Filter, config.Limit, func(event Event) error {
		if !previous.IsZero() {
			delay := time.Duration(float64(event.Time.Sub(previous)) / config.Speed)
			if delay < 0 {
				delay = 0
			}
			if config.MaxDelay > 0 && delay > config.MaxDelay {
				delay = config.MaxDelay
			}
			if err := waitForReplay(ctx, delay); err != nil {
				return err
			}
		}
		if err := recorder.Write(event); err != nil {
			return err
		}
		previous = event.Time
		return nil
	})
	readerCloseErr := reader.Close()
	recorderCloseErr := recorder.Close()
	return joinRecorderErrors(readErr, readerCloseErr, recorderCloseErr)
}

func waitForReplay(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
