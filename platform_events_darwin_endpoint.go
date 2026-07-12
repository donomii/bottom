//go:build darwin && cgo && endpointsecurity

package main

/*
#cgo CFLAGS: -fblocks -mmacosx-version-min=10.15
#cgo LDFLAGS: -lEndpointSecurity -lbsm -mmacosx-version-min=10.15
#include "endpoint_security_bridge_darwin.h"
*/
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const maxMacOSEndpointPendingNotices = 4096

var (
	macOSEndpointHandlers      = map[uint64]*macOSEndpointHandler{}
	macOSEndpointHandlersMutex sync.RWMutex
	macOSEndpointNextHandlerID uint64 = 1
)

type macOSEndpointBackend struct {
	interval time.Duration
}

type macOSEndpointNotice struct {
	kind           EventKind
	pid            int
	parentPID      int
	uid            string
	session        string
	pidVersion     uint32
	exitCode       *int
	eventTime      time.Time
	exe            string
	command        string
	tty            string
	cwd            string
	startedAt      time.Time
	sequence       uint64
	globalSequence bool
}

type macOSEndpointHandler struct {
	id      uint64
	notices chan macOSEndpointNotice
	lost    atomic.Uint64
}

func newMacOSEndpointBackend(config Config) (LifecycleBackend, error) {
	return macOSEndpointBackend{interval: config.PollInterval}, nil
}

func (backend macOSEndpointBackend) Name() string {
	return BackendMacOSEndpoint
}

func (backend macOSEndpointBackend) Watch(ctx context.Context, events chan<- Event) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	handler := newMacOSEndpointHandler()
	defer handler.unregister()
	var result C.int
	client := C.bottom_es_open(C.uint64_t(handler.id), &result)
	if client == nil {
		return macOSEndpointOpenError(int(result))
	}
	closeClient := func() error {
		if closeResult := C.bottom_es_close(client); closeResult != 0 {
			return fmt.Errorf("close macOS Endpoint Security client: expected result 0, received %d", int(closeResult))
		}
		return nil
	}
	processes, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot after subscribing to macOS Endpoint Security: %w; cleanup=%v", err, closeClient())
	}
	resyncTicker := time.NewTicker(macOSEndpointResyncInterval(backend.interval))
	defer resyncTicker.Stop()
	sequences := map[EventKind]uint64{}
	var globalSequence uint64
	var reportedLost uint64
	for {
		select {
		case <-ctx.Done():
			if err := closeClient(); err != nil {
				return err
			}
			return ctx.Err()
		case notice := <-handler.notices:
			if gapMessage := macOSEndpointSequenceGap(notice, sequences, &globalSequence); gapMessage != "" {
				sendEvent(ctx, events, Event{Kind: EventGap, Time: notice.eventTime, ObservedAt: time.Now(), Backend: backend.Name(), Message: gapMessage})
				processes = resyncMacOSEndpoint(ctx, events, processes)
			}
			processes = applyMacOSEndpointNotice(ctx, events, processes, notice)
			lost := handler.lost.Load()
			if lost > reportedLost {
				now := time.Now()
				message := fmt.Sprintf("macOS Endpoint Security callback queue overflowed; expected every lifecycle notice, dropped %d notices", lost-reportedLost)
				sendEvent(ctx, events, Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: backend.Name(), Message: message})
				reportedLost = lost
				processes = resyncMacOSEndpoint(ctx, events, processes)
			}
		case <-resyncTicker.C:
			processes = resyncMacOSEndpoint(ctx, events, processes)
		}
	}
}

func newMacOSEndpointHandler() *macOSEndpointHandler {
	id := atomic.AddUint64(&macOSEndpointNextHandlerID, 1)
	handler := &macOSEndpointHandler{id: id, notices: make(chan macOSEndpointNotice, maxMacOSEndpointPendingNotices)}
	macOSEndpointHandlersMutex.Lock()
	macOSEndpointHandlers[id] = handler
	macOSEndpointHandlersMutex.Unlock()
	return handler
}

func (handler *macOSEndpointHandler) unregister() {
	macOSEndpointHandlersMutex.Lock()
	delete(macOSEndpointHandlers, handler.id)
	macOSEndpointHandlersMutex.Unlock()
}

//export bottomGoESEvent
func bottomGoESEvent(context C.uint64_t, raw *C.bottom_es_event) {
	macOSEndpointHandlersMutex.RLock()
	handler := macOSEndpointHandlers[uint64(context)]
	macOSEndpointHandlersMutex.RUnlock()
	if handler == nil {
		return
	}
	notice, ok := decodeMacOSEndpointNotice(raw)
	if !ok {
		return
	}
	select {
	case handler.notices <- notice:
	default:
		handler.lost.Add(1)
	}
}

