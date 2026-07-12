//go:build windows

package main

import (
	"context"
	"testing"
	"time"
	"unsafe"
)

func TestWindowsETWStructureSizesMatch64BitSDK(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("release builds target 64-bit Windows")
	}
	sizes := []struct {
		name     string
		received uintptr
		expected uintptr
	}{
		{name: "EVENT_TRACE_PROPERTIES", received: unsafe.Sizeof(etwProperties{}), expected: 120},
		{name: "TRACE_LOGFILE_HEADER", received: unsafe.Sizeof(etwTraceLogfileHeader{}), expected: 280},
		{name: "EVENT_TRACE_LOGFILEW", received: unsafe.Sizeof(etwTraceLogfile{}), expected: 448},
		{name: "EVENT_RECORD", received: unsafe.Sizeof(etwEventRecord{}), expected: 112},
	}
	for _, size := range sizes {
		if size.received != size.expected {
			t.Fatalf("expected %s size %d bytes, received %d", size.name, size.expected, size.received)
		}
	}
}

func TestApplyWindowsETWStopIncludesExitStatus(t *testing.T) {
	now := time.Unix(10, 0)
	proc := capturedProcess("7:1", 7, 1, "worker", `C:\\worker.exe`, "", "S-1-5-18", now.Add(-time.Second), now.Add(-time.Second))
	processes := ProcessSnapshot{proc.ID: proc}
	code := 23
	notice := windowsETWNotice{kind: EventStop, pid: 7, exitCode: &code, eventTime: now, observedAt: now}
	events := make(chan Event, 1)

	applyWindowsETWNotice(context.Background(), events, processes, notice)

	event := <-events
	if event.Kind != EventStop || event.ExitCode == nil || *event.ExitCode != code || event.ProcessID != proc.ID {
		t.Fatalf("expected attributed stop event, received %#v", event)
	}
	if _, found := processes[proc.ID]; found {
		t.Fatalf("expected stopped process to leave the Windows ETW cache")
	}
}

func TestDecodeWindowsETWProcessFieldsUses64BitKernelLayout(t *testing.T) {
	data := make([]byte, 32)
	data[8] = 42
	data[12] = 7
	data[20] = 23
	record := etwEventRecord{
		EventHeader:    etwEventHeader{Flags: 0x0040},
		UserDataLength: uint16(len(data)),
		UserData:       unsafe.Pointer(&data[0]),
	}
	pid, parent, status, ok := decodeWindowsETWProcessFields(&record)
	if !ok || pid != 42 || parent != 7 || status != 23 {
		t.Fatalf("expected pid=42 parent=7 status=23, received pid=%d parent=%d status=%d ok=%t", pid, parent, status, ok)
	}
}
