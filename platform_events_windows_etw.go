//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	etwRealTimeMode          = 0x00000100
	etwSystemLoggerMode      = 0x02000000
	etwProcessFlag           = 0x00000001
	etwEventRecordMode       = 0x10000000
	etwWnodeTracedGUID       = 0x00020000
	etwClockSystemTime       = 2
	etwProcessStartOpcode    = 1
	etwProcessEndOpcode      = 2
	etwMaximumPendingNotices = 4096
)

var (
	advapi32                      = windows.NewLazySystemDLL("advapi32.dll")
	tdhDLL                        = windows.NewLazySystemDLL("tdh.dll")
	procStartTraceW               = advapi32.NewProc("StartTraceW")
	procStopTraceW                = advapi32.NewProc("StopTraceW")
	procOpenTraceW                = advapi32.NewProc("OpenTraceW")
	procProcessTrace              = advapi32.NewProc("ProcessTrace")
	procCloseTrace                = advapi32.NewProc("CloseTrace")
	procTdhGetPropertySize        = tdhDLL.NewProc("TdhGetPropertySize")
	procTdhGetProperty            = tdhDLL.NewProc("TdhGetProperty")
	windowsProcessProviderGUID    = windows.GUID{Data1: 0x3d6fa8d0, Data2: 0xfe05, Data3: 0x11d0, Data4: [8]byte{0x9d, 0xda, 0x00, 0xc0, 0x4f, 0xd7, 0xba, 0x7c}}
	windowsSystemTraceControlGUID = windows.GUID{Data1: 0x9e814aad, Data2: 0x3204, Data3: 0x11d2, Data4: [8]byte{0x9a, 0x82, 0x00, 0x60, 0x08, 0xa8, 0x69, 0x39}}
	windowsETWCallback            = windows.NewCallback(windowsETWEventCallback)
	windowsETWHandlers            = map[uintptr]*windowsETWHandler{}
	windowsETWHandlersMutex       sync.RWMutex
	windowsETWNextHandlerID       uint64 = 1
)

type windowsETWBackend struct {
	interval time.Duration
}

type windowsETWNotice struct {
	kind       EventKind
	pid        int
	parentPID  int
	exitCode   *int
	eventTime  time.Time
	observedAt time.Time
}

type windowsETWHandler struct {
	id      uintptr
	notices chan windowsETWNotice
	lost    atomic.Uint64
}

type windowsETWSession struct {
	name          []uint16
	properties    []uint16
	controlHandle uint64
	traceHandle   uint64
	closeOnce     sync.Once
	closeErr      error
}

type etwWnodeHeader struct {
	BufferSize        uint32
	ProviderID        uint32
	HistoricalContext uint64
	Timestamp         int64
	GUID              windows.GUID
	ClientContext     uint32
	Flags             uint32
}

type etwProperties struct {
	Wnode               etwWnodeHeader
	BufferSize          uint32
	MinimumBuffers      uint32
	MaximumBuffers      uint32
	MaximumFileSize     uint32
	LogFileMode         uint32
	FlushTimer          uint32
	EnableFlags         uint32
	AgeLimit            int32
	NumberOfBuffers     uint32
	FreeBuffers         uint32
	EventsLost          uint32
	BuffersWritten      uint32
	LogBuffersLost      uint32
	RealTimeBuffersLost uint32
	LoggerThreadID      windows.Handle
	LogFileNameOffset   uint32
	LoggerNameOffset    uint32
}

type etwEventTraceHeader struct {
	Size          uint16
	FieldFlags    uint16
	Version       uint32
	ThreadID      uint32
	ProcessID     uint32
	Timestamp     int64
	GUID          windows.GUID
	ProcessorTime uint64
}

type etwEventTrace struct {
	Header           etwEventTraceHeader
	InstanceID       uint32
	ParentInstanceID uint32
	ParentGUID       windows.GUID
	Data             uintptr
	DataLength       uint32
	ClientContext    uint32
}

