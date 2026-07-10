package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"regexp"
	"runtime/debug"
	"strings"
	"time"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type listFlag []string

func (values *listFlag) String() string {
	return strings.Join(*values, ",")
}

func (values *listFlag) Set(value string) error {
	if value == "" {
		return fmt.Errorf("expected a non-empty filter value")
	}
	*values = append(*values, value)
	return nil
}

func parseConfig(args []string) (Config, error) {
	config := Config{
		Backend:        "auto",
		Format:         FormatText,
		OutputPath:     "",
		PollInterval:   100 * time.Millisecond,
		ChurnWindow:    10 * time.Second,
		ChurnThreshold: 5,
		ChurnCooldown:  10 * time.Second,
		ChurnMaxKeys:   4096,
		ChurnMaxLife:   5 * time.Second,
		RecorderBuffer: 1024,
		SQLiteBatch:    128,
		SQLiteFlush:    250 * time.Millisecond,
		Trigger:        "churn",
		PostTrigger:    10 * time.Second,
		Filter: Filter{
			EventMode: EventModeAll,
		},
	}
	var include listFlag
	var exclude listFlag
	var includeRegex listFlag
	var excludeRegex listFlag
	var redact listFlag
	format := string(config.Format)
	flagset := flag.NewFlagSet("bottom", flag.ContinueOnError)
	flagset.StringVar(&config.Backend, "backend", config.Backend, "process source: auto, poll, or linux-proc-connector")
	flagset.Var(&include, "include", "show events whose command, executable path, current directory, user, or parent chain contains this text; may be repeated")
	flagset.Var(&exclude, "exclude", "hide events whose command, executable path, current directory, user, or parent chain contains this text; may be repeated")
	flagset.Var(&includeRegex, "include-regex", "show events whose searchable fields match this regular expression; may be repeated")
	flagset.Var(&excludeRegex, "exclude-regex", "hide events whose searchable fields match this regular expression; may be repeated")
	flagset.StringVar(&config.Filter.User, "user", "", "show events owned by this user name or numeric id")
	flagset.StringVar(&config.Filter.CwdContains, "cwd", "", "show events whose current directory contains this text")
	flagset.StringVar(&config.Filter.ExeContains, "exe", "", "show events whose executable path contains this text")
	flagset.StringVar(&config.Filter.ContainerContains, "container", "", "show events whose container id contains this text")
	flagset.StringVar(&config.Filter.UnitContains, "unit", "", "show events whose system service unit contains this text")
	flagset.IntVar(&config.Filter.ParentPID, "ppid", 0, "show events whose immediate parent process has this pid")
	flagset.IntVar(&config.Filter.AncestorPID, "ancestor-pid", 0, "show events descended from this process id")
	flagset.StringVar(&config.Filter.EventMode, "events", config.Filter.EventMode, "event kinds to show: start, exec, stop, churn, gap, all, or both")
	flagset.DurationVar(&config.Filter.MinDuration, "min-duration", 0, "show stop events only when the process lived at least this long")
	flagset.DurationVar(&config.Filter.MaxDuration, "max-duration", 0, "show stop events only when the process lived no longer than this")
	flagset.DurationVar(&config.PollInterval, "poll", config.PollInterval, "polling interval used by the polling backend and fallback mode")
	flagset.StringVar(&format, "format", format, "output format: text, jsonl, csv, or sqlite")
	flagset.StringVar(&config.OutputPath, "output", config.OutputPath, "output file path for text, csv, jsonl, or sqlite; empty writes text, csv, and jsonl to stdout")
	flagset.BoolVar(&config.TUI, "tui", false, "show an interactive terminal timeline; when output is set, record there at the same time")
	flagset.DurationVar(&config.ChurnWindow, "churn-window", config.ChurnWindow, "time window used to group repeated short-lived command starts")
	flagset.IntVar(&config.ChurnThreshold, "churn-threshold", config.ChurnThreshold, "number of starts inside the churn window before bottom reports a churn event")
	flagset.DurationVar(&config.ChurnCooldown, "churn-cooldown", config.ChurnCooldown, "minimum time between repeated churn reports for the same process group")
	flagset.IntVar(&config.ChurnMaxKeys, "churn-max-keys", config.ChurnMaxKeys, "maximum process groups retained by churn detection before the oldest group is evicted")
	flagset.DurationVar(&config.ChurnMaxLife, "churn-max-life", config.ChurnMaxLife, "maximum lifetime treated as a restart-loop process; zero counts every repeated start")
	flagset.IntVar(&config.RecorderBuffer, "recorder-buffer", config.RecorderBuffer, "number of events buffered before recording applies backpressure")
	flagset.IntVar(&config.SQLiteBatch, "sqlite-batch", config.SQLiteBatch, "maximum SQLite events written in one transaction")
	flagset.DurationVar(&config.SQLiteFlush, "sqlite-flush", config.SQLiteFlush, "maximum delay before a partial SQLite transaction is written")
	flagset.DurationVar(&config.Retention, "retention", 0, "delete SQLite events older than this duration; zero keeps all events")
	flagset.Int64Var(&config.RotateSize, "rotate-size", 0, "rotate text, JSONL, or CSV output after this many bytes; zero disables size rotation")
	flagset.DurationVar(&config.RotateInterval, "rotate-interval", 0, "rotate text, JSONL, or CSV output after this duration; zero disables time rotation")
	flagset.Var(&redact, "redact", "replace this exact text with [REDACTED] in recorded fields; may be repeated and defaults to no redaction")
	flagset.IntVar(&config.RingBuffer, "ring-buffer", 0, "retain this many pre-trigger events and write them only when the trigger fires; zero disables triggered recording")
	flagset.StringVar(&config.Trigger, "trigger", config.Trigger, "ring-buffer trigger: churn, gap, failed-exit, or regex:EXPRESSION")
	flagset.DurationVar(&config.PostTrigger, "post-trigger", config.PostTrigger, "recording time retained after a ring-buffer trigger fires")
	flagset.BoolVar(&config.RunSelfTest, "test", false, "run built-in checks for filtering, recorders, churn detection, and snapshot diffing")
	flagset.BoolVar(&config.ShowVersion, "version", false, "print the bottom version and exit")
	flagset.Usage = func() {
		fmt.Fprintf(flagset.Output(), "Usage: bottom [options]\n\n")
		fmt.Fprintf(flagset.Output(), "bottom records process start, exec, stop, churn, and capture-gap events. With no options it prints text to stdout.\n\n")
		flagset.PrintDefaults()
	}
	if err := flagset.Parse(args); err != nil {
		return Config{}, err
	}
	if flagset.NArg() != 0 {
		return Config{}, fmt.Errorf("expected options only, received positional arguments %q", strings.Join(flagset.Args(), " "))
	}
	config.Filter.Include = []string(include)
	config.Filter.Exclude = []string(exclude)
	config.Filter.IncludeRegex = []string(includeRegex)
	config.Filter.ExcludeRegex = []string(excludeRegex)
	config.Redact = []string(redact)
	config.Format = OutputFormat(format)
	if !validBackendName(config.Backend) {
		return Config{}, fmt.Errorf("backend must be auto, poll, or linux-proc-connector, received %q", config.Backend)
	}
	if config.PollInterval <= 0 {
		return Config{}, fmt.Errorf("poll interval must be positive, received %s", config.PollInterval)
	}
	if config.ChurnWindow <= 0 {
		return Config{}, fmt.Errorf("churn window must be positive, received %s", config.ChurnWindow)
	}
	if config.ChurnThreshold <= 0 {
		return Config{}, fmt.Errorf("churn threshold must be positive, received %d", config.ChurnThreshold)
	}
	if config.ChurnCooldown < 0 {
		return Config{}, fmt.Errorf("churn cooldown must not be negative, received %s", config.ChurnCooldown)
	}
	if config.ChurnMaxKeys <= 0 {
		return Config{}, fmt.Errorf("churn max keys must be positive, received %d", config.ChurnMaxKeys)
	}
	if config.ChurnMaxLife < 0 {
		return Config{}, fmt.Errorf("churn max life must not be negative, received %s", config.ChurnMaxLife)
	}
	if config.RecorderBuffer <= 0 {
		return Config{}, fmt.Errorf("recorder buffer must be positive, received %d", config.RecorderBuffer)
	}
	if config.SQLiteBatch <= 0 {
		return Config{}, fmt.Errorf("sqlite batch must be positive, received %d", config.SQLiteBatch)
	}
	if config.SQLiteFlush <= 0 {
		return Config{}, fmt.Errorf("sqlite flush interval must be positive, received %s", config.SQLiteFlush)
	}
	if config.Retention < 0 {
		return Config{}, fmt.Errorf("retention must not be negative, received %s", config.Retention)
	}
	if config.RotateSize < 0 {
		return Config{}, fmt.Errorf("rotate size must not be negative, received %d", config.RotateSize)
	}
	if config.RotateInterval < 0 {
		return Config{}, fmt.Errorf("rotate interval must not be negative, received %s", config.RotateInterval)
	}
	if config.RingBuffer < 0 {
		return Config{}, fmt.Errorf("ring buffer must not be negative, received %d", config.RingBuffer)
	}
	if config.PostTrigger < 0 {
		return Config{}, fmt.Errorf("post-trigger duration must not be negative, received %s", config.PostTrigger)
	}
	if _, err := newEventTrigger(config.Trigger); err != nil {
		return Config{}, err
	}
	if config.Filter.MinDuration < 0 {
		return Config{}, fmt.Errorf("minimum duration must not be negative, received %s", config.Filter.MinDuration)
	}
	if config.Filter.MaxDuration < 0 {
		return Config{}, fmt.Errorf("maximum duration must not be negative, received %s", config.Filter.MaxDuration)
	}
	if config.Filter.MaxDuration > 0 && config.Filter.MinDuration > config.Filter.MaxDuration {
		return Config{}, fmt.Errorf("minimum duration %s must not exceed maximum duration %s", config.Filter.MinDuration, config.Filter.MaxDuration)
	}
	for _, expression := range append(append([]string{}, config.Filter.IncludeRegex...), config.Filter.ExcludeRegex...) {
		if _, err := regexp.Compile(expression); err != nil {
			return Config{}, fmt.Errorf("compile event filter regular expression %q: %w", expression, err)
		}
	}
	if !validEventMode(config.Filter.EventMode) {
		return Config{}, fmt.Errorf("events must be start, exec, stop, churn, gap, all, or both, received %q", config.Filter.EventMode)
	}
	if !validOutputFormat(config.Format) {
		return Config{}, fmt.Errorf("format must be text, jsonl, csv, or sqlite, received %q", config.Format)
	}
	if config.Format == FormatSQLite && (config.RotateSize > 0 || config.RotateInterval > 0) {
		return Config{}, fmt.Errorf("output rotation supports text, jsonl, and csv, received format %q", config.Format)
	}
	if config.Retention > 0 && config.Format != FormatSQLite {
		return Config{}, fmt.Errorf("retention requires sqlite output, received format %q", config.Format)
	}
	if config.Format == FormatSQLite && config.OutputPath == "" {
		config.OutputPath = "bottom.sqlite"
	}
	if config.TUI && config.OutputPath == "" && config.Format != FormatText {
		return Config{}, fmt.Errorf("output path is required to combine tui with %s recording", config.Format)
	}
	if (config.RotateSize > 0 || config.RotateInterval > 0) && config.OutputPath == "" {
		return Config{}, fmt.Errorf("output path is required when output rotation is enabled")
	}
	if config.RingBuffer > 0 && config.OutputPath == "" {
		return Config{}, fmt.Errorf("output path is required when triggered ring-buffer recording is enabled")
	}
	if config.RingBuffer == 0 && (config.Trigger != "churn" || config.PostTrigger != 10*time.Second) {
		return Config{}, fmt.Errorf("ring buffer must be positive when trigger or post-trigger differs from its default")
	}
	return config, nil
}

