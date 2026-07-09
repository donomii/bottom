package main

import "time"

type ChurnDetector struct {
	window    time.Duration
	threshold int
	starts    map[string][]time.Time
}

func NewChurnDetector(window time.Duration, threshold int) ChurnDetector {
	return ChurnDetector{
		window:    window,
		threshold: threshold,
		starts:    map[string][]time.Time{},
	}
}

func (detector ChurnDetector) Observe(event Event) (Event, bool) {
	if event.Kind != EventStart || event.Command == "" {
		return Event{}, false
	}
	cutoff := event.Time.Add(-detector.window)
	kept := []time.Time{}
	for _, seenAt := range detector.starts[event.Command] {
		if !seenAt.Before(cutoff) {
			kept = append(kept, seenAt)
		}
	}
	kept = append(kept, event.Time)
	detector.starts[event.Command] = kept
	if len(kept) != detector.threshold {
		return Event{}, false
	}
	return Event{
		Kind:         EventChurn,
		Time:         event.Time,
		Command:      event.Command,
		Exe:          event.Exe,
		User:         event.User,
		Backend:      event.Backend,
		Count:        len(kept),
		WindowMillis: detector.window.Milliseconds(),
		Message:      "command started repeatedly inside the churn window",
	}, true
}
