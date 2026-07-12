//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	cnIdxProc          = 1
	cnValProc          = 1
	procCnMcastListen  = 1
	procCnMcastIgnore  = 2
	procEventFork      = 0x00000001
	procEventExec      = 0x00000002
	procEventExit      = 0x80000000
	procConnectorData  = 20
	procEventUnionData = 16
)

type LinuxProcConnectorBackend struct {
	interval time.Duration
}

type connectorNotice struct {
	kind        uint32
	pid         int
	parentPID   int
	exitCode    *int
	timestampNS uint64
}

type connectorRecord struct {
	cpu      uint32
	sequence uint32
	notice   connectorNotice
	relevant bool
}

type connectorBatch struct {
	records   []connectorRecord
	truncated bool
	overrun   bool
	malformed bool
}

type linuxKernelClock struct {
	monotonicNS uint64
	wallTime    time.Time
	valid       bool
}

func NewPlatformEventBackend(config Config) (LifecycleBackend, error) {
	return NewNamedEventBackend(Config{Backend: BackendLinuxProcConnector, PollInterval: config.PollInterval})
}

func NewNamedEventBackend(config Config) (LifecycleBackend, error) {
	switch config.Backend {
	case BackendLinuxProcConnector:
		return LinuxProcConnectorBackend{interval: config.PollInterval}, nil
	case BackendWindowsETW:
		return nil, fmt.Errorf("windows-etw backend is only available in Windows builds; this build target is Linux")
	case BackendMacOSEndpoint:
		return nil, fmt.Errorf("macos-endpoint-security backend is only available in macOS builds; this build target is Linux")
	default:
		return nil, fmt.Errorf("unknown event backend %q", config.Backend)
	}
}

func (backend LinuxProcConnectorBackend) Name() string {
	return BackendLinuxProcConnector
}

func (backend LinuxProcConnectorBackend) Watch(ctx context.Context, events chan<- Event) error {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, syscall.NETLINK_CONNECTOR)
	if err != nil {
		return fmt.Errorf("open netlink connector socket for process events: %w", err)
	}
	defer syscall.Close(fd)
	timeout := syscall.NsecToTimeval(int64(time.Second))
	if err := syscall.SetsockoptTimeval(fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &timeout); err != nil {
		return fmt.Errorf("set netlink connector receive timeout: %w", err)
	}
	addr := &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: cnIdxProc, Pid: uint32(os.Getpid())}
	if err := syscall.Bind(fd, addr); err != nil {
		return fmt.Errorf("bind netlink connector socket to process event group: %w", err)
	}
	if err := sendProcConnectorControl(fd, procCnMcastListen); err != nil {
		return fmt.Errorf("subscribe to process connector events: %w", err)
	}
	defer sendProcConnectorControl(fd, procCnMcastIgnore)
	processes, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot for proc connector backend: %w", err)
	}
	processClock := readLinuxProcessClock()
	kernelClock := readLinuxKernelClock()
	sequences := map[uint32]uint32{}
	nextResync := time.Now().Add(backend.resyncInterval())
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if !time.Now().Before(nextResync) {
			processes = backend.resync(ctx, events, processes)
			processClock = readLinuxProcessClock()
			kernelClock = readLinuxKernelClock()
			nextResync = time.Now().Add(backend.resyncInterval())
			continue
		}
		batch, err := receiveProcConnectorBatch(fd)
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				continue
			}
			if errors.Is(err, syscall.ENOBUFS) {
				observedAt := time.Now()
				sendConnectorGap(ctx, backend.Name(), events, observedAt, "process connector receive queue overflowed; lifecycle records are missing")
				processes = backend.resync(ctx, events, processes)
				processClock = readLinuxProcessClock()
				kernelClock = readLinuxKernelClock()
				nextResync = time.Now().Add(backend.resyncInterval())
				continue
			}
			return err
		}
		observedAt := time.Now()
		needsResync := emitConnectorDiagnostics(ctx, backend.Name(), events, batch, sequences, observedAt)
		for _, record := range batch.records {
			if !record.relevant {
				continue
			}
			processes = applyConnectorNotice(ctx, backend.Name(), events, processes, processClock, kernelClock, record.notice, observedAt)
		}
		if needsResync {
			processes = backend.resync(ctx, events, processes)
			processClock = readLinuxProcessClock()
			kernelClock = readLinuxKernelClock()
			nextResync = time.Now().Add(backend.resyncInterval())
		}
	}
}

