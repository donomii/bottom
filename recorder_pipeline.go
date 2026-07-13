package main

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	defaultRecorderBufferSize = 1024
	defaultSQLiteBatchSize    = 128
	defaultRecorderFlush      = 250 * time.Millisecond
)

var (
	errRecorderBackpressure = errors.New("recorder buffer has no available capacity")
	errRecorderClosed       = errors.New("recorder is closed")
)

type recorderOptions struct {
	bufferSize      int
	sqliteBatchSize int
	flushInterval   time.Duration
	retention       time.Duration
	rotation        rotationOptions
	redact          []string
	tuiStop         func()
}

type rotationOptions struct {
	maxBytes int64
	interval time.Duration
}

func defaultRecorderOptions() recorderOptions {
	return recorderOptions{
		bufferSize:      defaultRecorderBufferSize,
		sqliteBatchSize: defaultSQLiteBatchSize,
		flushInterval:   defaultRecorderFlush,
	}
}

func recorderOptionsFromConfig(config Config) recorderOptions {
	options := defaultRecorderOptions()
	if config.RecorderBuffer != 0 {
		options.bufferSize = config.RecorderBuffer
	}
	if config.SQLiteBatch != 0 {
		options.sqliteBatchSize = config.SQLiteBatch
	}
	if config.SQLiteFlush != 0 {
		options.flushInterval = config.SQLiteFlush
	}
	options.retention = config.Retention
	options.rotation = rotationOptions{maxBytes: config.RotateSize, interval: config.RotateInterval}
	options.redact = append([]string(nil), config.Redact...)
	return options
}

func normalizeRecorderOptions(options recorderOptions) recorderOptions {
	defaults := defaultRecorderOptions()
	if options.bufferSize == 0 {
		options.bufferSize = defaults.bufferSize
	}
	if options.sqliteBatchSize == 0 {
		options.sqliteBatchSize = defaults.sqliteBatchSize
	}
	if options.flushInterval == 0 {
		options.flushInterval = defaults.flushInterval
	}
	return options
}

func validateRecorderOptions(options recorderOptions) error {
	if options.bufferSize < -1 {
		return fmt.Errorf("recorder buffer must be positive or -1 to disable buffering, received %d", options.bufferSize)
	}
	if options.sqliteBatchSize <= 0 {
		return fmt.Errorf("sqlite batch size must be positive, received %d", options.sqliteBatchSize)
	}
	if options.flushInterval <= 0 {
		return fmt.Errorf("recorder flush interval must be positive, received %s", options.flushInterval)
	}
	if options.retention < 0 {
		return fmt.Errorf("recording retention must not be negative, received %s", options.retention)
	}
	if options.rotation.maxBytes < 0 {
		return fmt.Errorf("recording rotation size must not be negative, received %d", options.rotation.maxBytes)
	}
	if options.rotation.interval < 0 {
		return fmt.Errorf("recording rotation interval must not be negative, received %s", options.rotation.interval)
	}
	for index, pattern := range options.redact {
		if pattern == "" {
			return fmt.Errorf("redaction pattern %d must be non-empty", index+1)
		}
	}
	return nil
}

func (options rotationOptions) enabled() bool {
	return options.maxBytes > 0 || options.interval > 0
}

type sessionRecorder struct {
	target   Recorder
	session  recordingSession
	mutex    sync.Mutex
	sequence uint64
}

func newSessionRecorder(target Recorder, session recordingSession) Recorder {
	return &sessionRecorder{target: target, session: session}
}

func (recorder *sessionRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if event.SchemaVersion == 0 {
		event.SchemaVersion = EventSchemaVersion
	}
	if event.SessionID == "" {
		event.SessionID = recorder.session.ID
	}
	if event.Sequence == 0 {
		recorder.sequence++
		event.Sequence = recorder.sequence
	} else if event.Sequence > recorder.sequence {
		recorder.sequence = event.Sequence
	}
	if event.Host == "" {
		event.Host = recorder.session.Hostname
	}
	if event.BootID == "" {
		event.BootID = recorder.session.BootID
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now().UTC()
	}
	return recorder.target.Write(event)
}

func (recorder *sessionRecorder) Close() error {
	return recorder.target.Close()
}

func (recorder *sessionRecorder) Flush() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return flushRecorderTarget(recorder.target)
}

type redactingRecorder struct {
	target   Recorder
	patterns []string
}

func newRedactingRecorder(target Recorder, patterns []string) Recorder {
	return redactingRecorder{target: target, patterns: append([]string(nil), patterns...)}
}

func (recorder redactingRecorder) Write(event Event) error {
	return recorder.target.Write(redactEvent(event, recorder.patterns))
}

func (recorder redactingRecorder) Close() error {
	return recorder.target.Close()
}

func (recorder redactingRecorder) Flush() error {
	return flushRecorderTarget(recorder.target)
}

type filteringRecorder struct {
	target Recorder
	filter Filter
}

func newFilteringRecorder(target Recorder, filter Filter) Recorder {
	return filteringRecorder{target: target, filter: filter}
}

func (recorder filteringRecorder) Write(event Event) error {
	if event.Kind != EventGap && !recorder.filter.Accepts(event) {
		return nil
	}
	return recorder.target.Write(event)
}

func (recorder filteringRecorder) Close() error {
	return recorder.target.Close()
}

func (recorder filteringRecorder) Flush() error {
	return flushRecorderTarget(recorder.target)
}

