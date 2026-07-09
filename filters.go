package main

import (
	"strconv"
	"strings"
	"time"
)

func (filter Filter) Accepts(event Event) bool {
	if !filter.acceptsKind(event.Kind) {
		return false
	}
	if filter.User != "" && event.User != filter.User {
		return false
	}
	if filter.CwdContains != "" && !strings.Contains(event.Cwd, filter.CwdContains) {
		return false
	}
	if filter.ExeContains != "" && !strings.Contains(event.Exe, filter.ExeContains) {
		return false
	}
	if filter.ParentPID != 0 && event.ParentPID != filter.ParentPID {
		return false
	}
	if event.Kind == EventStop && filter.MinDuration > 0 && time.Duration(event.DurationMillis)*time.Millisecond < filter.MinDuration {
		return false
	}
	if event.Kind == EventStop && filter.MaxDuration > 0 && time.Duration(event.DurationMillis)*time.Millisecond > filter.MaxDuration {
		return false
	}
	if len(filter.Include) > 0 && !containsOne(eventSearchText(event), filter.Include) {
		return false
	}
	if len(filter.Exclude) > 0 && containsOne(eventSearchText(event), filter.Exclude) {
		return false
	}
	return true
}

func (filter Filter) acceptsKind(kind EventKind) bool {
	if filter.EventMode == "" || filter.EventMode == EventModeBoth {
		return kind == EventStart || kind == EventStop || kind == EventChurn || kind == EventGap
	}
	return string(kind) == filter.EventMode
}

func containsOne(text string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(text, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

func eventSearchText(event Event) string {
	parts := []string{
		event.Command,
		event.Exe,
		event.Cwd,
		event.User,
		strconv.Itoa(event.PID),
		strconv.Itoa(event.ParentPID),
		event.Message,
	}
	for _, parent := range event.ParentChain {
		parts = append(parts, parent.Command, parent.Exe, parent.User, strconv.Itoa(parent.PID))
	}
	return strings.ToLower(strings.Join(parts, "\n"))
}