type etwTraceLogfileHeader struct {
	BufferSize         uint32
	Version            uint32
	ProviderVersion    uint32
	NumberOfProcessors uint32
	EndTime            int64
	TimerResolution    uint32
	MaximumFileSize    uint32
	LogFileMode        uint32
	BuffersWritten     uint32
	LogInstanceGUID    windows.GUID
	LoggerName         uintptr
	LogFileName        uintptr
	Timezone           windows.Timezoneinformation
	BootTime           int64
	PerformanceFreq    int64
	StartTime          int64
	ReservedFlags      uint32
	BuffersLost        uint32
}

type etwTraceLogfile struct {
	LogFileName         uintptr
	LoggerName          uintptr
	CurrentTime         int64
	BuffersRead         uint32
	ProcessTraceMode    uint32
	CurrentEvent        etwEventTrace
	LogfileHeader       etwTraceLogfileHeader
	BufferCallback      uintptr
	BufferSize          uint32
	Filled              uint32
	EventsLost          uint32
	EventRecordCallback uintptr
	IsKernelTrace       uint32
	Context             uintptr
}

type etwEventDescriptor struct {
	ID      uint16
	Version uint8
	Channel uint8
	Level   uint8
	Opcode  uint8
	Task    uint16
	Keyword uint64
}

type etwEventHeader struct {
	Size            uint16
	HeaderType      uint16
	Flags           uint16
	EventProperty   uint16
	ThreadID        uint32
	ProcessID       uint32
	Timestamp       int64
	ProviderID      windows.GUID
	EventDescriptor etwEventDescriptor
	ProcessorTime   uint64
	ActivityID      windows.GUID
}

type etwEventRecord struct {
	EventHeader       etwEventHeader
	BufferContext     uint32
	ExtendedDataCount uint16
	UserDataLength    uint16
	ExtendedData      uintptr
	UserData          unsafe.Pointer
	UserContext       uintptr
}

type etwPropertyDescriptor struct {
	PropertyName uint64
	ArrayIndex   uint32
	Reserved     uint32
}

func (backend windowsETWBackend) Name() string {
	return BackendWindowsETW
}

func (backend windowsETWBackend) Watch(ctx context.Context, events chan<- Event) error {
	handler := newWindowsETWHandler()
	defer handler.unregister()
	session, err := startWindowsETWSession(handler)
	if err != nil {
		return err
	}
	defer session.close()
	processes, err := ReadProcessSnapshot()
	if err != nil {
		return fmt.Errorf("read initial process snapshot after subscribing to Windows ETW: %w", err)
	}
	traceResult := make(chan error, 1)
	go func() {
		traceResult <- session.process()
	}()
	resyncTicker := time.NewTicker(windowsETWResyncInterval(backend.interval))
	defer resyncTicker.Stop()
	var reportedLost uint64
	for {
		select {
		case <-ctx.Done():
			if err := session.close(); err != nil {
				return fmt.Errorf("close Windows ETW process session after cancellation: %w", err)
			}
			return ctx.Err()
		case err := <-traceResult:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err == nil {
				return fmt.Errorf("Windows ETW process trace ended before cancellation")
			}
			return err
		case notice := <-handler.notices:
			processes = applyWindowsETWNotice(ctx, events, processes, notice)
			lost := handler.lost.Load()
			if lost > reportedLost {
				now := time.Now()
				message := fmt.Sprintf("Windows ETW callback queue overflowed; expected every lifecycle notice, dropped %d notices", lost-reportedLost)
				sendEvent(ctx, events, Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: backend.Name(), Message: message})
				reportedLost = lost
				processes = resyncWindowsETW(ctx, events, processes)
			}
		case <-resyncTicker.C:
			processes = resyncWindowsETW(ctx, events, processes)
		}
	}
}

