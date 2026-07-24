package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func processID(pid int, startToken string) string {
	if startToken == "" {
		return strconv.Itoa(pid)
	}
	return fmt.Sprintf("%d:%s", pid, startToken)
}

func commandFromParts(parts []string, fallback string) string {
	clean := []string{}
	for _, part := range parts {
		if part != "" {
			clean = append(clean, part)
		}
	}
	if len(clean) > 0 {
		return strings.Join(clean, " ")
	}
	return fallback
}

func capturedProcess(id string, pid int, ppid int, command string, exe string, cwd string, user string, startedAt time.Time, capturedAt time.Time) Process {
	return Process{
		ID:         id,
		PID:        pid,
		ParentPID:  ppid,
		Command:    strings.TrimSpace(command),
		Exe:        strings.TrimSpace(exe),
		Cwd:        strings.TrimSpace(cwd),
		User:       strings.TrimSpace(user),
		StartedAt:  startedAt,
		CapturedAt: capturedAt,
	}
}

func parentExecutableName(event Event) string {
	if len(event.ParentChain) == 0 {
		return "-"
	}
	executable := event.ParentChain[0].Exe
	if executable == "" {
		fields := strings.Fields(event.ParentChain[0].Command)
		if len(fields) > 0 {
			executable = fields[0]
		}
	}
	if executable == "" {
		return "-"
	}
	return filepath.Base(executable)
}

func parsePSOutput(output []byte, capturedAt time.Time, collectorPID int) ProcessSnapshot {
	snapshot := ProcessSnapshot{}
	for _, line := range strings.Split(string(output), "\n") {
		proc, ok := parsePSLine(line, capturedAt)
		if !ok || proc.PID == collectorPID {
			continue
		}
		snapshot[proc.ID] = proc
	}
	return snapshot
}

func parsePSLine(line string, capturedAt time.Time) (Process, bool) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return Process{}, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return Process{}, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return Process{}, false
	}
	command := strings.Join(fields[3:], " ")
	id := processID(pid, "")
	return capturedProcess(id, pid, ppid, command, fields[3], "", fields[2], time.Time{}, capturedAt), true
}
