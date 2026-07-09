//go:build windows

package main

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	output, err := exec.Command("wmic", "process", "get", "ProcessId,ParentProcessId,ExecutablePath,CommandLine", "/format:csv").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("run wmic for process snapshot: expected process table output, received error %w and output %q", err, string(output))
	}
	records, err := csv.NewReader(bytes.NewReader(output)).ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse wmic csv process snapshot: %w", err)
	}
	if len(records) == 0 {
		return ProcessSnapshot{}, nil
	}
	header := records[0]
	commandIndex := csvIndex(header, "CommandLine")
	exeIndex := csvIndex(header, "ExecutablePath")
	parentIndex := csvIndex(header, "ParentProcessId")
	pidIndex := csvIndex(header, "ProcessId")
	if commandIndex < 0 || exeIndex < 0 || parentIndex < 0 || pidIndex < 0 {
		return nil, fmt.Errorf("parse wmic csv process snapshot: expected CommandLine, ExecutablePath, ParentProcessId, and ProcessId columns, received %q", strings.Join(header, ","))
	}
	now := time.Now()
	snapshot := ProcessSnapshot{}
	for _, record := range records[1:] {
		if len(record) <= pidIndex || len(record) <= parentIndex {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(record[pidIndex]))
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(strings.TrimSpace(record[parentIndex]))
		if err != nil {
			ppid = 0
		}
		command := strings.TrimSpace(record[commandIndex])
		exe := strings.TrimSpace(record[exeIndex])
		if command == "" {
			command = exe
		}
		id := processID(pid, "")
		snapshot[id] = capturedProcess(id, pid, ppid, command, exe, "", "", time.Time{}, now)
	}
	return snapshot, nil
}

func csvIndex(header []string, name string) int {
	for i, field := range header {
		if strings.EqualFold(strings.TrimSpace(field), name) {
			return i
		}
	}
	return -1
}
