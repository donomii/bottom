//go:build !linux && !windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	output, err := exec.Command("ps", "-axo", "pid=,ppid=,user=,command=").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run ps for process snapshot: expected process table output, received error %w and output %q", err, string(output))
	}
	now := time.Now()
	snapshot := ProcessSnapshot{}
	for _, line := range strings.Split(string(output), "\n") {
		proc, ok := parsePSLine(line, now)
		if !ok {
			continue
		}
		snapshot[proc.ID] = proc
	}
	return snapshot, nil
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
	exe := ""
	if len(fields) >= 4 {
		exe = fields[3]
	}
	id := processID(pid, "")
	return capturedProcess(id, pid, ppid, command, exe, "", fields[2], time.Time{}, capturedAt), true
}
