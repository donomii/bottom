package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type recorderFactory func(string) (Recorder, error)

type rotatingRecorder struct {
	path       string
	options    rotationOptions
	factory    recorderFactory
	current    Recorder
	openedAt   time.Time
	generation int
	mutex      sync.Mutex
	closed     bool
}

func newRotatingRecorder(path string, options rotationOptions, factory recorderFactory) (Recorder, error) {
	current, err := factory(path)
	if err != nil {
		return nil, err
	}
	return &rotatingRecorder{
		path:     path,
		options:  options,
		factory:  factory,
		current:  current,
		openedAt: time.Now(),
	}, nil
}

func (recorder *rotatingRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("record event kind=%s pid=%d to rotating output %q: %w", event.Kind, event.PID, recorder.path, errRecorderClosed)
	}
	rotate, err := recorder.shouldRotate(time.Now())
	if err != nil {
		return err
	}
	if rotate {
		if err := recorder.rotate(); err != nil {
			return err
		}
	}
	if err := recorder.current.Write(event); err != nil {
		return fmt.Errorf("write event kind=%s pid=%d to current output segment %q: %w", event.Kind, event.PID, recorder.path, err)
	}
	return nil
}

func (recorder *rotatingRecorder) Flush() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.current == nil {
		return nil
	}
	flusher, ok := recorder.current.(recorderFlusher)
	if !ok {
		return nil
	}
	if err := flusher.Flush(); err != nil {
		return fmt.Errorf("flush current output segment %q: %w", recorder.path, err)
	}
	return nil
}

func (recorder *rotatingRecorder) Close() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return nil
	}
	recorder.closed = true
	if recorder.current == nil {
		return nil
	}
	if err := recorder.current.Close(); err != nil {
		return fmt.Errorf("close current output segment %q: %w", recorder.path, err)
	}
	return nil
}

func (recorder *rotatingRecorder) shouldRotate(now time.Time) (bool, error) {
	if recorder.options.interval > 0 && now.Sub(recorder.openedAt) >= recorder.options.interval {
		return true, nil
	}
	if recorder.options.maxBytes == 0 {
		return false, nil
	}
	info, err := os.Stat(recorder.path)
	if err != nil {
		return false, fmt.Errorf("read output segment size for rotation at %q: %w", recorder.path, err)
	}
	return info.Size() >= recorder.options.maxBytes, nil
}

func (recorder *rotatingRecorder) rotate() error {
	if err := recorder.current.Close(); err != nil {
		return fmt.Errorf("close output segment %q before rotation: %w", recorder.path, err)
	}
	recorder.current = nil
	rotatedPath, err := recorder.nextRotatedPath()
	if err != nil {
		reopenErr := recorder.reopenCurrent()
		return joinRecorderErrors(err, reopenErr)
	}
	if err := os.Rename(recorder.path, rotatedPath); err != nil {
		reopenErr := recorder.reopenCurrent()
		return joinRecorderErrors(fmt.Errorf("rotate output %q to %q: %w", recorder.path, rotatedPath, err), reopenErr)
	}
	next, err := recorder.factory(recorder.path)
	if err != nil {
		restoreErr := os.Rename(rotatedPath, recorder.path)
		if restoreErr != nil {
			restoreErr = fmt.Errorf("restore output segment %q after replacement open failed: %w", rotatedPath, restoreErr)
		} else {
			restoreErr = recorder.reopenCurrent()
		}
		return joinRecorderErrors(fmt.Errorf("open replacement output segment %q: %w", recorder.path, err), restoreErr)
	}
	recorder.current = next
	recorder.openedAt = time.Now()
	return nil
}

func (recorder *rotatingRecorder) reopenCurrent() error {
	current, err := recorder.factory(recorder.path)
	if err != nil {
		return fmt.Errorf("reopen current output segment %q after rotation failed: %w", recorder.path, err)
	}
	recorder.current = current
	return nil
}

func (recorder *rotatingRecorder) nextRotatedPath() (string, error) {
	for {
		recorder.generation++
		candidate := rotatedOutputPath(recorder.path, recorder.openedAt, recorder.generation)
		_, err := os.Stat(candidate)
		if os.IsNotExist(err) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("check rotated output path %q: %w", candidate, err)
		}
	}
}

func rotatedOutputPath(path string, openedAt time.Time, generation int) string {
	extension := filepath.Ext(path)
	base := strings.TrimSuffix(path, extension)
	timestamp := openedAt.UTC().Format("20060102T150405.000000000Z")
	return base + "." + timestamp + "." + strconv.Itoa(generation) + extension
}