func decodeMacOSEndpointNotice(raw *C.bottom_es_event) (macOSEndpointNotice, bool) {
	kind := EventKind("")
	switch raw.kind {
	case C.BOTTOM_ES_EVENT_FORK:
		kind = EventStart
	case C.BOTTOM_ES_EVENT_EXEC:
		kind = EventExec
	case C.BOTTOM_ES_EVENT_EXIT:
		kind = EventStop
	default:
		return macOSEndpointNotice{}, false
	}
	notice := macOSEndpointNotice{
		kind:           kind,
		pid:            int(raw.pid),
		parentPID:      int(raw.parent_pid),
		uid:            strconv.FormatUint(uint64(raw.uid), 10),
		session:        strconv.FormatUint(uint64(raw.session_id), 10),
		pidVersion:     uint32(raw.pid_version),
		eventTime:      time.Unix(0, int64(raw.event_time_unix_nano)),
		exe:            C.GoString(&raw.executable[0]),
		command:        C.GoString(&raw.command[0]),
		tty:            C.GoString(&raw.tty[0]),
		cwd:            C.GoString(&raw.cwd[0]),
		sequence:       uint64(raw.sequence),
		globalSequence: raw.global_sequence != 0,
	}
	if raw.start_time_unix_nano != 0 {
		notice.startedAt = time.Unix(0, int64(raw.start_time_unix_nano))
	}
	if kind == EventStop {
		code := decodeDarwinWaitStatus(uint32(raw.exit_status))
		notice.exitCode = &code
	}
	return notice, notice.pid > 0 && !notice.eventTime.IsZero()
}

func decodeDarwinWaitStatus(status uint32) int {
	signal := status & 0x7f
	if signal == 0 {
		return int((status >> 8) & 0xff)
	}
	if signal != 0x7f {
		return 128 + int(signal)
	}
	return int(status)
}

func applyMacOSEndpointNotice(ctx context.Context, events chan<- Event, processes ProcessSnapshot, notice macOSEndpointNotice) ProcessSnapshot {
	proc := macOSEndpointProcess(notice)
	previous, found := findProcessByPID(processes, notice.pid)
	switch notice.kind {
	case EventStart:
		if found && sameProcessGeneration(previous, proc) {
			preserveProcessObservation(previous, &proc)
		} else if found {
			sendEvent(ctx, events, processStopEventObserved(notice.eventTime, time.Now(), BackendMacOSEndpoint, previous, processes, nil))
		}
		removeProcessByPID(processes, notice.pid)
		processes[proc.ID] = proc
		sendEvent(ctx, events, processStartEventObserved(notice.eventTime, time.Now(), BackendMacOSEndpoint, proc, processes))
	case EventExec:
		if found {
			preserveProcessObservation(previous, &proc)
		}
		removeProcessByPID(processes, notice.pid)
		processes[proc.ID] = proc
		sendEvent(ctx, events, processExecEvent(notice.eventTime, time.Now(), BackendMacOSEndpoint, proc, processes))
	case EventStop:
		if found {
			proc = previous
		}
		sendEvent(ctx, events, processStopEventObserved(notice.eventTime, time.Now(), BackendMacOSEndpoint, proc, processes, notice.exitCode))
		removeProcessByPID(processes, notice.pid)
	}
	return processes
}

func macOSEndpointProcess(notice macOSEndpointNotice) Process {
	startedAt := notice.startedAt
	id := ""
	if startedAt.IsZero() {
		id = fmt.Sprintf("es:%d:%d", notice.pid, notice.pidVersion)
	} else {
		id = processID(notice.pid, strconv.FormatInt(startedAt.UnixNano(), 10))
	}
	proc := capturedProcess(id, notice.pid, notice.parentPID, notice.command, notice.exe, notice.cwd, resolvedUser(notice.uid), startedAt, time.Now())
	proc.UID = notice.uid
	proc.TTY = notice.tty
	proc.Session = notice.session
	return proc
}

func macOSEndpointSequenceGap(notice macOSEndpointNotice, sequences map[EventKind]uint64, global *uint64) string {
	if notice.sequence == 0 {
		return ""
	}
	previous := uint64(0)
	if notice.globalSequence {
		previous = *global
		*global = notice.sequence
	} else {
		previous = sequences[notice.kind]
		sequences[notice.kind] = notice.sequence
	}
	if previous == 0 || notice.sequence == previous+1 {
		return ""
	}
	return fmt.Sprintf("macOS Endpoint Security sequence gap; expected sequence %d, received %d", previous+1, notice.sequence)
}

func macOSEndpointResyncInterval(interval time.Duration) time.Duration {
	value := interval * 10
	if value < time.Second {
		return time.Second
	}
	if value > 30*time.Second {
		return 30 * time.Second
	}
	return value
}

func resyncMacOSEndpoint(ctx context.Context, events chan<- Event, previous ProcessSnapshot) ProcessSnapshot {
	next, err := ReadProcessSnapshot()
	if err != nil {
		now := time.Now()
		message := fmt.Sprintf("macOS Endpoint Security resync failed; expected a complete process table, received error %v", err)
		sendEvent(ctx, events, Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendMacOSEndpoint, Message: message})
		return previous
	}
	emitSnapshotDiff(ctx, BackendMacOSEndpoint, previous, next, events)
	return next
}

func macOSEndpointOpenError(result int) error {
	detail := "internal Endpoint Security error"
	switch result {
	case 1:
		detail = "invalid Endpoint Security client arguments"
	case 2:
		detail = "Endpoint Security subsystem communication failed"
	case 3:
		detail = "missing com.apple.developer.endpoint-security.client entitlement"
	case 4:
		detail = "Full Disk Access has not been granted"
	case 5:
		detail = "the process is not running with the required privilege"
	case 6:
		detail = "the system Endpoint Security client limit was reached"
	case int(C.BOTTOM_ES_SUBSCRIBE_FAILED):
		detail = "subscription to fork, exec, and exit notifications failed"
	}
	return fmt.Errorf("start macOS Endpoint Security backend: result=%d reason=%s", result, detail)
}
