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

	"golang.org/x/term"
)

const (
	tuiVisibleEvents = 18
	tuiEventLimit    = 2048
	tuiCountLimit    = 4096
)

type tuiColumnMode int

const (
	tuiColumnsCommand tuiColumnMode = iota
	tuiColumnsContext
	tuiColumnsExecutable
)

type tuiSortMode int

const (
	tuiSortTimeline tuiSortMode = iota
	tuiSortDuration
	tuiSortPID
	tuiSortCommand
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
	input       *os.File
	output      *os.File
	state       *term.State
	resizeDone  chan struct{}
	width       int
	height      int
	columns     tuiColumnMode
	sortMode    tuiSortMode
	searching   bool
	searchDraft string
	stop        func()
}

func NewTUIRecorder(writer io.Writer) *TUIRecorder {
	return newTUIRecorder(writer, nil)
}

func newTUIRecorder(writer io.Writer, stop func()) *TUIRecorder {
	recorder := &TUIRecorder{
		writer: writer, events: []Event{}, counts: map[string]int{}, input: os.Stdin,
		resizeDone: make(chan struct{}), width: 120, height: 32, stop: stop,
	}
	output, outputIsFile := writer.(*os.File)
	if outputIsFile && term.IsTerminal(int(output.Fd())) {
		recorder.output = output
		recorder.interactive = true
		recorder.updateSize()
		if term.IsTerminal(int(recorder.input.Fd())) {
			state, err := term.MakeRaw(int(recorder.input.Fd()))
			if err == nil {
				recorder.state = state
				go recorder.readKeys(recorder.input)
			} else {
				recorder.status = fmt.Sprintf("raw terminal input unavailable: %v", err)
				go recorder.readCommands(recorder.input)
			}
		} else {
			go recorder.readCommands(recorder.input)
		}
		go recorder.watchSize()
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
	close(recorder.resizeDone)
	var displayErr error
	if recorder.interactive && recorder.entered {
		if _, err := fmt.Fprint(recorder.writer, "\033[?25h\033[?1049l"); err != nil {
			displayErr = fmt.Errorf("restore terminal display after tui recording: %w", err)
		}
	}
	var inputErr error
	if recorder.state != nil {
		if err := term.Restore(int(recorder.input.Fd()), recorder.state); err != nil {
			inputErr = fmt.Errorf("restore terminal input after tui recording: %w", err)
		}
		recorder.state = nil
	}
	return joinRecorderErrors(displayErr, inputErr)
}

func (recorder *TUIRecorder) readKeys(reader io.Reader) {
	input := bufio.NewReader(reader)
	for {
		key, _, err := input.ReadRune()
		if err != nil {
			recorder.recordInputError(err)
			return
		}
		recorder.handleKey(key)
	}
}

func (recorder *TUIRecorder) handleKey(key rune) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return
	}
	if recorder.searching {
		recorder.handleSearchKey(key)
	} else {
		switch key {
		case 'p':
			recorder.togglePause()
		case 'k':
			recorder.moveOlder()
		case 'j':
			recorder.moveNewer()
		case 'd':
			recorder.detail = !recorder.detail
		case '?':
			recorder.help = !recorder.help
		case '/':
			recorder.searching = true
			recorder.searchDraft = recorder.search
		case 'x':
			recorder.search = ""
			recorder.searchDraft = ""
			recorder.scroll = 0
			recorder.status = "search cleared"
		case 'c':
			recorder.columns = (recorder.columns + 1) % 3
			recorder.status = "columns: " + recorder.columnName()
		case 's':
			recorder.sortMode = (recorder.sortMode + 1) % 4
			recorder.scroll = 0
			recorder.status = "sort: " + recorder.sortName()
		case 0x03, 0x04:
			recorder.status = "stopping"
			if recorder.stop != nil {
				recorder.stop()
				recorder.stop = nil
			}
		}
	}
	_ = recorder.writeFrameLocked()
}

func (recorder *TUIRecorder) handleSearchKey(key rune) {
	switch key {
	case '\r', '\n':
		recorder.search = recorder.searchDraft
		recorder.searching = false
		recorder.scroll = 0
		recorder.status = fmt.Sprintf("search applied: %q", recorder.search)
	case 0x1b:
		recorder.searchDraft = recorder.search
		recorder.searching = false
		recorder.status = "search unchanged"
	case 0x7f, '\b':
		characters := []rune(recorder.searchDraft)
		if len(characters) > 0 {
			recorder.searchDraft = string(characters[:len(characters)-1])
		}
	case 0x15:
		recorder.searchDraft = ""
	default:
		if key >= 0x20 && key != 0x7f {
			recorder.searchDraft += string(key)
		}
	}
}

func (recorder *TUIRecorder) recordInputError(err error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed || err == io.EOF {
		return
	}
	recorder.status = fmt.Sprintf("input error: %v", err)
	_ = recorder.writeFrameLocked()
}

