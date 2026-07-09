package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

type TUIRecorder struct {
	writer io.Writer
	events []Event
	counts map[string]int
}

func NewTUIRecorder(writer io.Writer) *TUIRecorder {
	return &TUIRecorder{
		writer: writer,
		events: []Event{},
		counts: map[string]int{},
	}
}

func (recorder *TUIRecorder) Write(event Event) error {
	if event.Command != "" && event.Kind == EventStart {
		recorder.counts[event.Command]++
	}
	recorder.events = append(recorder.events, event)
	if len(recorder.events) > 18 {
		recorder.events = recorder.events[len(recorder.events)-18:]
	}
	_, err := fmt.Fprint(recorder.writer, recorder.render())
	if err != nil {
		return fmt.Errorf("write tui frame: %w", err)
	}
	return nil
}

func (recorder *TUIRecorder) Close() error {
	return nil
}

func (recorder *TUIRecorder) render() string {
	var builder strings.Builder
	builder.WriteString("\033[H\033[2J")
	builder.WriteString("bottom process lifecycle monitor\n")
	builder.WriteString("time            kind   pid      user        command\n")
	builder.WriteString("-------------------------------------------------------------\n")
	for _, event := range recorder.events {
		builder.WriteString(tuiEventLine(event))
		builder.WriteByte('\n')
	}
	builder.WriteString("\nTop churners in this session\n")
	for _, item := range recorder.topChurners(8) {
		builder.WriteString(fmt.Sprintf("%5d  %s\n", item.count, item.command))
	}
	return builder.String()
}

func tuiEventLine(event Event) string {
	user := event.User
	if user == "" {
		user = "-"
	}
	command := event.Command
	if event.Kind == EventGap {
		command = event.Message
	}
	if event.Kind == EventChurn {
		command = fmt.Sprintf("%s (%d starts in %s)", event.Command, event.Count, time.Duration(event.WindowMillis)*time.Millisecond)
	}
	return fmt.Sprintf("%-15s %-6s %-8d %-11s %s", event.Time.Format("15:04:05.000"), event.Kind, event.PID, truncate(user, 11), truncate(command, 90))
}

type churnItem struct {
	command string
	count   int
}

func (recorder *TUIRecorder) topChurners(limit int) []churnItem {
	items := []churnItem{}
	for command, count := range recorder.counts {
		items = append(items, churnItem{command: command, count: count})
	}
	sort.Slice(items, func(i int, j int) bool {
		if items[i].count == items[j].count {
			return items[i].command < items[j].command
		}
		return items[i].count > items[j].count
	})
	if len(items) > limit {
		return items[:limit]
	}
	return items
}

func truncate(text string, width int) string {
	if len(text) <= width {
		return text
	}
	if width <= 3 {
		return text[:width]
	}
	return text[:width-3] + "..."
}
