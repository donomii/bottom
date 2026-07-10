package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sync"
	"time"
)

const recordingSchemaVersion = 4

const (
	recordTypeSession = "session"
	recordTypeEvent   = "event"
	recordTypeGap     = "gap"
)

type recordingSession struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"id"`
	StartedAt     time.Time  `json:"started_at"`
	EndedAt       *time.Time `json:"ended_at,omitempty"`
	Hostname      string     `json:"hostname"`
	BootID        string     `json:"boot_id,omitempty"`
	OS            string     `json:"os"`
	Arch          string     `json:"arch"`
	Backend       string     `json:"backend"`
}

type recordingSessionState struct {
	metadata recordingSession
	mutex    sync.Mutex
	ended    bool
}

type jsonRecordingRecord struct {
	RecordType             string            `json:"record_type"`
	RecordingSchemaVersion int               `json:"recording_schema_version"`
	RecordingSessionID     string            `json:"recording_session_id"`
	SessionState           string            `json:"session_state,omitempty"`
	Session                *recordingSession `json:"session,omitempty"`
	*Event
}

func newRecordingSession(backend string) (recordingSession, error) {
	id, err := newRecordingSessionID()
	if err != nil {
		return recordingSession{}, err
	}
	hostname, err := os.Hostname()
	if err != nil {
		return recordingSession{}, fmt.Errorf("read hostname for recording session metadata: %w", err)
	}
	return recordingSession{
		SchemaVersion: recordingSchemaVersion,
		ID:            id,
		StartedAt:     time.Now().UTC(),
		Hostname:      hostname,
		BootID:        readBootID(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Backend:       backend,
	}, nil
}

func newRecordingSessionID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate recording session id from system randomness: %w", err)
	}
	bytes[6] = (bytes[6] & 0x0f) | 0x40
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	encoded := make([]byte, 32)
	hex.Encode(encoded, bytes)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func newRecordingSessionState(session recordingSession) *recordingSessionState {
	return &recordingSessionState{metadata: session}
}

func (state *recordingSessionState) end() (recordingSession, bool) {
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.ended {
		return recordingSession{}, false
	}
	state.ended = true
	endedAt := time.Now().UTC()
	session := state.metadata
	session.EndedAt = &endedAt
	return session, true
}

func recordingRecordType(event Event) string {
	if event.Kind == EventGap {
		return recordTypeGap
	}
	return recordTypeEvent
}

func joinRecorderErrors(errs ...error) error {
	return errors.Join(errs...)
}
