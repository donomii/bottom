package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

type traceCommandResult struct {
	finishedAt time.Time
	exitCode   *int
	err        error
}

type perfettoTrace struct {
	Events []perfettoEvent `json:"traceEvents"`
}

type perfettoEvent struct {
	Name      string            `json:"name"`
	Category  string            `json:"cat"`
	Phase     string            `json:"ph"`
	Timestamp int64             `json:"ts"`
	PID       int               `json:"pid"`
	TID       int               `json:"tid"`
	Scope     string            `json:"s,omitempty"`
	Args      map[string]string `json:"args,omitempty"`
}

func parseTraceConfig(args []string) (TraceConfig, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	config := TraceConfig{
		Recorder: Config{
			Backend:        BackendTrace,
			Format:         FormatJSONL,
			OutputPath:     "",
			PollInterval:   10 * time.Millisecond,
			RecorderBuffer: 1024,
			Filter:         Filter{EventMode: EventModeAll},
		},
		Tail: 2 * time.Second,
	}
	optionArgs := args
	if separator >= 0 {
		optionArgs = args[:separator]
		config.Command = append([]string(nil), args[separator+1:]...)
	}
	var redact listFlag
	format := string(config.Recorder.Format)
	flagset := flag.NewFlagSet("bottom trace", flag.ContinueOnError)
	flagset.StringVar(&format, "format", format, "recording format: text, jsonl, or csv")
	flagset.StringVar(&config.Recorder.OutputPath, "output", config.Recorder.OutputPath, "recording path; empty selects bottom-trace with the format extension")
	flagset.DurationVar(&config.Recorder.PollInterval, "poll", config.Recorder.PollInterval, "descendant snapshot interval")
	flagset.DurationVar(&config.Tail, "tail", config.Tail, "maximum time to observe surviving descendants after the command exits")
	flagset.StringVar(&config.PerfettoPath, "perfetto", "", "write a Perfetto-compatible JSON timeline to this new file; empty disables export")
	flagset.BoolVar(&config.Recorder.TUI, "tui", false, "unsupported for trace because the command shares the terminal; replay the recording with tui")
	flagset.IntVar(&config.Recorder.RecorderBuffer, "recorder-buffer", config.Recorder.RecorderBuffer, "number of trace events buffered before recording applies backpressure")
	flagset.Var(&redact, "redact", "replace this exact text with [REDACTED] in recorded fields; may be repeated and defaults to no redaction")
	if err := flagset.Parse(optionArgs); err != nil {
		return TraceConfig{}, err
	}
	if separator < 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace requires -- before the command")
	}
	if flagset.NArg() != 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace expected options before --, received positional arguments %q", strings.Join(flagset.Args(), " "))
	}
	config.Recorder.Format = OutputFormat(format)
	config.Recorder.Redact = []string(redact)
	if len(config.Command) == 0 || config.Command[0] == "" {
		return TraceConfig{}, fmt.Errorf("bottom trace expected a command after --")
	}
	if !validOutputFormat(config.Recorder.Format) {
		return TraceConfig{}, fmt.Errorf("bottom trace format must be text, jsonl, or csv, received %q", config.Recorder.Format)
	}
	if config.Recorder.OutputPath == "" {
		switch config.Recorder.Format {
		case FormatText:
			config.Recorder.OutputPath = "bottom-trace.log"
		case FormatJSONL:
			config.Recorder.OutputPath = "bottom-trace.jsonl"
		case FormatCSV:
			config.Recorder.OutputPath = "bottom-trace.csv"
		}
	}
	if err := validateTraceOutputConfiguration(config); err != nil {
		return TraceConfig{}, err
	}
	if config.Recorder.PollInterval <= 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace poll interval must be positive, received %s", config.Recorder.PollInterval)
	}
	if config.Tail < 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace tail must not be negative, received %s", config.Tail)
	}
	if config.Recorder.RecorderBuffer <= 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace recorder buffer must be positive, received %d", config.Recorder.RecorderBuffer)
	}
	return config, nil
}

func validateTraceOutputConfiguration(config TraceConfig) error {
	if config.Recorder.TUI {
		return fmt.Errorf("bottom trace does not support tui because the traced command shares the terminal; record to a file and replay it with tui")
	}
	return validateDistinctTracePaths(config.Recorder.OutputPath, config.PerfettoPath)
}

