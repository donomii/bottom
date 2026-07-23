package main

import (
	"fmt"
	"io"
	"log"
)

func logBackendFallback(logger *log.Logger, backend string, err error) {
	logger.Printf("backend=%s fallback=%s reason=%v", backend, BackendPoll, err)
}

func logBackendDiagnostic(logger *log.Logger, event Event) {
	logger.Printf("backend=%s diagnostic=%s", event.Backend, event.Message)
}

func writeWatchStarted(writer io.Writer) error {
	if _, err := fmt.Fprintln(writer, "Starting process watch"); err != nil {
		return fmt.Errorf("write process watch start log: %w", err)
	}
	return nil
}

func writeEventLog(writer io.Writer, event Event, showPPID bool) error {
	if _, err := fmt.Fprintln(writer, formatEventLog(event, showPPID)); err != nil {
		return fmt.Errorf("write process event log: %w", err)
	}
	return nil
}

func formatEventLog(event Event, showPPID bool) string {
	command := sanitizeTerminalText(event.Command)
	if command == "" {
		command = sanitizeTerminalText(event.Exe)
	}
	if command == "" {
		command = "-"
	}
	switch event.Kind {
	case EventStart:
		return formatStartLog(event.PID, event.ParentPID, command, showPPID)
	case EventExec:
		return formatExecLog(event.PID, event.ParentPID, command, showPPID)
	case EventStop:
		return formatStopLog(event.PID, event.ParentPID, command, showPPID)
	case EventGap:
		return formatGapLog(event.Message)
	default:
		return formatUnknownEventLog(event.Message)
	}
}

func formatStartLog(pid int, parentPID int, command string, showPPID bool) string {
	return formatProcessLog("Start", pid, parentPID, command, showPPID)
}
func formatExecLog(pid int, parentPID int, command string, showPPID bool) string {
	return formatProcessLog("Exec", pid, parentPID, command, showPPID)
}
func formatStopLog(pid int, parentPID int, command string, showPPID bool) string {
	return formatProcessLog("Stop", pid, parentPID, command, showPPID)
}
func formatProcessLog(kind string, pid int, parentPID int, command string, showPPID bool) string {
	if showPPID {
		return fmt.Sprintf("%s: %d (ppid %d): %s", kind, pid, parentPID, command)
	}
	return fmt.Sprintf("%s: %d: %s", kind, pid, command)
}
func formatGapLog(message string) string {
	return fmt.Sprintf("Gap: %s", sanitizeTerminalText(message))
}
func formatUnknownEventLog(message string) string {
	return fmt.Sprintf("Event: %s", sanitizeTerminalText(message))
}