func run(config Config, logger *log.Logger) (runErr error) {
	if config.RunSelfTest {
		return runSelfTest()
	}
	if config.ShowVersion {
		fmt.Println(versionLine())
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), notifiedSignals()...)
	defer stop()
	return runWithContext(ctx, config, logger)
}

func runWithContext(ctx context.Context, config Config, logger *log.Logger) (runErr error) {
	backend, fallbackAllowed, err := selectBackend(config)
	selectionErr := err
	if err != nil {
		if fallbackAllowed {
			backend = NewPollingBackend(config.PollInterval)
		} else {
			return err
		}
	}
	recorderConfig := config
	recorderConfig.Backend = backend.Name()
	recorder, err := newRecorder(recorderConfig)
	if err != nil {
		return err
	}
	defer func() {
		if err := recorder.Close(); runErr == nil && err != nil {
			runErr = err
		}
	}()
	if selectionErr != nil {
		logBackendFallback(logger, config.Backend, selectionErr)
		gap := Event{Kind: EventGap, Time: time.Now(), Backend: backend.Name(), Message: fmt.Sprintf("backend %s was unavailable and bottom started with %s: %v", config.Backend, BackendPoll, selectionErr)}
		if writeErr := recorder.Write(gap); writeErr != nil {
			return writeErr
		}
	}
	churn := NewConfiguredChurnDetector(config)
	events := make(chan Event, 256)
	backendErrors := make(chan error, 1)
	startBackend(ctx, backend, events, backendErrors)
	for {
		select {
		case <-ctx.Done():
			return nil
		case event := <-events:
			if event.Kind == EventGap {
				logBackendDiagnostic(logger, event)
			}
			if err := recorder.Write(event); err != nil {
				return err
			}
			if churnEvent, ok := churn.Observe(event); ok {
				if err := recorder.Write(churnEvent); err != nil {
					return err
				}
			}
		case err := <-backendErrors:
			if err == nil {
				return nil
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			if fallbackAllowed && backend.Name() != BackendPoll {
				logBackendFallback(logger, backend.Name(), err)
				gap := Event{Kind: EventGap, Time: time.Now(), Backend: backend.Name(), Message: fmt.Sprintf("backend %s failed and bottom switched to %s: %v", backend.Name(), BackendPoll, err)}
				if writeErr := recorder.Write(gap); writeErr != nil {
					return writeErr
				}
				backend = NewPollingBackend(config.PollInterval)
				fallbackAllowed = false
				startBackend(ctx, backend, events, backendErrors)
			} else {
				return err
			}
		}
	}
}

func startBackend(ctx context.Context, backend LifecycleBackend, events chan<- Event, errors chan<- error) {
	go func() {
		errors <- backend.Watch(ctx, events)
	}()
}

func main() {
	logger := log.New(os.Stderr, "bottom: ", log.LstdFlags)
	args := os.Args[1:]
	command := "record"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command = args[0]
		args = args[1:]
	}
	if command == "version" {
		fmt.Println(versionLine())
		return
	}
	if command == "completion" {
		if err := runCompletion(args, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
			os.Exit(2)
		}
		return
	}
	if command == "trace" {
		config, err := parseTraceConfig(args)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
			os.Exit(2)
		}
		ctx, stop := signal.NotifyContext(context.Background(), notifiedSignals()...)
		runErr := runTrace(ctx, config)
		stop()
		if errors.Is(runErr, context.Canceled) {
			runErr = nil
		}
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "bottom: %v\n", runErr)
			os.Exit(1)
		}
		return
	}
	if command == "record" || command == "watch" {
		config, err := parseConfig(args)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
			os.Exit(2)
		}
		if err := run(config, logger); err != nil {
			fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if command == "compare" {
		config, err := parseRecordingCompareConfig(args)
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return
			}
			fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
			os.Exit(2)
		}
		if err := runRecordingCompare(config); err != nil {
			fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if command != "query" && command != "replay" && command != "report" {
		fmt.Fprintf(os.Stderr, "bottom: command must be record, watch, trace, query, replay, report, compare, version, or completion, received %q\n", command)
		os.Exit(2)
	}
	config, err := parseRecordingReadConfig(command, args, time.Now())
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
		os.Exit(2)
	}
	var runErr error
	switch command {
	case "query":
		runErr = runRecordingQuery(config)
	case "report":
		runErr = runRecordingReport(config)
	case "replay":
		ctx, stop := signal.NotifyContext(context.Background(), notifiedSignals()...)
		runErr = runRecordingReplay(ctx, config)
		stop()
		if errors.Is(runErr, context.Canceled) {
			runErr = nil
		}
	}
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "bottom: %v\n", runErr)
		os.Exit(1)
	}
}

func versionLine() string {
	resolvedVersion, resolvedCommit := sourceBuildIdentity()
	return fmt.Sprintf("bottom %s commit=%s built=%s", resolvedVersion, resolvedCommit, buildDate)
}

func sourceBuildIdentity() (string, string) {
	resolvedVersion := version
	resolvedCommit := commit
	commitInjected := resolvedCommit != "unknown"
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return resolvedVersion, resolvedCommit
	}
	if resolvedVersion == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		resolvedVersion = info.Main.Version
	}
	modified := false
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			if resolvedCommit == "unknown" && setting.Value != "" {
				resolvedCommit = setting.Value
			}
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}
	if modified && !commitInjected && resolvedCommit != "unknown" {
		resolvedCommit += "+modified"
	}
	return resolvedVersion, resolvedCommit
}