func validateDistinctTracePaths(outputPath string, perfettoPath string) error {
	if perfettoPath == "" {
		return nil
	}
	output, err := canonicalTracePath(outputPath)
	if err != nil {
		return fmt.Errorf("resolve bottom trace output path %q: %w", outputPath, err)
	}
	perfetto, err := canonicalTracePath(perfettoPath)
	if err != nil {
		return fmt.Errorf("resolve bottom trace Perfetto path %q: %w", perfettoPath, err)
	}
	if sameTracePath(output, perfetto) {
		return fmt.Errorf("bottom trace output path %q and Perfetto path %q must refer to different files", outputPath, perfettoPath)
	}
	outputInfo, outputErr := os.Stat(output)
	perfettoInfo, perfettoErr := os.Stat(perfetto)
	if outputErr != nil && !os.IsNotExist(outputErr) {
		return fmt.Errorf("inspect bottom trace output path %q: %w", outputPath, outputErr)
	}
	if perfettoErr != nil && !os.IsNotExist(perfettoErr) {
		return fmt.Errorf("inspect bottom trace Perfetto path %q: %w", perfettoPath, perfettoErr)
	}
	if outputErr == nil && perfettoErr == nil && os.SameFile(outputInfo, perfettoInfo) {
		return fmt.Errorf("bottom trace output path %q and Perfetto path %q must refer to different files", outputPath, perfettoPath)
	}
	return nil
}

func canonicalTracePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	missingParts := []string{}
	candidate := absolute
	for {
		resolved, resolveErr := filepath.EvalSymlinks(candidate)
		if resolveErr == nil {
			for index := len(missingParts) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missingParts[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", resolveErr
		}
		info, linkErr := os.Lstat(candidate)
		if linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
			target, readErr := os.Readlink(candidate)
			if readErr != nil {
				return "", readErr
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(filepath.Dir(candidate), target)
			}
			for index := len(missingParts) - 1; index >= 0; index-- {
				target = filepath.Join(target, missingParts[index])
			}
			return canonicalTracePath(target)
		}
		if linkErr != nil && !os.IsNotExist(linkErr) {
			return "", linkErr
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return filepath.Clean(absolute), nil
		}
		missingParts = append(missingParts, filepath.Base(candidate))
		candidate = parent
	}
}

func sameTracePath(first string, second string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(first, second)
	}
	return first == second
}

