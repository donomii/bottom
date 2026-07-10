//go:build windows

package main

import (
	"errors"
	"fmt"
	"strconv"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ReadProcessSnapshot() (ProcessSnapshot, error) {
	handle, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, fmt.Errorf("create native Windows process snapshot: %w", err)
	}
	defer windows.CloseHandle(handle)
	now := time.Now()
	snapshot := ProcessSnapshot{}
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err := windows.Process32First(handle, &entry); err != nil {
		return nil, fmt.Errorf("read first process from native Windows snapshot: %w", err)
	}
	for {
		proc := windowsSnapshotProcess(entry, now)
		snapshot[proc.ID] = proc
		entry.Size = uint32(unsafe.Sizeof(windows.ProcessEntry32{}))
		if err := windows.Process32Next(handle, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, fmt.Errorf("read next process from native Windows snapshot after pid %d: %w", proc.PID, err)
		}
	}
	return snapshot, nil
}

func windowsSnapshotProcess(entry windows.ProcessEntry32, capturedAt time.Time) Process {
	pid := int(entry.ProcessID)
	parentPID := int(entry.ParentProcessID)
	command := windows.UTF16ToString(entry.ExeFile[:])
	exe, startedAt, user, uid := readWindowsProcessDetails(entry.ProcessID)
	if exe == "" {
		exe = command
	}
	startToken := ""
	if !startedAt.IsZero() {
		startToken = strconv.FormatInt(startedAt.UnixNano(), 10)
	}
	proc := capturedProcess(processID(pid, startToken), pid, parentPID, command, exe, "", user, startedAt, capturedAt)
	proc.UID = uid
	var sessionID uint32
	if windows.ProcessIdToSessionId(entry.ProcessID, &sessionID) == nil {
		proc.Session = strconv.FormatUint(uint64(sessionID), 10)
	}
	return proc
}

func readWindowsProcessDetails(pid uint32) (string, time.Time, string, string) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return "", time.Time{}, "", ""
	}
	defer windows.CloseHandle(handle)
	pathBuffer := make([]uint16, 32768)
	pathSize := uint32(len(pathBuffer))
	exe := ""
	if windows.QueryFullProcessImageName(handle, 0, &pathBuffer[0], &pathSize) == nil {
		exe = windows.UTF16ToString(pathBuffer[:pathSize])
	}
	user, uid := readWindowsProcessOwner(handle)
	var creation windows.Filetime
	var exit windows.Filetime
	var kernel windows.Filetime
	var userTime windows.Filetime
	if windows.GetProcessTimes(handle, &creation, &exit, &kernel, &userTime) != nil {
		return exe, time.Time{}, user, uid
	}
	return exe, time.Unix(0, creation.Nanoseconds()), user, uid
}

func readWindowsProcessOwner(handle windows.Handle) (string, string) {
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return "", ""
	}
	defer token.Close()
	tokenUser, err := token.GetTokenUser()
	if err != nil || tokenUser.User.Sid == nil {
		return "", ""
	}
	uid := tokenUser.User.Sid.String()
	return resolvedUser(uid), uid
}
