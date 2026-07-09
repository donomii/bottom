package main

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func runSelfTest() error {
	now := time.Date(2026, 7, 8, 12, 0, 0, 0, time.UTC)
	event := Event{
		Kind:      EventStart,
		Time:      now,
		PID:       101,
		ParentPID: 10,
		Command:   "compiler --input main.go",
		Exe:       "/usr/bin/compiler",
		Cwd:       "/tmp/build",
		User:      "jer",
		Backend:   BackendPoll,
	}
	if !(Filter{Include: []string{"compiler"}, EventMode: EventModeBoth}).Accepts(event) {
		return fmt.Errorf("self-test filter include: expected compiler event to pass")
	}
	if (Filter{Exclude: []string{"compiler"}, EventMode: EventModeBoth}).Accepts(event) {
		return fmt.Errorf("self-test filter exclude: expected compiler event to be hidden")
	}
	if err := selfTestRecorders(event); err != nil {
		return err
	}
	if err := selfTestChurn(now, event); err != nil {
		return err
	}
	if err := selfTestSnapshotDiff(now); err != nil {
		return err
	}
	fmt.Println("self-test ok")
	return nil
}

func selfTestRecorders(event Event) error {
	var text bytes.Buffer
	textRec := textRecorder{writer: &text}
	if err := textRec.Write(event); err != nil {
		return err
	}
	if !strings.Contains(text.String(), "compiler") {
		return fmt.Errorf("self-test text recorder: expected command in output, received %q", text.String())
	}
	var jsonBuffer bytes.Buffer
	if err := (jsonlRecorder{encoder: json.NewEncoder(&jsonBuffer)}).Write(event); err != nil {
		return err
	}
	if !strings.Contains(jsonBuffer.String(), `"kind":"start"`) {
		return fmt.Errorf("self-test jsonl recorder: expected start kind in output, received %q", jsonBuffer.String())
	}
	var csvBuffer bytes.Buffer
	csvWriter := csv.NewWriter(&csvBuffer)
	csvRec := csvRecorder{writer: csvWriter}
	if err := csvRec.Write(event); err != nil {
		return err
	}
	if !strings.Contains(csvBuffer.String(), "compiler") {
		return fmt.Errorf("self-test csv recorder: expected command in output, received %q", csvBuffer.String())
	}
	sqlRecorder, err := newSQLiteRecorder(":memory:")
	if err != nil {
		return err
	}
	defer sqlRecorder.Close()
	if err := sqlRecorder.Write(event); err != nil {
		return err
	}
	return nil
}

func selfTestChurn(now time.Time, event Event) error {
	detector := NewChurnDetector(time.Second, 2)
	if _, ok := detector.Observe(event); ok {
		return fmt.Errorf("self-test churn detector: expected first start to stay below threshold")
	}
	event.Time = now.Add(500 * time.Millisecond)
	churnEvent, ok := detector.Observe(event)
	if !ok {
		return fmt.Errorf("self-test churn detector: expected second start to report churn")
	}
	if churnEvent.Kind != EventChurn || churnEvent.Count != 2 {
		return fmt.Errorf("self-test churn detector: expected churn count 2, received kind=%s count=%d", churnEvent.Kind, churnEvent.Count)
	}
	return nil
}

func selfTestSnapshotDiff(now time.Time) error {
	previous := ProcessSnapshot{
		"1": capturedProcess("1", 1, 0, "parent", "/bin/parent", "/", "jer", time.Time{}, now),
	}
	next := ProcessSnapshot{
		"1": capturedProcess("1", 1, 0, "parent", "/bin/parent", "/", "jer", time.Time{}, now),
		"2": capturedProcess("2", 2, 1, "child", "/bin/child", "/", "jer", time.Time{}, now),
	}
	events := make(chan Event, 1)
	emitSnapshotDiff(context.Background(), BackendPoll, previous, next, events)
	select {
	case event := <-events:
		if event.Kind != EventStart || event.PID != 2 || len(event.ParentChain) != 1 {
			return fmt.Errorf("self-test snapshot diff: expected child start with parent chain, received kind=%s pid=%d parents=%d", event.Kind, event.PID, len(event.ParentChain))
		}
	default:
		return fmt.Errorf("self-test snapshot diff: expected one start event")
	}
	return nil
}
