package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
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

func parseTraceConfig(args []string) (TraceConfig, error) {
	separator := -1
	for index, argument := range args {
		if argument == "--" {
			separator = index
			break
		}
	}
	config := TraceConfig{
		PollInterval: 10 * time.Millisecond,
		ShowPPID:     false,
		Tail:         2 * time.Second,
	}
	optionArgs := args
	if separator >= 0 {
		optionArgs = args[:separator]
		config.Command = append([]string(nil), args[separator+1:]...)
	}
	flagset := flag.NewFlagSet("bottom trace", flag.ContinueOnError)
	flagset.DurationVar(&config.PollInterval, "poll", config.PollInterval, "descendant snapshot interval")
	flagset.BoolVar(&config.ShowPPID, "ppid", config.ShowPPID, "include the parent PID in readable event lines")
	flagset.DurationVar(&config.Tail, "tail", config.Tail, "maximum time to observe surviving descendants after the command exits")
	if err := flagset.Parse(optionArgs); err != nil {
		return TraceConfig{}, err
	}
	if separator < 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace requires -- before the command")
	}
	if flagset.NArg() != 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace expected options before --, received positional arguments %q", strings.Join(flagset.Args(), " "))
	}
	if len(config.Command) == 0 || config.Command[0] == "" {
		return TraceConfig{}, fmt.Errorf("bottom trace expected a command after --")
	}
	if config.PollInterval <= 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace poll interval must be positive, received %s", config.PollInterval)
	}
	if config.Tail < 0 {
		return TraceConfig{}, fmt.Errorf("bottom trace tail must not be negative, received %s", config.Tail)
	}
	return config, nil
}

func runTrace(ctx context.Context, config TraceConfig) (runErr error) {
	_, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot before tracing %q: %w", config.Command[0], err)
	}
	writeEvent := func(event Event) error { return writeEventLog(os.Stdout, event, config.ShowPPID) }
	var commandResults chan traceCommandResult
	rootReaped := true
	defer func() {
		runErr = reapTraceRoot(commandResults, rootReaped, runErr, config.Command)
		if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
			runErr = nil
		}
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
	if err := writeEvent(startEvent); err != nil {
		return err
	}
	ticker := time.NewTicker(config.PollInterval)
	defer ticker.Stop()
	rootFinished := false
	var rootFinishedAt time.Time
	var commandErr error
	for {
		select {
		case <-ctx.Done():
			if err := recordTraceCancellation(writeEvent, time.Now()); err != nil {
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
				if err := writeEvent(event); err != nil {
					return err
				}
				delete(observed, rootPID)
				delete(tracked, rootPID)
			}
		case <-ticker.C:
			next, err := ReadProcessSnapshot()
			if err != nil {
				now := time.Now()
				if writeErr := writeEvent(Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendTrace, Message: fmt.Sprintf("trace snapshot failed; expected a complete process table, received error %v", err)}); writeErr != nil {
					return writeErr
				}
				continue
			}
			if err := updateTracedProcesses(writeEvent, next, observed, tracked, rootPID, !rootFinished); err != nil {
				return err
			}
			if rootFinished && traceFinished(rootFinishedAt, config, observed) {
				if len(observed) > 0 {
					now := time.Now()
					message := fmt.Sprintf("trace tail ended with observed descendant process ids %v still running", sortedProcessIDs(observed))
					if err := writeEvent(Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendTrace, Message: message}); err != nil {
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

func recordTraceCancellation(writeEvent func(Event) error, now time.Time) error {
	event := Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendTrace, Message: "trace stopped before the command and all observed descendants completed"}
	if err := writeEvent(event); err != nil {
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
	return errors.Join(runErr, commandErr)
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

func updateTracedProcesses(writeEvent func(Event) error, snapshot ProcessSnapshot, observed map[int]Process, tracked map[int]bool, rootPID int, rootActive bool) error {
	now := time.Now()
	selected := selectTracedProcesses(snapshot, tracked, rootPID, rootActive)
	for _, pid := range sortedProcessIDs(selected) {
		proc := selected[pid]
		previous, found := observed[pid]
		if !found {
			tracked[pid] = true
			observed[pid] = proc
			if err := writeEvent(processStartEvent(now, BackendTrace, proc, snapshot)); err != nil {
				return err
			}
			continue
		}
		if !sameProcessGeneration(previous, proc) {
			if err := writeEvent(processStopEvent(now, BackendTrace, previous, snapshotFromProcesses(observed), nil)); err != nil {
				return err
			}
			observed[pid] = proc
			if err := writeEvent(processStartEvent(now, BackendTrace, proc, snapshot)); err != nil {
				return err
			}
			continue
		}
		preserveProcessObservation(previous, &proc)
		observed[pid] = proc
		if previous.Command != proc.Command || previous.Exe != proc.Exe {
			if err := writeEvent(processExecEvent(now, now, BackendTrace, proc, snapshot)); err != nil {
				return err
			}
		}
	}
	for _, pid := range sortedProcessIDs(observed) {
		if _, found := selected[pid]; found || pid == rootPID && rootActive {
			continue
		}
		proc := observed[pid]
		if err := writeEvent(processStopEvent(now, BackendTrace, proc, snapshotFromProcesses(observed), nil)); err != nil {
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
	if len(observed) == 0 && time.Since(rootFinishedAt) >= 2*config.PollInterval {
		return true
	}
	return config.Tail == 0 || time.Since(rootFinishedAt) >= config.Tail
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
