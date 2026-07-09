//go:build linux

package main

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc directory for process snapshot: %w", err)
	}
	now := time.Now()
	snapshot := ProcessSnapshot{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		comm, ppid, startToken, err := readLinuxStat(pid)
		if err != nil {
			continue
		}
		command := readLinuxCmdline(pid, comm)
		exe := readLinuxLink(pid, "exe")
		cwd := readLinuxLink(pid, "cwd")
		owner := readLinuxUser(pid)
		id := processID(pid, startToken)
		snapshot[id] = capturedProcess(id, pid, ppid, command, exe, cwd, owner, time.Time{}, now)
	}
	return snapshot, nil
}

func readLinuxStat(pid int) (string, int, string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, "", err
	}
	text := string(data)
	left := strings.Index(text, "(")
	right := strings.LastIndex(text, ")")
	if left < 0 || right <= left {
		return "", 0, "", fmt.Errorf("parse /proc/%d/stat: expected command in parentheses, received %q", pid, text)
	}
	fields := strings.Fields(text[right+1:])
	if len(fields) < 20 {
		return "", 0, "", fmt.Errorf("parse /proc/%d/stat: expected at least 20 fields after command, received %d", pid, len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, "", fmt.Errorf("parse /proc/%d/stat parent pid: expected integer, received %q", pid, fields[1])
	}
	return text[left+1 : right], ppid, fields[19], nil
}

func readLinuxCmdline(pid int, fallback string) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return fallback
	}
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	return commandFromParts(parts, fallback)
}

func readLinuxLink(pid int, name string) string {
	target, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), name))
	if err != nil {
		return ""
	}
	return target
}

func readLinuxUser(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ""
		}
		uid := fields[1]
		account, err := user.LookupId(uid)
		if err != nil {
			return uid
		}
		return account.Username
	}
	return ""
}
