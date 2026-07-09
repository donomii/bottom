package main

import (
	"fmt"
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