func (recorder *TUIRecorder) watchSize() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			recorder.mutex.Lock()
			changed := recorder.updateSize()
			if changed && !recorder.closed {
				_ = recorder.writeFrameLocked()
			}
			recorder.mutex.Unlock()
		case <-recorder.resizeDone:
			return
		}
	}
}

func (recorder *TUIRecorder) updateSize() bool {
	if recorder.output == nil {
		return false
	}
	width, height, err := term.GetSize(int(recorder.output.Fd()))
	if err != nil || width <= 0 || height <= 0 {
		return false
	}
	changed := width != recorder.width || height != recorder.height
	recorder.width = width
	recorder.height = height
	return changed
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
		recorder.togglePause()
	case trimmed == "k" || trimmed == "up":
		recorder.moveOlder()
	case trimmed == "j" || trimmed == "down":
		recorder.moveNewer()
	case trimmed == "d":
		recorder.detail = !recorder.detail
	case trimmed == "?":
		recorder.help = !recorder.help
	case trimmed == "clear":
		recorder.search = ""
		recorder.searchDraft = ""
		recorder.scroll = 0
	case trimmed == "columns":
		recorder.columns = (recorder.columns + 1) % 3
		recorder.status = "columns: " + recorder.columnName()
	case trimmed == "sort":
		recorder.sortMode = (recorder.sortMode + 1) % 4
		recorder.scroll = 0
		recorder.status = "sort: " + recorder.sortName()
	case strings.HasPrefix(trimmed, "/"):
		recorder.search = strings.TrimPrefix(trimmed, "/")
		recorder.scroll = 0
	case trimmed != "":
		recorder.status = fmt.Sprintf("unknown command %q; enter ? for help", trimmed)
	}
	_ = recorder.writeFrameLocked()
}

func (recorder *TUIRecorder) togglePause() {
	recorder.paused = !recorder.paused
	if recorder.paused {
		recorder.status = "timeline paused"
	} else {
		recorder.status = "timeline resumed"
	}
}

func (recorder *TUIRecorder) moveOlder() {
	if recorder.scroll < len(recorder.filteredEvents())-1 {
		recorder.scroll++
	}
}