func (backend LinuxProcConnectorBackend) resyncInterval() time.Duration {
	interval := backend.interval * 10
	if interval < time.Second {
		return time.Second
	}
	if interval > 30*time.Second {
		return 30 * time.Second
	}
	return interval
}

func (backend LinuxProcConnectorBackend) resync(ctx context.Context, events chan<- Event, previous ProcessSnapshot) ProcessSnapshot {
	next, err := ReadProcessSnapshot()
	if err != nil {
		now := time.Now()
		sendEvent(ctx, events, Event{
			Kind:       EventGap,
			Time:       now,
			ObservedAt: now,
			Backend:    backend.Name(),
			Message:    fmt.Sprintf("process connector resync failed; expected a complete process table, received error %v", err),
		})
		return previous
	}
	alignConnectorProcessIDs(previous, next)
	emitSnapshotDiff(ctx, backend.Name(), previous, next, events)
	return next
}

func alignConnectorProcessIDs(previous ProcessSnapshot, next ProcessSnapshot) {
	for nextID, nextProcess := range next {
		if _, ok := previous[nextID]; ok {
			continue
		}
		previousID, previousProcess, ok := findProcessEntryByPID(previous, nextProcess.PID)
		if !ok || !strings.HasPrefix(previousID, "connector:") || !sameProcessGeneration(previousProcess, nextProcess) {
			continue
		}
		delete(previous, previousID)
		previousProcess.ID = nextID
		previous[nextID] = previousProcess
	}
}

func applyConnectorNotice(ctx context.Context, backendName string, events chan<- Event, processes ProcessSnapshot, processClock linuxProcessClock, kernelClock linuxKernelClock, notice connectorNotice, observedAt time.Time) ProcessSnapshot {
	eventTime := kernelClock.eventTime(notice.timestampNS, observedAt)
	switch notice.kind {
	case procEventFork:
		proc, err := readLinuxProcess(notice.pid, observedAt, processClock)
		if err != nil {
			id := connectorProcessID(notice.pid, notice.timestampNS)
			proc = capturedProcess(id, notice.pid, notice.parentPID, "", "", "", "", eventTime, observedAt)
		} else if notice.parentPID > 0 {
			proc.ParentPID = notice.parentPID
		}
		oldProcess, found := findProcessByPID(processes, notice.pid)
		if found && sameProcessGeneration(oldProcess, proc) {
			if err != nil {
				proc = oldProcess
				proc.ParentPID = notice.parentPID
			} else {
				preserveProcessObservation(oldProcess, &proc)
			}
		} else if found {
			sendEvent(ctx, events, processStopEventObserved(eventTime, observedAt, backendName, oldProcess, processes, nil))
		}
		removeProcessByPID(processes, notice.pid)
		processes[proc.ID] = proc
		sendEvent(ctx, events, processStartEventObserved(eventTime, observedAt, backendName, proc, processes))
	case procEventExec:
		oldProcess, found := findProcessByPID(processes, notice.pid)
		proc, err := readLinuxProcess(notice.pid, observedAt, processClock)
		if err != nil {
			if found {
				proc = oldProcess
			} else {
				id := connectorProcessID(notice.pid, notice.timestampNS)
				proc = capturedProcess(id, notice.pid, 0, "", "", "", "", eventTime, observedAt)
			}
		} else if found {
			preserveProcessObservation(oldProcess, &proc)
		}
		removeProcessByPID(processes, notice.pid)
		processes[proc.ID] = proc
		sendEvent(ctx, events, processExecEvent(eventTime, observedAt, backendName, proc, processes))
	case procEventExit:
		proc, found := findProcessByPID(processes, notice.pid)
		if !found {
			id := connectorProcessID(notice.pid, notice.timestampNS)
			proc = capturedProcess(id, notice.pid, 0, "", "", "", "", eventTime, observedAt)
		}
		sendEvent(ctx, events, processStopEventObserved(eventTime, observedAt, backendName, proc, processes, notice.exitCode))
		removeProcessByPID(processes, notice.pid)
	}
	return processes
}

func connectorProcessID(pid int, timestampNS uint64) string {
	return fmt.Sprintf("connector:%d:%d", pid, timestampNS)
}

func findProcessEntryByPID(snapshot ProcessSnapshot, pid int) (string, Process, bool) {
	for id, proc := range snapshot {
		if proc.PID == pid {
			return id, proc, true
		}
	}
	return "", Process{}, false
}