func newWindowsETWHandler() *windowsETWHandler {
	id := uintptr(atomic.AddUint64(&windowsETWNextHandlerID, 1))
	handler := &windowsETWHandler{id: id, notices: make(chan windowsETWNotice, etwMaximumPendingNotices)}
	windowsETWHandlersMutex.Lock()
	windowsETWHandlers[id] = handler
	windowsETWHandlersMutex.Unlock()
	return handler
}

func (handler *windowsETWHandler) unregister() {
	windowsETWHandlersMutex.Lock()
	delete(windowsETWHandlers, handler.id)
	windowsETWHandlersMutex.Unlock()
}

func windowsETWEventCallback(record *etwEventRecord) uintptr {
	if record.EventHeader.ProviderID != windowsProcessProviderGUID {
		return 0
	}
	windowsETWHandlersMutex.RLock()
	handler := windowsETWHandlers[record.UserContext]
	windowsETWHandlersMutex.RUnlock()
	if handler == nil {
		return 0
	}
	notice, ok := decodeWindowsETWNotice(record)
	if !ok {
		return 0
	}
	select {
	case handler.notices <- notice:
	default:
		handler.lost.Add(1)
	}
	return 0
}

func decodeWindowsETWNotice(record *etwEventRecord) (windowsETWNotice, bool) {
	kind := EventKind("")
	switch record.EventHeader.EventDescriptor.Opcode {
	case etwProcessStartOpcode:
		kind = EventStart
	case etwProcessEndOpcode:
		kind = EventStop
	default:
		return windowsETWNotice{}, false
	}
	decodedPID, decodedParent, decodedStatus, decoded := decodeWindowsETWProcessFields(record)
	pid, ok := windowsETWUint32Property(record, "ProcessId")
	if !ok || pid == 0 {
		pid = decodedPID
	}
	if pid == 0 {
		pid = record.EventHeader.ProcessID
	}
	if pid == 0 {
		return windowsETWNotice{}, false
	}
	parent, parentFound := windowsETWUint32Property(record, "ParentId")
	if !parentFound && decoded {
		parent = decodedParent
	}
	observedAt := time.Now()
	notice := windowsETWNotice{
		kind:       kind,
		pid:        int(pid),
		parentPID:  int(parent),
		eventTime:  windowsETWTime(record.EventHeader.Timestamp, observedAt),
		observedAt: observedAt,
	}
	if kind == EventStop {
		status, found := windowsETWUint32Property(record, "ExitStatus")
		if !found && decoded {
			status = decodedStatus
			found = true
		}
		if found {
			code := int(status)
			notice.exitCode = &code
		}
	}
	return notice, true
}

func decodeWindowsETWProcessFields(record *etwEventRecord) (uint32, uint32, uint32, bool) {
	pointerSize := 4
	if record.EventHeader.Flags&0x0040 != 0 {
		pointerSize = 8
	}
	required := pointerSize + 16
	if record.UserData == nil || int(record.UserDataLength) < required {
		return 0, 0, 0, false
	}
	data := unsafe.Slice((*byte)(record.UserData), int(record.UserDataLength))
	pid := binary.LittleEndian.Uint32(data[pointerSize : pointerSize+4])
	parentOffset := pointerSize + 4
	parent := binary.LittleEndian.Uint32(data[parentOffset : parentOffset+4])
	exitOffset := pointerSize + 12
	exitStatus := binary.LittleEndian.Uint32(data[exitOffset : exitOffset+4])
	return pid, parent, exitStatus, true
}

