//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const linuxUserClockTicksPerSecond = 100

type linuxProcessClock struct {
	bootedAt time.Time
	valid    bool
}

type linuxProcessStat struct {
	command    string
	parentPID  int
	session    string
	tty        string
	startToken string
}

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("read /proc directory for process snapshot: %w", err)
	}
	clock := readLinuxProcessClock()
	snapshot := ProcessSnapshot{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		proc, err := readLinuxProcess(pid, time.Now(), clock)
		if err != nil {
			continue
		}
		snapshot[proc.ID] = proc
	}
	return snapshot, nil
}

func readLinuxProcess(pid int, capturedAt time.Time, clock linuxProcessClock) (Process, error) {
	stat, err := readLinuxStatDetails(pid)
	if err != nil {
		return Process{}, err
	}
	command := readLinuxCmdline(pid, stat.command)
	exe := readLinuxLink(pid, "exe")
	cwd := readLinuxLink(pid, "cwd")
	owner, uid := readLinuxIdentity(pid)
	id := processID(pid, stat.startToken)
	startedAt := clock.processStartedAt(stat.startToken, capturedAt)
	proc := capturedProcess(id, pid, stat.parentPID, command, exe, cwd, owner, startedAt, capturedAt)
	proc.UID = uid
	proc.TTY = readLinuxTTY(pid, stat.tty)
	proc.Session = stat.session
	return proc, nil
}

func readLinuxProcessClock() linuxProcessClock {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return linuxProcessClock{}
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return linuxProcessClock{}
	}
	uptime, err := time.ParseDuration(fields[0] + "s")
	if err != nil || uptime < 0 {
		return linuxProcessClock{}
	}
	return linuxProcessClock{bootedAt: time.Now().Add(-uptime), valid: true}
}

func (clock linuxProcessClock) processStartedAt(startToken string, capturedAt time.Time) time.Time {
	if !clock.valid {
		return time.Time{}
	}
	ticks, err := strconv.ParseUint(startToken, 10, 64)
	if err != nil {
		return time.Time{}
	}
	seconds := ticks / linuxUserClockTicksPerSecond
	nanoseconds := ticks % linuxUserClockTicksPerSecond * uint64(time.Second) / linuxUserClockTicksPerSecond
	if seconds > uint64((time.Duration(1<<63-1)-time.Duration(nanoseconds))/time.Second) {
		return time.Time{}
	}
	startedAt := clock.bootedAt.Add(time.Duration(seconds)*time.Second + time.Duration(nanoseconds))
	if startedAt.After(capturedAt.Add(time.Second)) {
		return time.Time{}
	}
	return startedAt
}

func readLinuxStat(pid int) (string, int, string, error) {
	stat, err := readLinuxStatDetails(pid)
	if err != nil {
		return "", 0, "", err
	}
	return stat.command, stat.parentPID, stat.startToken, nil
}

func readLinuxStatDetails(pid int) (linuxProcessStat, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return linuxProcessStat{}, err
	}
	text := string(data)
	left := strings.Index(text, "(")
	right := strings.LastIndex(text, ")")
	if left < 0 || right <= left {
		return linuxProcessStat{}, fmt.Errorf("parse /proc/%d/stat: expected command in parentheses, received %q", pid, text)
	}
	fields := strings.Fields(text[right+1:])
	if len(fields) < 20 {
		return linuxProcessStat{}, fmt.Errorf("parse /proc/%d/stat: expected at least 20 fields after command, received %d", pid, len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return linuxProcessStat{}, fmt.Errorf("parse /proc/%d/stat parent pid: expected integer, received %q", pid, fields[1])
	}
	return linuxProcessStat{
		command:    text[left+1 : right],
		parentPID:  ppid,
		session:    fields[3],
		tty:        fields[4],
		startToken: fields[19],
	}, nil
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
	owner, _ := readLinuxIdentity(pid)
	return owner
}

func readLinuxIdentity(pid int) (string, string) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return "", ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", ""
		}
		uid := fields[1]
		return resolvedUser(uid), uid
	}
	return "", ""
}

func readLinuxTTY(pid int, ttyNumber string) string {
	tty := readLinuxLink(pid, "fd/0")
	if strings.HasPrefix(tty, "/dev/") {
		return tty
	}
	if ttyNumber != "0" {
		return ttyNumber
	}
	return ""
}