func emitConnectorDiagnostics(ctx context.Context, backendName string, events chan<- Event, batch connectorBatch, sequences map[uint32]uint32, observedAt time.Time) bool {
	needsResync := false
	if batch.truncated {
		sendConnectorGap(ctx, backendName, events, observedAt, "process connector receive buffer was truncated; lifecycle records may be missing")
		needsResync = true
	}
	if batch.overrun {
		sendConnectorGap(ctx, backendName, events, observedAt, "process connector reported a netlink overrun; lifecycle records are missing")
		needsResync = true
	}
	if batch.malformed && !batch.truncated {
		sendConnectorGap(ctx, backendName, events, observedAt, "process connector returned a malformed netlink message; lifecycle records may be missing")
		needsResync = true
	}
	for _, record := range batch.records {
		previous, ok := sequences[record.cpu]
		sequences[record.cpu] = record.sequence
		if !ok || record.sequence == previous+1 {
			continue
		}
		message := fmt.Sprintf("process connector sequence gap on cpu %d; expected sequence %d, received %d", record.cpu, previous+1, record.sequence)
		sendConnectorGap(ctx, backendName, events, observedAt, message)
		needsResync = true
	}
	return needsResync
}

func sendConnectorGap(ctx context.Context, backendName string, events chan<- Event, observedAt time.Time, message string) {
	sendEvent(ctx, events, Event{Kind: EventGap, Time: observedAt, ObservedAt: observedAt, Backend: backendName, Message: message})
}

func readLinuxKernelClock() linuxKernelClock {
	var monotonic unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &monotonic); err != nil {
		return linuxKernelClock{}
	}
	return linuxKernelClock{monotonicNS: uint64(monotonic.Nano()), wallTime: time.Now(), valid: true}
}

func (clock linuxKernelClock) eventTime(timestampNS uint64, observedAt time.Time) time.Time {
	if !clock.valid || timestampNS == 0 {
		return observedAt
	}
	if timestampNS >= clock.monotonicNS {
		delta := timestampNS - clock.monotonicNS
		if delta > uint64(1<<63-1) {
			return observedAt
		}
		return clock.wallTime.Add(time.Duration(delta))
	}
	delta := clock.monotonicNS - timestampNS
	if delta > uint64(1<<63-1) {
		return observedAt
	}
	return clock.wallTime.Add(-time.Duration(delta))
}

func sendProcConnectorControl(fd int, op uint32) error {
	request := make([]byte, syscall.NLMSG_HDRLEN+procConnectorData+4)
	binary.LittleEndian.PutUint32(request[0:4], uint32(len(request)))
	binary.LittleEndian.PutUint16(request[4:6], syscall.NLMSG_DONE)
	binary.LittleEndian.PutUint32(request[8:12], 1)
	binary.LittleEndian.PutUint32(request[12:16], uint32(os.Getpid()))
	cn := syscall.NLMSG_HDRLEN
	binary.LittleEndian.PutUint32(request[cn:cn+4], cnIdxProc)
	binary.LittleEndian.PutUint32(request[cn+4:cn+8], cnValProc)
	binary.LittleEndian.PutUint16(request[cn+16:cn+18], 4)
	binary.LittleEndian.PutUint32(request[cn+procConnectorData:cn+procConnectorData+4], op)
	return syscall.Sendto(fd, request, 0, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK})
}

func receiveProcConnectorBatch(fd int) (connectorBatch, error) {
	buffer := make([]byte, 64*1024)
	n, _, flags, _, err := syscall.Recvmsg(fd, buffer, nil, 0)
	if err != nil {
		return connectorBatch{}, fmt.Errorf("receive process connector event: %w", err)
	}
	batch := parseProcConnectorBatch(buffer[:n])
	batch.truncated = flags&syscall.MSG_TRUNC != 0
	return batch, nil
}

func receiveProcConnectorNotices(fd int) ([]connectorNotice, error) {
	batch, err := receiveProcConnectorBatch(fd)
	if err != nil {
		return nil, err
	}
	return relevantConnectorNotices(batch), nil
}

func parseProcConnectorNotices(buffer []byte) []connectorNotice {
	return relevantConnectorNotices(parseProcConnectorBatch(buffer))
}

func relevantConnectorNotices(batch connectorBatch) []connectorNotice {
	notices := []connectorNotice{}
	for _, record := range batch.records {
		if record.relevant {
			notices = append(notices, record.notice)
		}
	}
	return notices
}