func redactEvent(event Event, patterns []string) Event {
	event.Host = redactString(event.Host, patterns)
	event.BootID = redactString(event.BootID, patterns)
	event.ProcessID = redactString(event.ProcessID, patterns)
	event.Command = redactString(event.Command, patterns)
	event.Exe = redactString(event.Exe, patterns)
	event.Cwd = redactString(event.Cwd, patterns)
	event.User = redactString(event.User, patterns)
	event.UID = redactString(event.UID, patterns)
	event.TTY = redactString(event.TTY, patterns)
	event.Session = redactString(event.Session, patterns)
	event.Cgroup = redactString(event.Cgroup, patterns)
	event.SystemdUnit = redactString(event.SystemdUnit, patterns)
	event.ContainerID = redactString(event.ContainerID, patterns)
	event.Message = redactString(event.Message, patterns)
	parents := make([]ProcessSummary, len(event.ParentChain))
	copy(parents, event.ParentChain)
	for index := range parents {
		parents[index].ProcessID = redactString(parents[index].ProcessID, patterns)
		parents[index].Command = redactString(parents[index].Command, patterns)
		parents[index].Exe = redactString(parents[index].Exe, patterns)
		parents[index].User = redactString(parents[index].User, patterns)
	}
	event.ParentChain = parents
	return event
}

func redactRecordingSession(session recordingSession, patterns []string) recordingSession {
	session.Hostname = redactString(session.Hostname, patterns)
	session.BootID = redactString(session.BootID, patterns)
	return session
}

func redactString(value string, patterns []string) string {
	for _, pattern := range patterns {
		value = strings.ReplaceAll(value, pattern, "[REDACTED]")
	}
	return value
}

type multiRecorder struct {
	recorders []Recorder
	mutex     sync.Mutex
	closed    bool
}

func newMultiRecorder(recorders ...Recorder) Recorder {
	return &multiRecorder{recorders: append([]Recorder(nil), recorders...)}
}

func (recorder *multiRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("write event kind=%s pid=%d to recording sinks: %w", event.Kind, event.PID, errRecorderClosed)
	}
	errs := []error{}
	for index, target := range recorder.recorders {
		if err := target.Write(event); err != nil {
			errs = append(errs, fmt.Errorf("write event kind=%s pid=%d to recording sink %d: %w", event.Kind, event.PID, index+1, err))
		}
	}
	return joinRecorderErrors(errs...)
}

func (recorder *multiRecorder) Close() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return nil
	}
	recorder.closed = true
	errs := []error{}
	for index, target := range recorder.recorders {
		if err := target.Close(); err != nil {
			errs = append(errs, fmt.Errorf("close recording sink %d: %w", index+1, err))
		}
	}
	return joinRecorderErrors(errs...)
}

type recorderFlusher interface {
	Flush() error
}

func flushRecorderTarget(target Recorder) error {
	flusher, ok := target.(recorderFlusher)
	if !ok {
		return nil
	}
	return flusher.Flush()
}

type bufferedRecorder struct {
	target     Recorder
	queue      chan Event
	flushEvery time.Duration
	done       chan struct{}
	closeDone  chan struct{}
	mutex      sync.Mutex
	closed     bool
	failure    error
	closeErr   error
	closeOnce  sync.Once
}

func newBufferedRecorder(target Recorder, capacity int, flushEvery time.Duration) Recorder {
	if capacity <= 0 {
		return target
	}
	recorder := &bufferedRecorder{
		target:     target,
		queue:      make(chan Event, capacity),
		flushEvery: flushEvery,
		done:       make(chan struct{}),
		closeDone:  make(chan struct{}),
	}
	go recorder.run()
	return recorder
}

func (recorder *bufferedRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("record event kind=%s pid=%d: %w", event.Kind, event.PID, errRecorderClosed)
	}
	if recorder.failure != nil {
		return fmt.Errorf("record event kind=%s pid=%d after recording sink failure: %w", event.Kind, event.PID, recorder.failure)
	}
	select {
	case recorder.queue <- event:
		return nil
	default:
		return fmt.Errorf(
			"record event kind=%s pid=%d: %w; capacity=%d queued=%d and this event was not recorded",
			event.Kind, event.PID, errRecorderBackpressure, cap(recorder.queue), len(recorder.queue),
		)
	}
}

func (recorder *bufferedRecorder) Close() error {
	recorder.closeOnce.Do(func() {
		recorder.mutex.Lock()
		recorder.closed = true
		close(recorder.queue)
		recorder.mutex.Unlock()
		<-recorder.done
		closeErr := recorder.target.Close()
		recorder.mutex.Lock()
		recorder.closeErr = joinRecorderErrors(recorder.failure, closeErr)
		recorder.mutex.Unlock()
		close(recorder.closeDone)
	})
	<-recorder.closeDone
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.closeErr
}

func (recorder *bufferedRecorder) run() {
	defer close(recorder.done)
	var ticker *time.Ticker
	var ticks <-chan time.Time
	if recorder.flushEvery > 0 {
		ticker = time.NewTicker(recorder.flushEvery)
		ticks = ticker.C
		defer ticker.Stop()
	}
	for {
		select {
		case event, open := <-recorder.queue:
			if !open {
				recorder.flushTarget()
				return
			}
			if recorder.currentFailure() == nil {
				if err := recorder.target.Write(event); err != nil {
					recorder.setFailure(fmt.Errorf("write buffered event kind=%s pid=%d: %w", event.Kind, event.PID, err))
				}
			}
		case <-ticks:
			recorder.flushTarget()
		}
	}
}

func (recorder *bufferedRecorder) flushTarget() {
	if recorder.currentFailure() != nil {
		return
	}
	flusher, ok := recorder.target.(recorderFlusher)
	if !ok {
		return
	}
	if err := flusher.Flush(); err != nil {
		recorder.setFailure(fmt.Errorf("flush buffered recording sink: %w", err))
	}
}

func (recorder *bufferedRecorder) currentFailure() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.failure
}

func (recorder *bufferedRecorder) setFailure(err error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.failure == nil {
		recorder.failure = err
	}
}