func windowsETWUint32Property(record *etwEventRecord, property string) (uint32, bool) {
	name, err := windows.UTF16FromString(property)
	if err != nil {
		return 0, false
	}
	descriptor := etwPropertyDescriptor{PropertyName: uint64(uintptr(unsafe.Pointer(&name[0]))), ArrayIndex: ^uint32(0)}
	var size uint32
	status, _, _ := procTdhGetPropertySize.Call(
		uintptr(unsafe.Pointer(record)), 0, 0, 1, uintptr(unsafe.Pointer(&descriptor)), uintptr(unsafe.Pointer(&size)),
	)
	if status != 0 || size != 4 {
		return 0, false
	}
	var value uint32
	status, _, _ = procTdhGetProperty.Call(
		uintptr(unsafe.Pointer(record)), 0, 0, 1, uintptr(unsafe.Pointer(&descriptor)), 4, uintptr(unsafe.Pointer(&value)),
	)
	return value, status == 0
}

func windowsETWTime(timestamp int64, fallback time.Time) time.Time {
	if timestamp <= 0 {
		return fallback
	}
	filetime := windows.Filetime{LowDateTime: uint32(timestamp), HighDateTime: uint32(uint64(timestamp) >> 32)}
	value := time.Unix(0, filetime.Nanoseconds())
	if value.After(fallback.Add(time.Minute)) || value.Before(fallback.Add(-24*time.Hour)) {
		return fallback
	}
	return value
}

func startWindowsETWSession(handler *windowsETWHandler) (*windowsETWSession, error) {
	name, err := windows.UTF16FromString(fmt.Sprintf("Bottom Process Logger %d %d", os.Getpid(), handler.id))
	if err != nil {
		return nil, fmt.Errorf("encode Windows ETW session name: %w", err)
	}
	propertyBytes := int(unsafe.Sizeof(etwProperties{}))
	properties := make([]uint16, propertyBytes/2+len(name))
	property := (*etwProperties)(unsafe.Pointer(&properties[0]))
	property.Wnode.BufferSize = uint32(len(properties) * 2)
	property.Wnode.GUID = windowsSystemTraceControlGUID
	property.Wnode.ClientContext = etwClockSystemTime
	property.Wnode.Flags = etwWnodeTracedGUID
	property.LogFileMode = etwRealTimeMode | etwSystemLoggerMode
	property.EnableFlags = etwProcessFlag
	property.FlushTimer = 1
	property.LoggerNameOffset = uint32(propertyBytes)
	copy(properties[propertyBytes/2:], name)
	session := &windowsETWSession{name: name, properties: properties}
	status, _, _ := procStartTraceW.Call(
		uintptr(unsafe.Pointer(&session.controlHandle)), uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(property)),
	)
	if status != 0 {
		return nil, fmt.Errorf("start Windows ETW process session %q: status=%d error=%w", windows.UTF16ToString(name), status, syscall.Errno(status))
	}
	logfile := etwTraceLogfile{
		LoggerName:          uintptr(unsafe.Pointer(&name[0])),
		ProcessTraceMode:    etwRealTimeMode | etwEventRecordMode,
		EventRecordCallback: windowsETWCallback,
		Context:             handler.id,
	}
	traceHandle, _, openErr := procOpenTraceW.Call(uintptr(unsafe.Pointer(&logfile)))
	session.traceHandle = uint64(traceHandle)
	if session.traceHandle == uint64(^uintptr(0)) {
		closeErr := session.close()
		return nil, fmt.Errorf("open Windows ETW process session %q for real-time consumption: error=%v; cleanup=%v", windows.UTF16ToString(name), openErr, closeErr)
	}
	return session, nil
}

func (session *windowsETWSession) process() error {
	handles := []uint64{session.traceHandle}
	status, _, _ := procProcessTrace.Call(uintptr(unsafe.Pointer(&handles[0])), 1, 0, 0)
	if status != 0 && status != uintptr(windows.ERROR_CANCELLED) {
		return fmt.Errorf("consume Windows ETW process session %q: status=%d error=%w", windows.UTF16ToString(session.name), status, syscall.Errno(status))
	}
	return nil
}

