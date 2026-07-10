//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParseProcConnectorForkNotice(t *testing.T) {
	message := connectorTestMessage(procEventFork, 4, 12, 900, []uint32{10, 10, 20, 20})
	batch := parseProcConnectorBatch(message)
	if batch.malformed || len(batch.records) != 1 {
		t.Fatalf("expected one valid record, received malformed=%t records=%d", batch.malformed, len(batch.records))
	}
	record := batch.records[0]
	if record.cpu != 4 || record.sequence != 12 || !record.relevant {
		t.Fatalf("expected cpu 4 sequence 12 relevant record, received %#v", record)
	}
	if record.notice.kind != procEventFork || record.notice.pid != 20 || record.notice.parentPID != 10 || record.notice.timestampNS != 900 {
		t.Fatalf("expected fork 10 -> 20 at 900ns, received %#v", record.notice)
	}
}

func TestParseProcConnectorExecAndExitNotices(t *testing.T) {
	execBatch := parseProcConnectorBatch(connectorTestMessage(procEventExec, 1, 2, 300, []uint32{20, 20}))
	if len(execBatch.records) != 1 || execBatch.records[0].notice.kind != procEventExec || execBatch.records[0].notice.pid != 20 {
		t.Fatalf("expected exec notice for pid 20, received %#v", execBatch.records)
	}
	exitBatch := parseProcConnectorBatch(connectorTestMessage(procEventExit, 1, 3, 400, []uint32{20, 20, 7 << 8, 17}))
	if len(exitBatch.records) != 1 || exitBatch.records[0].notice.exitCode == nil || *exitBatch.records[0].notice.exitCode != 7 {
		t.Fatalf("expected exit code 7 for pid 20, received %#v", exitBatch.records)
	}
}

func TestParseProcConnectorRetainsThreadSequenceWithoutProcessNotice(t *testing.T) {
	batch := parseProcConnectorBatch(connectorTestMessage(procEventFork, 2, 8, 900, []uint32{10, 10, 21, 20}))
	if len(batch.records) != 1 || batch.records[0].relevant {
		t.Fatalf("expected a sequence-bearing thread record without process notice, received %#v", batch.records)
	}
}

func TestConnectorSequenceGapRequestsResync(t *testing.T) {
	batch := connectorBatch{records: []connectorRecord{{cpu: 2, sequence: 10}, {cpu: 2, sequence: 12}}}
	events := make(chan Event, 1)
	observedAt := time.Unix(20, 0)
	if !emitConnectorDiagnostics(context.Background(), BackendLinuxProcConnector, events, batch, map[uint32]uint32{}, observedAt) {
		t.Fatalf("expected sequence gap to request resync")
	}
	event := <-events
	if event.Kind != EventGap || !strings.Contains(event.Message, "expected sequence 11, received 12") {
		t.Fatalf("expected structured sequence gap, received %#v", event)
	}
}

func TestConnectorSequenceWrapDoesNotReportGap(t *testing.T) {
	batch := connectorBatch{records: []connectorRecord{{cpu: 2, sequence: 0}}}
	sequences := map[uint32]uint32{2: ^uint32(0)}
	events := make(chan Event, 1)
	if emitConnectorDiagnostics(context.Background(), BackendLinuxProcConnector, events, batch, sequences, time.Unix(20, 0)) {
		t.Fatalf("expected wrapped sequence to remain contiguous")
	}
	if len(events) != 0 {
		t.Fatalf("expected no gap event, received %d", len(events))
	}
}

func TestApplyConnectorNoticeEmitsDirectLifecycle(t *testing.T) {
	processes := ProcessSnapshot{}
	events := make(chan Event, 3)
	kernelClock := linuxKernelClock{monotonicNS: 100, wallTime: time.Unix(30, 0), valid: true}
	observedAt := time.Unix(31, 0)
	pid := 999999999
	processes = applyConnectorNotice(context.Background(), BackendLinuxProcConnector, events, processes, linuxProcessClock{}, kernelClock, connectorNotice{
		kind: procEventFork, pid: pid, parentPID: 7, timestampNS: 110,
	}, observedAt)
	processes = applyConnectorNotice(context.Background(), BackendLinuxProcConnector, events, processes, linuxProcessClock{}, kernelClock, connectorNotice{
		kind: procEventExec, pid: pid, timestampNS: 120,
	}, observedAt)
	exitCode := 4
	processes = applyConnectorNotice(context.Background(), BackendLinuxProcConnector, events, processes, linuxProcessClock{}, kernelClock, connectorNotice{
		kind: procEventExit, pid: pid, exitCode: &exitCode, timestampNS: 130,
	}, observedAt)
	start := <-events
	exec := <-events
	stop := <-events
	if start.Kind != EventStart || exec.Kind != EventExec || stop.Kind != EventStop {
		t.Fatalf("expected start, exec, stop; received %s, %s, %s", start.Kind, exec.Kind, stop.Kind)
	}
	if start.ParentPID != 7 || start.ProcessID == "" || exec.ProcessID != start.ProcessID || stop.ProcessID != start.ProcessID {
		t.Fatalf("expected one identified process with parent 7, received start=%#v exec=%#v stop=%#v", start, exec, stop)
	}
	if !start.Time.Equal(time.Unix(30, 10)) || !stop.Time.Equal(time.Unix(30, 30)) {
		t.Fatalf("expected converted kernel times, received start=%s stop=%s", start.Time, stop.Time)
	}
	if len(processes) != 0 {
		t.Fatalf("expected exit to remove process state, received %d entries", len(processes))
	}
}

func FuzzParseProcConnectorBatch(f *testing.F) {
	f.Add(connectorTestMessage(procEventExec, 1, 2, 300, []uint32{20, 20}))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		batch := parseProcConnectorBatch(data)
		if len(batch.records) > len(data) {
			t.Fatalf("expected no more records than input bytes, received records=%d bytes=%d", len(batch.records), len(data))
		}
	})
}

func connectorTestMessage(kind uint32, cpu uint32, sequence uint32, timestampNS uint64, union []uint32) []byte {
	eventLength := procEventUnionData + len(union)*4
	messageLength := syscall.NLMSG_HDRLEN + procConnectorData + eventLength
	message := make([]byte, alignNetlinkLength(messageLength))
	binary.LittleEndian.PutUint32(message[0:4], uint32(messageLength))
	binary.LittleEndian.PutUint16(message[4:6], syscall.NLMSG_DONE)
	cn := syscall.NLMSG_HDRLEN
	binary.LittleEndian.PutUint32(message[cn:cn+4], cnIdxProc)
	binary.LittleEndian.PutUint32(message[cn+4:cn+8], cnValProc)
	binary.LittleEndian.PutUint32(message[cn+8:cn+12], sequence)
	binary.LittleEndian.PutUint16(message[cn+16:cn+18], uint16(eventLength))
	event := cn + procConnectorData
	binary.LittleEndian.PutUint32(message[event:event+4], kind)
	binary.LittleEndian.PutUint32(message[event+4:event+8], cpu)
	binary.LittleEndian.PutUint64(message[event+8:event+16], timestampNS)
	for index, value := range union {
		offset := event + procEventUnionData + index*4
		binary.LittleEndian.PutUint32(message[offset:offset+4], value)
	}
	return message
}
