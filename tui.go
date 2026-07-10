package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	tuiVisibleEvents = 18
	tuiEventLimit    = 2048
	tuiCountLimit    = 4096
)

type TUIRecorder struct {
	writer      io.Writer
	events      []Event
	counts      map[string]int
	mutex       sync.Mutex
	paused      bool
	scroll      int
	search      string
	detail      bool
	help        bool
	gapCount    int
	backend     string
	status      string
	interactive bool
	entered     bool
	closed      bool
}

func NewTUIRecorder(writer io.Writer) *TUIRecorder {
	recorder := &TUIRecorder{writer: writer, events: []Event{}, counts: map[string]int{}, interactive: isTerminalWriter(writer)}
	if recorder.interactive {
		go recorder.readCommands(os.Stdin)
	}
	return recorder
}

func (recorder *TUIRecorder) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("write tui event kind=%s pid=%d: recorder is closed", event.Kind, event.PID)
	}
	if event.Backend != "" {
		recorder.backend = event.Backend
	}
	if event.Kind == EventGap {
		recorder.gapCount++
	}
	if event.Kind == EventStart {
		recorder.incrementCount(tuiProcessGroup(event))
	}
	recorder.events = append(recorder.events, event)
	if len(recorder.events) > tuiEventLimit {
		recorder.events = append([]Event(nil), recorder.events[len(recorder.events)-tuiEventLimit:]...)
	}
	if recorder.paused {
		return nil
	}
	return recorder.writeFrameLocked()
}

func (recorder *TUIRecorder) Close() error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return nil
	}
	recorder.closed = true
	if recorder.interactive && recorder.entered {
		if _, err := fmt.Fprint(recorder.writer, "\033[?25h\033[?1049l"); err != nil {
			return fmt.Errorf("restore terminal after tui recording: %w", err)
		}
	}
	return nil
}

func (recorder *TUIRecorder) readCommands(reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		recorder.handleCommand(scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		recorder.mutex.Lock()
		recorder.status = fmt.Sprintf("input error: %v", err)
		_ = recorder.writeFrameLocked()
		recorder.mutex.Unlock()
	}
}

func (recorder *TUIRecorder) handleCommand(command string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return
	}
	trimmed := strings.TrimSpace(command)
	switch {
	case trimmed == "p":
		recorder.paused = !recorder.paused
		if recorder.paused {
			recorder.status = "timeline paused"
		} else {
			recorder.status = "timeline resumed"
		}
	case trimmed == "k" || trimmed == "up":
		if recorder.scroll < len(recorder.filteredEvents())-1 {
			recorder.scroll++
		}
	case trimmed == "j" || trimmed == "down":
		if recorder.scroll > 0 {
			recorder.scroll--
		}
	case trimmed == "d":
		recorder.detail = !recorder.detail
	case trimmed == "?":
		recorder.help = !recorder.help
	case trimmed == "clear":
		recorder.search = ""
		recorder.scroll = 0
	case strings.HasPrefix(trimmed, "/"):
		recorder.search = strings.TrimPrefix(trimmed, "/")
		recorder.scroll = 0
	case trimmed != "":
		recorder.status = fmt.Sprintf("unknown command %q; enter ? for help", trimmed)
	}
	_ = recorder.writeFrameLocked()
}

func (recorder *TUIRecorder) render() string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.renderLocked()
}

func (recorder *TUIRecorder) writeFrameLocked() error {
	_, err := fmt.Fprint(recorder.writer, recorder.renderLocked())
	if err != nil {
		return fmt.Errorf("write tui frame: %w", err)
	}
	return nil
}

