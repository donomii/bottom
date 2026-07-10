//go:build darwin

package main

import (
	"encoding/binary"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	entries, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("read native macOS process table: %w", err)
	}
	now := time.Now()
	snapshot := ProcessSnapshot{}
	for _, entry := range entries {
		pid := int(entry.Proc.P_pid)
		if pid <= 0 {
			continue
		}
		startedAt := time.Unix(entry.Proc.P_starttime.Sec, int64(entry.Proc.P_starttime.Usec)*int64(time.Microsecond))
		startToken := strconv.FormatInt(startedAt.UnixNano(), 10)
		exe, command := readDarwinProcessArgs(pid)
		if command == "" {
			command = darwinCommand(entry.Proc.P_comm[:])
		}
		if exe == "" {
			fields := strings.Fields(command)
			if len(fields) > 0 {
				exe = fields[0]
			}
		}
		uid := strconv.FormatUint(uint64(entry.Eproc.Ucred.Uid), 10)
		proc := capturedProcess(processID(pid, startToken), pid, int(entry.Eproc.Ppid), command, exe, "", resolvedUser(uid), startedAt, now)
		proc.UID = uid
		if entry.Eproc.Tdev != -1 {
			proc.TTY = strconv.Itoa(int(entry.Eproc.Tdev))
		}
		snapshot[proc.ID] = proc
	}
	return snapshot, nil
}

func readDarwinProcessArgs(pid int) (string, string) {
	data, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(data) < 4 {
		return "", ""
	}
	argumentCount := int(binary.LittleEndian.Uint32(data[:4]))
	data = data[4:]
	executableEnd := byteIndex(data, 0)
	if executableEnd < 0 {
		return "", ""
	}
	executable := string(data[:executableEnd])
	position := executableEnd
	for position < len(data) && data[position] == 0 {
		position++
	}
	arguments := []string{}
	for len(arguments) < argumentCount && position < len(data) {
		end := byteIndex(data[position:], 0)
		if end < 0 {
			break
		}
		arguments = append(arguments, string(data[position:position+end]))
		position += end + 1
	}
	return executable, commandFromParts(arguments, executable)
}

func byteIndex(data []byte, value byte) int {
	for index, candidate := range data {
		if candidate == value {
			return index
		}
	}
	return -1
}

func darwinCommand(command []byte) string {
	bytes := make([]byte, 0, len(command))
	for _, character := range command {
		if character == 0 {
			break
		}
		bytes = append(bytes, character)
	}
	return string(bytes)
}
