package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
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
		PollInterval:   time.Second,
		ChurnWindow:    10 * time.Second,
		ChurnThreshold: 5,
		Filter: Filter{
			EventMode: EventModeBoth,
		},
	}
	var include listFlag
	var exclude listFlag
	format := string(config.Format)
	flagset := flag.NewFlagSet("bottom", flag.ContinueOnError)
	flagset.StringVar(&config.Backend, "backend", config.Backend, "process source: auto, poll, linux-proc-connector, linux-ebpf, windows-wmi, macos-endpoint-security")
	flagset.Var(&include, "include", "show events whose command, executable path, current directory, user, or parent chain contains this text; may be repeated")
	flagset.Var(&exclude, "exclude", "hide events whose command, executable path, current directory, user, or parent chain contains this text; may be repeated")
	flagset.StringVar(&config.Filter.User, "user", "", "show events owned by this user name or numeric id")
	flagset.StringVar(&config.Filter.CwdContains, "cwd", "", "show events whose current directory contains this text")
	flagset.StringVar(&config.Filter.ExeContains, "exe", "", "show events whose executable path contains this text")
	flagset.IntVar(&config.Filter.ParentPID, "ppid", 0, "show events whose immediate parent process has this pid")
	flagset.StringVar(&config.Filter.EventMode, "events", config.Filter.EventMode, "event kinds to show: start, stop, churn, gap, or both")
	flagset.DurationVar(&config.Filter.MinDuration, "min-duration", 0, "show stop events only when the process lived at least this long")
	flagset.DurationVar(&config.Filter.MaxDuration, "max-duration", 0, "show stop events only when the process lived no longer than this")
	flagset.DurationVar(&config.PollInterval, "poll", config.PollInterval, "polling interval used by the polling backend and fallback mode")
	flagset.StringVar(&format, "format", format, "output format: text, jsonl, csv, or sqlite")
	flagset.StringVar(&config.OutputPath, "output", config.OutputPath, "output file path for csv, jsonl, or sqlite; empty writes text, csv, and jsonl to stdout")
	flagset.BoolVar(&config.TUI, "tui", false, "show a live terminal timeline with recent events and process churn counts")
	flagset.DurationVar(&config.ChurnWindow, "churn-window", config.ChurnWindow, "time window used to group repeated short-lived command starts")
	flagset.IntVar(&config.ChurnThreshold, "churn-threshold", config.ChurnThreshold, "number of starts inside the churn window before bottom reports a churn event")
	flagset.BoolVar(&config.RunSelfTest, "test", false, "run built-in checks for filtering, recorders, churn detection, and snapshot diffing")
	flagset.Usage = func() {
		fmt.Fprintf(flagset.Output(), "Usage: bottom [options]\n\n")
		fmt.Fprintf(flagset.Output(), "bottom records process start and stop events. With no options it prints text to stdout.\n\n")
		flagset.PrintDefaults()
	}
	if err := flagset.Parse(args); err != nil {
		return Config{}, err
	}
	config.Filter.Include = []string(include)
	config.Filter.Exclude = []string(exclude)
	config.Format = OutputFormat(format)
	if config.PollInterval <= 0 {
		return Config{}, fmt.Errorf("poll interval must be positive, received %s", config.PollInterval)
	}
	if config.ChurnWindow <= 0 {
		return Config{}, fmt.Errorf("churn window must be positive, received %s", config.ChurnWindow)
	}
	if config.ChurnThreshold <= 0 {
		return Config{}, fmt.Errorf("churn threshold must be positive, received %d", config.ChurnThreshold)
	}
	if !validEventMode(config.Filter.EventMode) {
		return Config{}, fmt.Errorf("events must be start, stop, churn, gap, or both, received %q", config.Filter.EventMode)
	}
	if !validOutputFormat(config.Format) {
		return Config{}, fmt.Errorf("format must be text, jsonl, csv, or sqlite, received %q", config.Format)
	}
	if config.Format == FormatSQLite && config.OutputPath == "" {
		config.OutputPath = "bottom.sqlite"
	}
	return config, nil
}

func run(config Config, logger *log.Logger) (runErr error) {
	if config.RunSelfTest {
		return runSelfTest()
	}
	recorder, err := newRecorder(config)
	if err != nil {
		return err
	}
	defer func() {
		if err := recorder.Close(); runErr == nil && err != nil {
			runErr = err
		}
	}()
	backend, fallbackAllowed, err := selectBackend(config)
	if err != nil {
		if fallbackAllowed {
			logBackendFallback(logger, config.Backend, err)
			backend = NewPollingBackend(config.PollInterval)
		} else {
			return err
		}
	}
	churn := NewChurnDetector(config.ChurnWindow, config.ChurnThreshold)
	events := make(chan Event, 256)
	errors := make(chan error, 1)
	ctx := context.Background()
	startBackend(ctx, backend, events, errors)
	for {
		select {
		case event := <-events:
			if config.Filter.Accepts(event) {
				if err := recorder.Write(event); err != nil {
					return err
				}
			}
			if churnEvent, ok := churn.Observe(event); ok && config.Filter.Accepts(churnEvent) {
				if err := recorder.Write(churnEvent); err != nil {
					return err
				}
			}
		case err := <-errors:
			if err == nil {
				return nil
			}
			if fallbackAllowed && backend.Name() != BackendPoll {
				logBackendFallback(logger, backend.Name(), err)
				backend = NewPollingBackend(config.PollInterval)
				fallbackAllowed = false
				startBackend(ctx, backend, events, errors)
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
	config, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
		os.Exit(2)
	}
	logger := log.New(os.Stderr, "bottom: ", log.LstdFlags)
	if err := run(config, logger); err != nil {
		fmt.Fprintf(os.Stderr, "bottom: %v\n", err)
		os.Exit(1)
	}
}