func runTrace(ctx context.Context, config TraceConfig) (runErr error) {
	if err := validateTraceOutputConfiguration(config); err != nil {
		return err
	}
	_, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot before tracing %q: %w", config.Command[0], err)
	}
	recorder, err := newRecorder(config.Recorder)
	if err != nil {
		return err
	}
	var captured []Event
	var captureTarget *[]Event
	if config.PerfettoPath != "" {
		captured = []Event{}
		captureTarget = &captured
	}
	var commandResults chan traceCommandResult
	rootReaped := true
	defer func() {
		runErr = reapTraceRoot(commandResults, rootReaped, runErr, config.Command)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			runErr = nil
		}
		closeErr := recorder.Close()
		exportErr := writePerfettoTrace(config.PerfettoPath, captured, config.Recorder.Redact)
		runErr = joinRecorderErrors(runErr, closeErr, exportErr)
	}()
	command := exec.Command(config.Command[0], config.Command[1:]...)
	command.Stdin = os.Stdin
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	startedAt := time.Now()
	if err := command.Start(); err != nil {
		return fmt.Errorf("start traced command %q: %w", strings.Join(config.Command, " "), err)
	}
	commandResults = make(chan traceCommandResult, 1)
	rootReaped = false
	go waitForTracedCommand(command, commandResults)
	rootPID := command.Process.Pid
	root := capturedProcess("trace:"+strconv.Itoa(rootPID)+":"+strconv.FormatInt(startedAt.UnixNano(), 10), rootPID, os.Getpid(), strings.Join(config.Command, " "), config.Command[0], "", "", startedAt, startedAt)
	observed := map[int]Process{rootPID: root}
	tracked := map[int]bool{rootPID: true}
	startEvent := processStartEventObserved(startedAt, startedAt, BackendTrace, root, ProcessSnapshot{root.ID: root})
	if err := writeTraceEvent(recorder, captureTarget, startEvent); err != nil {
		return err
	}
	ticker := time.NewTicker(config.Recorder.PollInterval)
	defer ticker.Stop()
	rootFinished := false
	var rootFinishedAt time.Time
	var commandErr error
	for {
		select {
		case <-ctx.Done():
			if err := recordTraceCancellation(recorder, captureTarget, time.Now()); err != nil {
				return err
			}
			return ctx.Err()
		case result := <-commandResults:
			rootReaped = true
			rootFinished = true
			rootFinishedAt = result.finishedAt
			commandErr = result.err
			if proc, found := observed[rootPID]; found {
				event := processStopEventObserved(result.finishedAt, result.finishedAt, BackendTrace, proc, snapshotFromProcesses(observed), result.exitCode)
				if err := writeTraceEvent(recorder, captureTarget, event); err != nil {
					return err
				}
				delete(observed, rootPID)
				delete(tracked, rootPID)
			}
		case <-ticker.C:
			next, err := ReadProcessSnapshot()
			if err != nil {
				now := time.Now()
				if writeErr := writeTraceEvent(recorder, captureTarget, Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendTrace, Message: fmt.Sprintf("trace snapshot failed; expected a complete process table, received error %v", err)}); writeErr != nil {
					return writeErr
				}
				continue
			}
			if err := updateTracedProcesses(recorder, captureTarget, next, observed, tracked, rootPID, !rootFinished); err != nil {
				return err
			}
			if rootFinished && traceFinished(rootFinishedAt, config, observed) {
				if len(observed) > 0 {
					now := time.Now()
					message := fmt.Sprintf("trace tail ended with observed descendant process ids %v still running", sortedProcessIDs(observed))
					if err := writeTraceEvent(recorder, captureTarget, Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendTrace, Message: message}); err != nil {
						return err
					}
				}
				if commandErr != nil {
					return fmt.Errorf("traced command %q completed unsuccessfully: %w", strings.Join(config.Command, " "), commandErr)
				}
				return nil
			}
		}
	}
}

func recordTraceCancellation(recorder Recorder, captured *[]Event, now time.Time) error {
	event := Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendTrace, Message: "trace stopped before the command and all observed descendants completed"}
	if err := writeTraceEvent(recorder, captured, event); err != nil {
		return fmt.Errorf("record trace cancellation gap before returning: %w", err)
	}
	return nil
}

func reapTraceRoot(results <-chan traceCommandResult, alreadyReaped bool, runErr error, command []string) error {
	if alreadyReaped || results == nil {
		return runErr
	}
	result := <-results
	if result.err == nil || errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return runErr
	}
	commandErr := fmt.Errorf("traced command %q completed unsuccessfully while bottom was finishing: %w", strings.Join(command, " "), result.err)
	return joinRecorderErrors(runErr, commandErr)
}

func waitForTracedCommand(command *exec.Cmd, results chan<- traceCommandResult) {
	err := command.Wait()
	result := traceCommandResult{finishedAt: time.Now(), err: err}
	if command.ProcessState != nil {
		exitCode := command.ProcessState.ExitCode()
		result.exitCode = &exitCode
	}
	results <- result
}