func (session *windowsETWSession) close() error {
	session.closeOnce.Do(func() {
		var errs []error
		if session.traceHandle != 0 && session.traceHandle != uint64(^uintptr(0)) {
			status, _, _ := procCloseTrace.Call(uintptr(session.traceHandle))
			if status != 0 && status != uintptr(windows.ERROR_CANCELLED) {
				errs = append(errs, fmt.Errorf("close Windows ETW consumer handle: status=%d error=%w", status, syscall.Errno(status)))
			}
		}
		if session.controlHandle != 0 {
			property := (*etwProperties)(unsafe.Pointer(&session.properties[0]))
			status, _, _ := procStopTraceW.Call(
				uintptr(session.controlHandle), uintptr(unsafe.Pointer(&session.name[0])), uintptr(unsafe.Pointer(property)),
			)
			if status != 0 && status != uintptr(windows.ERROR_WMI_INSTANCE_NOT_FOUND) {
				errs = append(errs, fmt.Errorf("stop Windows ETW process session: status=%d error=%w", status, syscall.Errno(status)))
			}
		}
		session.closeErr = errors.Join(errs...)
	})
	return session.closeErr
}

func applyWindowsETWNotice(ctx context.Context, events chan<- Event, processes ProcessSnapshot, notice windowsETWNotice) ProcessSnapshot {
	switch notice.kind {
	case EventStart:
		proc := readWindowsETWProcess(notice)
		oldProcess, found := findProcessByPID(processes, notice.pid)
		if found && sameProcessGeneration(oldProcess, proc) {
			preserveProcessObservation(oldProcess, &proc)
		} else if found {
			sendEvent(ctx, events, processStopEventObserved(notice.eventTime, notice.observedAt, BackendWindowsETW, oldProcess, processes, nil))
		}
		removeProcessByPID(processes, notice.pid)
		processes[proc.ID] = proc
		sendEvent(ctx, events, processStartEventObserved(notice.eventTime, notice.observedAt, BackendWindowsETW, proc, processes))
	case EventStop:
		proc, found := findProcessByPID(processes, notice.pid)
		if !found {
			id := fmt.Sprintf("etw:%d:%d", notice.pid, notice.eventTime.UnixNano())
			proc = capturedProcess(id, notice.pid, notice.parentPID, "", "", "", "", notice.eventTime, notice.observedAt)
		}
		sendEvent(ctx, events, processStopEventObserved(notice.eventTime, notice.observedAt, BackendWindowsETW, proc, processes, notice.exitCode))
		removeProcessByPID(processes, notice.pid)
	}
	return processes
}

func readWindowsETWProcess(notice windowsETWNotice) Process {
	details := readWindowsProcessDetails(uint32(notice.pid))
	startTime := details.startedAt
	if startTime.IsZero() {
		startTime = notice.eventTime
	}
	startToken := strconv.FormatInt(startTime.UnixNano(), 10)
	proc := capturedProcess(processID(notice.pid, startToken), notice.pid, notice.parentPID, details.command, details.exe, "", details.user, startTime, notice.observedAt)
	proc.UID = details.uid
	var sessionID uint32
	if windows.ProcessIdToSessionId(uint32(notice.pid), &sessionID) == nil {
		proc.Session = strconv.FormatUint(uint64(sessionID), 10)
	}
	return proc
}

func windowsETWResyncInterval(interval time.Duration) time.Duration {
	value := interval * 10
	if value < time.Second {
		return time.Second
	}
	if value > 30*time.Second {
		return 30 * time.Second
	}
	return value
}

func resyncWindowsETW(ctx context.Context, events chan<- Event, previous ProcessSnapshot) ProcessSnapshot {
	next, err := ReadProcessSnapshot()
	if err != nil {
		now := time.Now()
		message := fmt.Sprintf("Windows ETW resync failed; expected a complete process table, received error %v", err)
		sendEvent(ctx, events, Event{Kind: EventGap, Time: now, ObservedAt: now, Backend: BackendWindowsETW, Message: message})
		return previous
	}
	emitSnapshotDiff(ctx, BackendWindowsETW, previous, next, events)
	return next
}
