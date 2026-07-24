package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"strconv"
	"strings"
	"time"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func parseConfig(args []string) (Config, error) {
	config := Config{
		Backend:       "auto",
		PollInterval:  100 * time.Millisecond,
		ShowParentExe: false,
		ShowPPID:      false,
	}
	parentExeSet := false
	flagset := flag.NewFlagSet("bottom", flag.ContinueOnError)
	flagset.StringVar(&config.Backend, "backend", config.Backend, "process source: auto, poll, linux-proc-connector, windows-etw, or macos-endpoint-security")
	flagset.BoolFunc("parent-exe", "include the parent executable name; enabled by default in the TUI and disabled by default in readable event lines", func(value string) error {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("parent-exe must be true or false, received %q", value)
		}
		config.ShowParentExe = enabled
		parentExeSet = true
		return nil
	})
	flagset.DurationVar(&config.PollInterval, "poll", config.PollInterval, "polling interval used by the polling backend and fallback mode")
	flagset.BoolVar(&config.ShowPPID, "ppid", config.ShowPPID, "include the parent PID in readable event lines")
	flagset.BoolVar(&config.TUI, "tui", false, "show the interactive terminal timeline instead of the readable event log")
	flagset.BoolVar(&config.ShowVersion, "version", false, "print the bottom version and exit")
	flagset.Usage = func() {
		fmt.Fprintf(flagset.Output(), "Usage: bottom [options]\n\n")
		fmt.Fprintf(flagset.Output(), "bottom watches process start, exec, stop, and capture-gap events. With no options it prints a concise process log.\n\n")
		flagset.PrintDefaults()
	}
	if err := flagset.Parse(args); err != nil {
		return Config{}, err
	}
	if flagset.NArg() != 0 {
		return Config{}, fmt.Errorf("expected options only, received positional arguments %q", strings.Join(flagset.Args(), " "))
	}
	if config.TUI && !parentExeSet {
		config.ShowParentExe = true
	}
	if !validBackendName(config.Backend) {
		return Config{}, fmt.Errorf("backend must be auto, poll, linux-proc-connector, windows-etw, or macos-endpoint-security, received %q", config.Backend)
	}
	if config.PollInterval <= 0 {
		return Config{}, fmt.Errorf("poll interval must be positive, received %s", config.PollInterval)
	}
	return config, nil
}

func run(config Config, logger *log.Logger) (runErr error) {
	if config.ShowVersion {
		fmt.Println(versionLine())
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), notifiedSignals()...)
	defer stop()
	return runWithContext(ctx, config, logger)
}

func runWithContext(ctx context.Context, config Config, logger *log.Logger) (runErr error) {
	runContext, stopRun := context.WithCancel(ctx)
	defer stopRun()
	backend, fallbackAllowed, err := selectBackend(config)
	selectionErr := err
	if err != nil {
		if fallbackAllowed {
			backend = NewPollingBackend(config.PollInterval)
		} else {
			return err
		}
	}
	var tui *TUI
	if config.TUI {
		tui = newTUI(os.Stdout, stopRun, config.ShowPPID, config.ShowParentExe)
		defer func() {
			if err := tui.Close(); runErr == nil && err != nil {
				runErr = err
			}
		}()
	} else {
		if err := writeWatchStarted(os.Stdout); err != nil {
			return err
		}
	}
	writeEvent := func(event Event) error {
		if tui != nil {
			return tui.Write(event)
		}
		return writeEventLog(os.Stdout, event, config.ShowPPID, config.ShowParentExe)
	}
	if selectionErr != nil {
		logBackendFallback(logger, config.Backend, selectionErr)
		gap := Event{Kind: EventGap, Time: time.Now(), Backend: backend.Name(), Message: fmt.Sprintf("backend %s was unavailable and bottom started with %s: %v", config.Backend, BackendPoll, selectionErr)}
		if writeErr := writeEvent(gap); writeErr != nil {
			return writeErr
		}
	}
	events := make(chan Event, 256)
	backendErrors := make(chan error, 1)
	startBackend(runContext, backend, events, backendErrors)
	for {
		select {
		case <-runContext.Done():
			return nil
		case event := <-events:
			if event.Kind == EventGap {
				logBackendDiagnostic(logger, event)
			}
			if err := writeEvent(event); err != nil {
				return err
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
				if writeErr := writeEvent(gap); writeErr != nil {
					return writeErr
				}
				backend = NewPollingBackend(config.PollInterval)
				fallbackAllowed = false
				startBackend(runContext, backend, events, backendErrors)
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
	command := "watch"
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
	if command == "watch" {
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
	fmt.Fprintf(os.Stderr, "bottom: command must be watch, trace, version, or completion, received %q\n", command)
	os.Exit(2)
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