func (recorder *TUIRecorder) renderLocked() string {
	var builder strings.Builder
	if recorder.interactive && !recorder.entered {
		builder.WriteString("\033[?1049h\033[?25l")
		recorder.entered = true
	}
	builder.WriteString("\033[H\033[2J")
	builder.WriteString("bottom process lifecycle flight recorder\n")
	builder.WriteString(fmt.Sprintf("backend=%s coverage_gaps=%d paused=%t search=%q scroll=%d\n", sanitizeTerminalText(valueOrDash(recorder.backend)), recorder.gapCount, recorder.paused, recorder.search, recorder.scroll))
	builder.WriteString("time            kind   pid      user        command\n")
	builder.WriteString("--------------------------------------------------------------------------------\n")
	visible, selected := recorder.visibleEvents()
	for _, event := range visible {
		builder.WriteString(tuiEventLine(event))
		builder.WriteByte('\n')
	}
	if recorder.detail && selected != nil {
		builder.WriteString("\nSelected event\n")
		builder.WriteString(tuiEventDetail(*selected))
		builder.WriteByte('\n')
	}
	builder.WriteString("\nTop process groups in this session\n")
	for _, item := range recorder.topChurners(8) {
		builder.WriteString(fmt.Sprintf("%5d  %s\n", item.count, sanitizeTerminalText(item.command)))
	}
	if recorder.help {
		builder.WriteString("\nCommands require Enter: p pause/resume, k older, j newer, /text search, clear, d details, ? help\n")
	} else {
		builder.WriteString("\nEnter ? for controls\n")
	}
	if recorder.status != "" {
		builder.WriteString(sanitizeTerminalText(recorder.status))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func (recorder *TUIRecorder) filteredEvents() []Event {
	if recorder.search == "" {
		return recorder.events
	}
	filtered := []Event{}
	needle := strings.ToLower(recorder.search)
	for _, event := range recorder.events {
		if strings.Contains(eventSearchText(event), needle) {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func (recorder *TUIRecorder) visibleEvents() ([]Event, *Event) {
	events := recorder.filteredEvents()
	end := len(events) - recorder.scroll
	if end < 0 {
		end = 0
	}
	if end > len(events) {
		end = len(events)
	}
	start := end - tuiVisibleEvents
	if start < 0 {
		start = 0
	}
	visible := events[start:end]
	if len(visible) == 0 {
		return visible, nil
	}
	selected := visible[len(visible)-1]
	return visible, &selected
}

func tuiEventLine(event Event) string {
	user := sanitizeTerminalText(valueOrDash(event.User))
	command := event.Command
	if event.Kind == EventChurn {
		command = fmt.Sprintf("%s (%d events in %s)", event.Command, event.Count, time.Duration(event.WindowMillis)*time.Millisecond)
	}
	if event.Kind == EventGap {
		command = event.Message
	}
	command = sanitizeTerminalText(command)
	return fmt.Sprintf("%-15s %-6s %-8d %-11s %s", event.Time.Format("15:04:05.000"), event.Kind, event.PID, truncate(user, 11), truncate(command, 90))
}

func tuiEventDetail(event Event) string {
	return fmt.Sprintf("process=%s parent=%d exe=%q cwd=%q unit=%q container=%q duration=%s backend=%s", sanitizeTerminalText(valueOrDash(event.ProcessID)), event.ParentPID, sanitizeTerminalText(event.Exe), sanitizeTerminalText(event.Cwd), sanitizeTerminalText(event.SystemdUnit), sanitizeTerminalText(event.ContainerID), time.Duration(event.DurationMillis)*time.Millisecond, sanitizeTerminalText(valueOrDash(event.Backend)))
}

type churnItem struct {
	command string
	count   int
}

func (recorder *TUIRecorder) incrementCount(command string) {
	if command == "" {
		return
	}
	if _, found := recorder.counts[command]; !found && len(recorder.counts) >= tuiCountLimit {
		recorder.dropSmallestCount()
	}
	recorder.counts[command]++
}

func (recorder *TUIRecorder) dropSmallestCount() {
	smallestKey := ""
	smallestCount := 0
	for command, count := range recorder.counts {
		if smallestKey == "" || count < smallestCount {
			smallestKey = command
			smallestCount = count
		}
	}
	delete(recorder.counts, smallestKey)
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

func tuiProcessGroup(event Event) string {
	return processGroupIdentityForEvent(event).label()
}

func valueOrDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func truncate(text string, width int) string {
	runes := []rune(text)
	if len(runes) <= width {
		return text
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

func sanitizeTerminalText(text string) string {
	const hex = "0123456789abcdef"
	var builder strings.Builder
	for _, character := range text {
		if character < 0x20 || character >= 0x7f && character <= 0x9f {
			builder.WriteString(`\x`)
			builder.WriteByte(hex[byte(character)>>4])
			builder.WriteByte(hex[byte(character)&0x0f])
		} else {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func isTerminalWriter(writer io.Writer) bool {
	file, ok := writer.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