func (recorder *TUIRecorder) moveNewer() {
	if recorder.scroll > 0 {
		recorder.scroll--
	}
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
	recorder.updateSize()
	var builder strings.Builder
	if recorder.interactive && !recorder.entered {
		builder.WriteString("\033[?1049h\033[?25l")
		recorder.entered = true
	}
	builder.WriteString("\033[H\033[2J")
	builder.WriteString("bottom process lifecycle flight recorder\n")
	search := recorder.search
	if recorder.searching {
		search = recorder.searchDraft + "_"
	}
	status := fmt.Sprintf("backend=%s coverage_gaps=%d paused=%t search=%q scroll=%d columns=%s sort=%s", sanitizeTerminalText(valueOrDash(recorder.backend)), recorder.gapCount, recorder.paused, search, recorder.scroll, recorder.displayColumnName(), recorder.sortName())
	builder.WriteString(truncate(status, recorder.width))
	builder.WriteByte('\n')
	builder.WriteString(truncate(recorder.columnHeader(), recorder.width))
	builder.WriteByte('\n')
	builder.WriteString(strings.Repeat("-", min(recorder.width, 160)))
	builder.WriteByte('\n')
	visible, selected := recorder.visibleEvents(recorder.visibleEventLimit())
	for _, event := range visible {
		builder.WriteString(recorder.eventLine(event))
		builder.WriteByte('\n')
	}
	if recorder.detail && selected != nil {
		builder.WriteString("\nSelected event\n")
		builder.WriteString(tuiEventDetail(*selected))
		builder.WriteByte('\n')
	}
	builder.WriteString("\nTop process groups in this session\n")
	for _, item := range recorder.topChurners(recorder.topGroupLimit()) {
		builder.WriteString(fmt.Sprintf("%5d  %s\n", item.count, truncate(sanitizeTerminalText(item.command), max(10, recorder.width-7))))
	}
	if recorder.help {
		builder.WriteString("\nKeys: p pause, k/j move, / search, x clear search, d details, c columns, s sort, ? help\n")
		builder.WriteString("Search: Return apply, Escape cancel, Backspace delete, Ctrl-U clear; Ctrl-C/D stop Bottom\n")
	} else {
		builder.WriteString("\nPress ? for controls\n")
	}
	if recorder.status != "" {
		builder.WriteString(sanitizeTerminalText(recorder.status))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func (recorder *TUIRecorder) filteredEvents() []Event {
	filtered := make([]Event, 0, len(recorder.events))
	needle := strings.ToLower(recorder.search)
	for _, event := range recorder.events {
		if needle == "" || strings.Contains(eventSearchText(event), needle) {
			filtered = append(filtered, event)
		}
	}
	recorder.sortEvents(filtered)
	return filtered
}

func (recorder *TUIRecorder) visibleEvents(limit int) ([]Event, *Event) {
	events := recorder.filteredEvents()
	end := len(events) - recorder.scroll
	if end < 0 {
		end = 0
	}
	if end > len(events) {
		end = len(events)
	}
	start := end - limit
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

func (recorder *TUIRecorder) sortEvents(events []Event) {
	switch recorder.sortMode {
	case tuiSortDuration:
		sort.SliceStable(events, func(i int, j int) bool {
			return events[i].DurationMillis > events[j].DurationMillis
		})
	case tuiSortPID:
		sort.SliceStable(events, func(i int, j int) bool {
			return events[i].PID < events[j].PID
		})
	case tuiSortCommand:
		sort.SliceStable(events, func(i int, j int) bool {
			return strings.ToLower(events[i].Command) < strings.ToLower(events[j].Command)
		})
	}
}

func (recorder *TUIRecorder) visibleEventLimit() int {
	reserved := 13 + recorder.topGroupLimit()
	if recorder.detail {
		reserved += 3
	}
	if recorder.help {
		reserved += 1
	}
	return max(3, recorder.height-reserved)
}

func (recorder *TUIRecorder) topGroupLimit() int {
	if recorder.height < 24 {
		return 3
	}
	return 6
}

func (recorder *TUIRecorder) columnName() string {
	switch recorder.columns {
	case tuiColumnsContext:
		return "context"
	case tuiColumnsExecutable:
		return "executable"
	default:
		return "command"
	}
}

func (recorder *TUIRecorder) displayColumnName() string {
	name := recorder.columnName()
	if recorder.effectiveColumns() != recorder.columns {
		return name + "(compact)"
	}
	return name
}

func (recorder *TUIRecorder) effectiveColumns() tuiColumnMode {
	if recorder.columns == tuiColumnsContext && recorder.width < 80 {
		return tuiColumnsCommand
	}
	if recorder.columns == tuiColumnsExecutable && recorder.width < 100 {
		return tuiColumnsCommand
	}
	return recorder.columns
}

func (recorder *TUIRecorder) sortName() string {
	switch recorder.sortMode {
	case tuiSortDuration:
		return "duration"
	case tuiSortPID:
		return "pid"
	case tuiSortCommand:
		return "command"
	default:
		return "timeline"
	}
}

func (recorder *TUIRecorder) columnHeader() string {
	switch recorder.effectiveColumns() {
	case tuiColumnsContext:
		return "time            kind   pid      ppid     context              command"
	case tuiColumnsExecutable:
		return "time            kind   pid      user        executable                    command"
	default:
		return "time            kind   pid      user        command"
	}
}

func (recorder *TUIRecorder) eventLine(event Event) string {
	command := tuiEventCommand(event)
	width := max(20, recorder.width)
	switch recorder.effectiveColumns() {
	case tuiColumnsContext:
		context := valueOrDash(event.SystemdUnit)
		if event.ContainerID != "" {
			context = event.ContainerID
		}
		fixed := 15 + 1 + 6 + 1 + 8 + 1 + 8 + 1 + 20 + 1
		return fmt.Sprintf("%-15s %-6s %-8d %-8d %-20s %s", event.Time.Format("15:04:05.000"), event.Kind, event.PID, event.ParentPID, truncate(sanitizeTerminalText(context), 20), truncate(command, max(10, width-fixed)))
	case tuiColumnsExecutable:
		fixed := 15 + 1 + 6 + 1 + 8 + 1 + 11 + 1 + 28 + 1
		return fmt.Sprintf("%-15s %-6s %-8d %-11s %-28s %s", event.Time.Format("15:04:05.000"), event.Kind, event.PID, truncate(sanitizeTerminalText(valueOrDash(event.User)), 11), truncate(sanitizeTerminalText(valueOrDash(event.Exe)), 28), truncate(command, max(10, width-fixed)))
	default:
		fixed := 15 + 1 + 6 + 1 + 8 + 1 + 11 + 1
		return fmt.Sprintf("%-15s %-6s %-8d %-11s %s", event.Time.Format("15:04:05.000"), event.Kind, event.PID, truncate(sanitizeTerminalText(valueOrDash(event.User)), 11), truncate(command, max(10, width-fixed)))
	}
}

func tuiEventLine(event Event) string {
	recorder := &TUIRecorder{width: 120, columns: tuiColumnsCommand}
	return recorder.eventLine(event)
}

func tuiEventCommand(event Event) string {
	command := event.Command
	if event.Kind == EventChurn || event.Kind == EventRestart {
		command = fmt.Sprintf("%s (%d events in %s)", event.Command, event.Count, time.Duration(event.WindowMillis)*time.Millisecond)
	}
	if event.Kind == EventRestart && event.Message != "" {
		command = event.Message
	}
	if event.Kind == EventGap {
		command = event.Message
	}
	command = sanitizeTerminalText(command)
	return command
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