func updateTracedProcesses(recorder Recorder, captured *[]Event, snapshot ProcessSnapshot, observed map[int]Process, tracked map[int]bool, rootPID int, rootActive bool) error {
	now := time.Now()
	selected := selectTracedProcesses(snapshot, tracked, rootPID, rootActive)
	for _, pid := range sortedProcessIDs(selected) {
		proc := selected[pid]
		previous, found := observed[pid]
		if !found {
			tracked[pid] = true
			observed[pid] = proc
			if err := writeTraceEvent(recorder, captured, processStartEvent(now, BackendTrace, proc, snapshot)); err != nil {
				return err
			}
			continue
		}
		if !sameProcessGeneration(previous, proc) {
			if err := writeTraceEvent(recorder, captured, processStopEvent(now, BackendTrace, previous, snapshotFromProcesses(observed), nil)); err != nil {
				return err
			}
			observed[pid] = proc
			if err := writeTraceEvent(recorder, captured, processStartEvent(now, BackendTrace, proc, snapshot)); err != nil {
				return err
			}
			continue
		}
		preserveProcessObservation(previous, &proc)
		observed[pid] = proc
		if previous.Command != proc.Command || previous.Exe != proc.Exe {
			if err := writeTraceEvent(recorder, captured, processExecEvent(now, now, BackendTrace, proc, snapshot)); err != nil {
				return err
			}
		}
	}
	for _, pid := range sortedProcessIDs(observed) {
		if _, found := selected[pid]; found || pid == rootPID && rootActive {
			continue
		}
		proc := observed[pid]
		if err := writeTraceEvent(recorder, captured, processStopEvent(now, BackendTrace, proc, snapshotFromProcesses(observed), nil)); err != nil {
			return err
		}
		delete(observed, pid)
		delete(tracked, pid)
	}
	return nil
}

func selectTracedProcesses(snapshot ProcessSnapshot, tracked map[int]bool, rootPID int, rootActive bool) map[int]Process {
	byPID := indexProcessesByPID(snapshot)
	selected := map[int]Process{}
	if rootActive {
		if root, found := byPID[rootPID]; found {
			selected[rootPID] = root
		}
	}
	for pid := range tracked {
		if proc, found := byPID[pid]; found {
			selected[pid] = proc
		}
	}
	for {
		added := false
		for pid, proc := range byPID {
			if _, found := selected[pid]; found {
				continue
			}
			if _, parentSelected := selected[proc.ParentPID]; parentSelected {
				selected[pid] = proc
				added = true
			}
		}
		if !added {
			return selected
		}
	}
}

func traceFinished(rootFinishedAt time.Time, config TraceConfig, observed map[int]Process) bool {
	if len(observed) == 0 && time.Since(rootFinishedAt) >= 2*config.Recorder.PollInterval {
		return true
	}
	return config.Tail == 0 || time.Since(rootFinishedAt) >= config.Tail
}

func writeTraceEvent(recorder Recorder, captured *[]Event, event Event) error {
	if err := recorder.Write(event); err != nil {
		return err
	}
	if captured != nil {
		*captured = append(*captured, event)
	}
	return nil
}

func snapshotFromProcesses(processes map[int]Process) ProcessSnapshot {
	snapshot := ProcessSnapshot{}
	for _, proc := range processes {
		snapshot[proc.ID] = proc
	}
	return snapshot
}

func sortedProcessIDs[T Process | bool](processes map[int]T) []int {
	ids := make([]int, 0, len(processes))
	for pid := range processes {
		ids = append(ids, pid)
	}
	sort.Ints(ids)
	return ids
}

func writePerfettoTrace(path string, events []Event, redact []string) error {
	if path == "" {
		return nil
	}
	trace := perfettoTrace{Events: make([]perfettoEvent, 0, len(events))}
	for _, event := range events {
		event = redactEvent(event, redact)
		phase := "i"
		scope := "t"
		if event.Kind == EventStart {
			phase = "B"
			scope = ""
		} else if event.Kind == EventStop {
			phase = "E"
			scope = ""
		}
		trace.Events = append(trace.Events, perfettoEvent{
			Name:      string(event.Kind) + " " + event.Command,
			Category:  "process-lifecycle",
			Phase:     phase,
			Timestamp: event.Time.UnixMicro(),
			PID:       event.PID,
			TID:       event.PID,
			Scope:     scope,
			Args: map[string]string{
				"process_id": event.ProcessID,
				"exe":        event.Exe,
				"parent_pid": strconv.Itoa(event.ParentPID),
				"backend":    event.Backend,
				"message":    event.Message,
			},
		})
	}
	encoded, err := json.MarshalIndent(trace, "", "  ")
	if err != nil {
		return fmt.Errorf("encode Perfetto trace %q: %w", path, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create new owner-only Perfetto trace %q: %w", path, err)
	}
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		closeErr := file.Close()
		return joinRecorderErrors(fmt.Errorf("write Perfetto trace %q: %w", path, err), closeErr)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Perfetto trace %q: %w", path, err)
	}
	return nil
}
