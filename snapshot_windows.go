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

const maxWindowsCommandLineBytes = 128 * 1024

type windowsProcessDetails struct {
	command   string
	exe       string
	startedAt time.Time
	user      string
	uid       string
}

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
	details := readWindowsProcessDetails(entry.ProcessID)
	if details.command != "" {
		command = details.command
	}
	exe := details.exe
	if exe == "" {
		exe = windows.UTF16ToString(entry.ExeFile[:])
	}
	startToken := ""
	if !details.startedAt.IsZero() {
		startToken = strconv.FormatInt(details.startedAt.UnixNano(), 10)
	}
	proc := capturedProcess(
		processID(pid, startToken), pid, parentPID, command, exe, "", details.user, details.startedAt, capturedAt,
	)
	proc.UID = details.uid
	var sessionID uint32
	if windows.ProcessIdToSessionId(entry.ProcessID, &sessionID) == nil {
		proc.Session = strconv.FormatUint(uint64(sessionID), 10)
	}
	return proc
}

func readWindowsProcessDetails(pid uint32) windowsProcessDetails {
	details := windowsProcessDetails{}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return details
	}
	defer windows.CloseHandle(handle)
	pathBuffer := make([]uint16, 32768)
	pathSize := uint32(len(pathBuffer))
	if windows.QueryFullProcessImageName(handle, 0, &pathBuffer[0], &pathSize) == nil {
		details.exe = windows.UTF16ToString(pathBuffer[:pathSize])
	}
	details.command = readWindowsProcessCommandLine(handle)
	details.user, details.uid = readWindowsProcessOwner(handle)
	var creation windows.Filetime
	var exit windows.Filetime
	var kernel windows.Filetime
	var userTime windows.Filetime
	if windows.GetProcessTimes(handle, &creation, &exit, &kernel, &userTime) != nil {
		return details
	}
	details.startedAt = time.Unix(0, creation.Nanoseconds())
	return details
}

func readWindowsProcessCommandLine(handle windows.Handle) string {
	var size uint32
	err := windows.NtQueryInformationProcess(
		handle, int32(windows.ProcessCommandLineInformation), nil, 0, &size,
	)
	if err != nil && !errors.Is(err, windows.STATUS_INFO_LENGTH_MISMATCH) {
		return ""
	}
	if size == 0 || size > maxWindowsCommandLineBytes {
		return ""
	}
	buffer := make([]byte, size)
	if err := windows.NtQueryInformationProcess(
		handle, int32(windows.ProcessCommandLineInformation), unsafe.Pointer(&buffer[0]), size, &size,
	); err != nil {
		return ""
	}
	commandLine := (*windows.NTUnicodeString)(unsafe.Pointer(&buffer[0]))
	if commandLine.Buffer == nil || commandLine.Length == 0 ||
		commandLine.Length%2 != 0 || commandLine.Length > commandLine.MaximumLength {
		return ""
	}
	return commandLine.String()
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
