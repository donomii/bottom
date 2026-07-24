package main

import (
	"fmt"
	"io"
	"log"
)

const (
	logKindWidth      = 6
	logPIDWidth       = 8
	logParentExeWidth = 24
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

func writeEventLog(writer io.Writer, event Event, showPPID bool, showParentExe bool) error {
	if _, err := fmt.Fprintln(writer, formatEventLog(event, showPPID, showParentExe)); err != nil {
		return fmt.Errorf("write process event log: %w", err)
	}
	return nil
}

func formatEventLog(event Event, showPPID bool, showParentExe bool) string {
	command := sanitizeTerminalText(event.Command)
	if command == "" {
		command = sanitizeTerminalText(event.Exe)
	}
	if command == "" {
		command = "-"
	}
	switch event.Kind {
	case EventStart:
		return formatStartLog(event.PID, event.ParentPID, parentExecutableName(event), command, showPPID, showParentExe)
	case EventExec:
		return formatExecLog(event.PID, event.ParentPID, parentExecutableName(event), command, showPPID, showParentExe)
	case EventStop:
		return formatStopLog(event.PID, event.ParentPID, parentExecutableName(event), command, showPPID, showParentExe)
	case EventGap:
		return formatGapLog(event.Message, showPPID, showParentExe)
	default:
		return formatUnknownEventLog(event.Message, showPPID, showParentExe)
	}
}

func formatStartLog(pid int, parentPID int, parentExe string, command string, showPPID bool, showParentExe bool) string {
	return formatProcessLog("Start", pid, parentPID, parentExe, command, showPPID, showParentExe)
}
func formatExecLog(pid int, parentPID int, parentExe string, command string, showPPID bool, showParentExe bool) string {
	return formatProcessLog("Exec", pid, parentPID, parentExe, command, showPPID, showParentExe)
}
func formatStopLog(pid int, parentPID int, parentExe string, command string, showPPID bool, showParentExe bool) string {
	return formatProcessLog("Stop", pid, parentPID, parentExe, command, showPPID, showParentExe)
}
func formatProcessLog(kind string, pid int, parentPID int, parentExe string, command string, showPPID bool, showParentExe bool) string {
	line := fmt.Sprintf("%-*s %-*d", logKindWidth, kind+":", logPIDWidth, pid)
	if showPPID {
		line += fmt.Sprintf(" %-*d", logPIDWidth, parentPID)
	}
	if showParentExe {
		line += fmt.Sprintf(" %-*s", logParentExeWidth, truncate(sanitizeTerminalText(parentExe), logParentExeWidth))
	}
	return line + " " + command
}
func formatGapLog(message string, showPPID bool, showParentExe bool) string {
	return formatMessageLog("Gap", message, showPPID, showParentExe)
}
func formatUnknownEventLog(message string, showPPID bool, showParentExe bool) string {
	return formatMessageLog("Event", message, showPPID, showParentExe)
}
func formatMessageLog(kind string, message string, showPPID bool, showParentExe bool) string {
	line := fmt.Sprintf("%-*s %-*s", logKindWidth, kind+":", logPIDWidth, "")
	if showPPID {
		line += fmt.Sprintf(" %-*s", logPIDWidth, "")
	}
	if showParentExe {
		line += fmt.Sprintf(" %-*s", logParentExeWidth, "")
	}
	return line + " " + sanitizeTerminalText(message)
}
