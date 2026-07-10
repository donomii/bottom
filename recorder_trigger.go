package main

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

type eventTrigger struct {
	kind    string
	pattern *regexp.Regexp
}

type triggeredRecorder struct {
	target      Recorder
	capacity    int
	postTrigger time.Duration
	trigger     eventTrigger
	mutex       sync.Mutex
	ring        []Event
	next        int
	full        bool
	activeUntil time.Time
	closed      bool
}

func newEventTrigger(value string) (eventTrigger, error) {
	switch value {
	case "churn", "gap", "failed-exit":
		return eventTrigger{kind: value}, nil
	default:
		if !strings.HasPrefix(value, "regex:") {
			return eventTrigger{}, fmt.Errorf("trigger must be churn, gap, failed-exit, or regex:EXPRESSION, received %q", value)
		}
		expression := strings.TrimPrefix(value, "regex:")
		if expression == "" {
			return eventTrigger{}, fmt.Errorf("trigger regular expression must be non-empty")
		}
		pattern, err := regexp.Compile(expression)
		if err != nil {
			return eventTrigger{}, fmt.Errorf("compile trigger regular expression %q: %w", expression, err)
		}
		return eventTrigger{kind: "regex", pattern: pattern}, nil
	}
}

func newTriggeredRecorder(target Recorder, capacity int, postTrigger time.Duration, trigger eventTrigger) Recorder {
	return &triggeredRecorder{target: target, capacity: capacity, postTrigger: postTrigger, trigger: trigger, ring: make([]Event, 0, capacity)}
}

func (recorder *triggeredRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("write triggered event kind=%s pid=%d: %w", event.Kind, event.PID, errRecorderClosed)
	}
	eventTime := event.Time
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	if !recorder.activeUntil.IsZero() && !eventTime.After(recorder.activeUntil) {
		if recorder.trigger.matches(event) {
			recorder.activeUntil = eventTime.Add(recorder.postTrigger)
		}
		return recorder.target.Write(event)
	}
	if !recorder.activeUntil.IsZero() {
		recorder.activeUntil = time.Time{}
	}
	recorder.appendRing(event)
	if !recorder.trigger.matches(event) {
		return nil
	}
	for _, buffered := range recorder.bufferedEvents() {
		if err := recorder.target.Write(buffered); err != nil {
			return fmt.Errorf("flush triggered ring buffer at event kind=%s pid=%d: %w", event.Kind, event.PID, err)
		}
	}
	recorder.ring = recorder.ring[:0]
	recorder.next = 0
	recorder.full = false
	recorder.activeUntil = eventTime.Add(recorder.postTrigger)
	return nil
}

func (recorder *triggeredRecorder) appendRing(event Event) {
	if len(recorder.ring) < recorder.capacity {
		recorder.ring = append(recorder.ring, event)
		if len(recorder.ring) == recorder.capacity {
			recorder.full = true
		}
		return
	}
	recorder.ring[recorder.next] = event
	recorder.next = (recorder.next + 1) % recorder.capacity
}

func (recorder *triggeredRecorder) bufferedEvents() []Event {
	if !recorder.full || recorder.next == 0 {
		return recorder.ring
	}
	ordered := make([]Event, 0, len(recorder.ring))
	ordered = append(ordered, recorder.ring[recorder.next:]...)
	ordered = append(ordered, recorder.ring[:recorder.next]...)
	return ordered
}

func (recorder *triggeredRecorder) Close() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return nil
	}
	recorder.closed = true
	recorder.ring = nil
	return recorder.target.Close()
}

func (recorder *triggeredRecorder) Flush() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("flush triggered recorder: %w", errRecorderClosed)
	}
	return flushRecorderTarget(recorder.target)
}

func (trigger eventTrigger) matches(event Event) bool {
	switch trigger.kind {
	case "churn":
		return event.Kind == EventChurn
	case "gap":
		return event.Kind == EventGap
	case "failed-exit":
		return event.Kind == EventStop && event.ExitCode != nil && *event.ExitCode != 0
	case "regex":
		return trigger.pattern.MatchString(eventSearchTextOriginal(event))
	default:
		return false
	}
}