func parseProcConnectorBatch(buffer []byte) connectorBatch {
	batch := connectorBatch{}
	offset := 0
	for offset+syscall.NLMSG_HDRLEN <= len(buffer) {
		length := int(binary.LittleEndian.Uint32(buffer[offset : offset+4]))
		if length < syscall.NLMSG_HDRLEN || offset+length > len(buffer) {
			batch.malformed = true
			return batch
		}
		messageType := binary.LittleEndian.Uint16(buffer[offset+4 : offset+6])
		if messageType == syscall.NLMSG_OVERRUN {
			batch.overrun = true
		} else {
			payload := buffer[offset+syscall.NLMSG_HDRLEN : offset+length]
			record, valid := parseProcConnectorRecord(payload)
			if valid {
				batch.records = append(batch.records, record)
			} else if messageType == syscall.NLMSG_DONE {
				batch.malformed = true
			}
		}
		offset += alignNetlinkLength(length)
	}
	if offset != len(buffer) {
		batch.malformed = true
	}
	return batch
}

func parseProcConnectorMessage(payload []byte) []connectorNotice {
	record, valid := parseProcConnectorRecord(payload)
	if !valid || !record.relevant {
		return []connectorNotice{}
	}
	return []connectorNotice{record.notice}
}

func parseProcConnectorRecord(payload []byte) (connectorRecord, bool) {
	if len(payload) < procConnectorData+procEventUnionData {
		return connectorRecord{}, false
	}
	if binary.LittleEndian.Uint32(payload[0:4]) != cnIdxProc || binary.LittleEndian.Uint32(payload[4:8]) != cnValProc {
		return connectorRecord{}, false
	}
	length := int(binary.LittleEndian.Uint16(payload[16:18]))
	if length < procEventUnionData || procConnectorData+length > len(payload) {
		return connectorRecord{}, false
	}
	event := payload[procConnectorData : procConnectorData+length]
	record := connectorRecord{
		cpu:      binary.LittleEndian.Uint32(event[4:8]),
		sequence: binary.LittleEndian.Uint32(payload[8:12]),
	}
	what := binary.LittleEndian.Uint32(event[0:4])
	union := event[procEventUnionData:]
	timestampNS := binary.LittleEndian.Uint64(event[8:16])
	switch what {
	case procEventFork:
		if len(union) < 16 {
			return connectorRecord{}, false
		}
		childPID := int(binary.LittleEndian.Uint32(union[8:12]))
		childTGID := int(binary.LittleEndian.Uint32(union[12:16]))
		if childPID != childTGID {
			return record, true
		}
		record.notice = connectorNotice{
			kind:        what,
			pid:         childTGID,
			parentPID:   int(binary.LittleEndian.Uint32(union[4:8])),
			timestampNS: timestampNS,
		}
		record.relevant = true
	case procEventExec:
		if len(union) < 8 {
			return connectorRecord{}, false
		}
		processPID := int(binary.LittleEndian.Uint32(union[0:4]))
		processTGID := int(binary.LittleEndian.Uint32(union[4:8]))
		if processPID != processTGID {
			return record, true
		}
		record.notice = connectorNotice{kind: what, pid: processTGID, timestampNS: timestampNS}
		record.relevant = true
	case procEventExit:
		if len(union) < 16 {
			return connectorRecord{}, false
		}
		processPID := int(binary.LittleEndian.Uint32(union[0:4]))
		processTGID := int(binary.LittleEndian.Uint32(union[4:8]))
		if processPID != processTGID {
			return record, true
		}
		code := decodeLinuxWaitStatus(binary.LittleEndian.Uint32(union[8:12]))
		record.notice = connectorNotice{kind: what, pid: processTGID, exitCode: &code, timestampNS: timestampNS}
		record.relevant = true
	}
	return record, true
}

func decodeLinuxWaitStatus(status uint32) int {
	signal := status & 0x7f
	if signal == 0 {
		return int((status >> 8) & 0xff)
	}
	if signal != 0x7f {
		return 128 + int(signal)
	}
	return int(status)
}

func alignNetlinkLength(length int) int {
	return (length + 3) & ^3
}

func exitCodesByPID(notices []connectorNotice) map[int]int {
	codes := map[int]int{}
	for _, notice := range notices {
		if notice.exitCode != nil {
			codes[notice.pid] = *notice.exitCode
		}
	}
	return codes
}
