package main

import (
	"bufio"
	"errors"
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

type TUI struct {
	writer      io.Writer
	events      []Event
	mutex       sync.Mutex
	paused      bool
	scroll      int
	search      string
	detail      bool
	help        bool
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

func NewTUI(writer io.Writer) *TUI {
	return newTUI(writer, nil)
}

func newTUI(writer io.Writer, stop func()) *TUI {
	recorder := &TUI{
		writer: writer, events: []Event{}, input: os.Stdin,
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

func (recorder *TUI) Write(event Event) error {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return fmt.Errorf("write tui event kind=%s pid=%d: display is closed", event.Kind, event.PID)
	}
	if event.Backend != "" {
		recorder.backend = event.Backend
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

func (recorder *TUI) Close() error {
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
			displayErr = fmt.Errorf("restore terminal display after tui watch: %w", err)
		}
	}
	var inputErr error
	if recorder.state != nil {
		if err := term.Restore(int(recorder.input.Fd()), recorder.state); err != nil {
			inputErr = fmt.Errorf("restore terminal input after tui watch: %w", err)
		}
		recorder.state = nil
	}
	return errors.Join(displayErr, inputErr)
}

func (recorder *TUI) readKeys(reader io.Reader) {
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

func (recorder *TUI) handleKey(key rune) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return
	}
	if key == 0x1b && recorder.searching {
		recorder.handleSearchKey(key)
		_ = recorder.writeFrameLocked()
		return
	}
	if key == 0x1b || key == 'q' || key == 0x03 || key == 0x04 {
		recorder.requestStop()
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
			recorder.status = ""
		case 'c':
			recorder.columns = (recorder.columns + 1) % 3
			recorder.status = ""
		case 's':
			recorder.sortMode = (recorder.sortMode + 1) % 4
			recorder.scroll = 0
			recorder.status = ""
		}
	}
	_ = recorder.writeFrameLocked()
}

func (recorder *TUI) handleSearchKey(key rune) {
	switch key {
	case '\r', '\n':
		recorder.search = recorder.searchDraft
		recorder.searching = false
		recorder.scroll = 0
		recorder.status = ""
	case 0x1b:
		recorder.searchDraft = recorder.search
		recorder.searching = false
		recorder.status = ""
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

func (recorder *TUI) recordInputError(err error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed || err == io.EOF {
		return
	}
	recorder.status = fmt.Sprintf("input error: %v", err)
	_ = recorder.writeFrameLocked()
}

func (recorder *TUI) watchSize() {
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

func (recorder *TUI) updateSize() bool {
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

func (recorder *TUI) readCommands(reader io.Reader) {
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

func (recorder *TUI) handleCommand(command string) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.closed {
		return
	}
	trimmed := strings.TrimSpace(command)
	if trimmed == "q" || trimmed == "quit" {
		recorder.requestStop()
		return
	}
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
		recorder.status = ""
	case trimmed == "sort":
		recorder.sortMode = (recorder.sortMode + 1) % 4
		recorder.scroll = 0
		recorder.status = ""
	case strings.HasPrefix(trimmed, "/"):
		recorder.search = strings.TrimPrefix(trimmed, "/")
		recorder.scroll = 0
	case trimmed != "":
		recorder.status = fmt.Sprintf("unknown command %q; enter ? for help", trimmed)
	}
	_ = recorder.writeFrameLocked()
}

func (recorder *TUI) requestStop() {
	recorder.status = "stopping"
	if recorder.stop != nil {
		recorder.stop()
		recorder.stop = nil
	}
}

func (recorder *TUI) togglePause() {
	recorder.paused = !recorder.paused
	recorder.status = ""
}

func (recorder *TUI) moveOlder() {
	if recorder.scroll < len(recorder.filteredEvents())-1 {
		recorder.scroll++
	}
}

func (recorder *TUI) moveNewer() {
	if recorder.scroll > 0 {
		recorder.scroll--
	}
}

func (recorder *TUI) render() string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.renderLocked()
}

func (recorder *TUI) writeFrameLocked() error {
	_, err := fmt.Fprint(recorder.writer, recorder.renderLocked())
	if err != nil {
		return fmt.Errorf("write tui frame: %w", err)
	}
	return nil
}

func (recorder *TUI) renderLocked() string {
	recorder.updateSize()
	var builder strings.Builder
	if recorder.interactive && !recorder.entered {
		builder.WriteString("\033[?1049h\033[?25l")
		recorder.entered = true
	}
	builder.WriteString("\033[H\033[2J")
	builder.WriteString(truncate(recorder.statusLine(), recorder.width))
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
	if recorder.help {
		builder.WriteString("\nKeys: p pause, k/j move, / search, x clear search, d details, c columns, s sort, ? help, Esc/q quit\n")
		builder.WriteString("Search: Return apply, Escape cancel, Backspace delete, Ctrl-U clear; q/Ctrl-C/Ctrl-D quit\n")
	} else {
		builder.WriteString("\np pause  k/j navigate  / search  d details  ? help  Esc/q quit\n")
	}
	if recorder.status != "" {
		builder.WriteString(sanitizeTerminalText(recorder.status))
		builder.WriteByte('\n')
	}
	return builder.String()
}

func (recorder *TUI) statusLine() string {
	parts := []string{}
	if recorder.paused {
		parts = append(parts, "PAUSED")
	}
	if recorder.backend == "" {
		parts = append(parts, "waiting for events")
	} else {
		parts = append(parts, sanitizeTerminalText(recorder.backend))
	}
	parts = append(parts, pluralCount(len(recorder.events), "event"))
	if recorder.searching {
		parts = append(parts, "search: "+sanitizeTerminalText(recorder.searchDraft)+"_")
	} else if recorder.search != "" {
		parts = append(parts, fmt.Sprintf("filter %q", sanitizeTerminalText(recorder.search)))
	}
	if recorder.scroll > 0 {
		parts = append(parts, pluralCount(recorder.scroll, "event")+" back")
	}
	if recorder.columns != tuiColumnsCommand {
		parts = append(parts, recorder.displayColumnName()+" view")
	}
	if recorder.sortMode != tuiSortTimeline {
		parts = append(parts, "sorted by "+recorder.sortName())
	}
	return strings.Join(parts, " • ")
}

func pluralCount(count int, singular string) string {
	if count == 1 {
		return fmt.Sprintf("%d %s", count, singular)
	}
	return fmt.Sprintf("%d %ss", count, singular)
}

func (recorder *TUI) filteredEvents() []Event {
	filtered := make([]Event, 0, len(recorder.events))
	needle := strings.ToLower(recorder.search)
	for _, event := range recorder.events {
		if needle == "" || strings.Contains(tuiSearchText(event), needle) {
			filtered = append(filtered, event)
		}
	}
	recorder.sortEvents(filtered)
	return filtered
}

func (recorder *TUI) visibleEvents(limit int) ([]Event, *Event) {
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

func (recorder *TUI) sortEvents(events []Event) {
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

func (recorder *TUI) visibleEventLimit() int {
	reserved := 13
	if recorder.detail {
		reserved += 3
	}
	if recorder.help {
		reserved += 1
	}
	return max(3, recorder.height-reserved)
}

func (recorder *TUI) columnName() string {
	switch recorder.columns {
	case tuiColumnsContext:
		return "context"
	case tuiColumnsExecutable:
		return "executable"
	default:
		return "command"
	}
}

func (recorder *TUI) displayColumnName() string {
	name := recorder.columnName()
	if recorder.effectiveColumns() != recorder.columns {
		return name + "(compact)"
	}
	return name
}

func (recorder *TUI) effectiveColumns() tuiColumnMode {
	if recorder.columns == tuiColumnsContext && recorder.width < 80 {
		return tuiColumnsCommand
	}
	if recorder.columns == tuiColumnsExecutable && recorder.width < 100 {
		return tuiColumnsCommand
	}
	return recorder.columns
}

func (recorder *TUI) sortName() string {
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

func (recorder *TUI) columnHeader() string {
	switch recorder.effectiveColumns() {
	case tuiColumnsContext:
		return "time            kind   pid      ppid     context              command"
	case tuiColumnsExecutable:
		return "time            kind   pid      user        executable                    command"
	default:
		return "time            kind   pid      user        command"
	}
}

func (recorder *TUI) eventLine(event Event) string {
	command := tuiEventCommand(event)
	width := max(20, recorder.width)
	switch recorder.effectiveColumns() {
	case tuiColumnsContext:
		context := valueOrDash(event.Cwd)
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
	recorder := &TUI{width: 120, columns: tuiColumnsCommand}
	return recorder.eventLine(event)
}

func tuiEventCommand(event Event) string {
	command := event.Command
	if event.Kind == EventGap {
		command = event.Message
	}
	command = sanitizeTerminalText(command)
	return command
}

func tuiEventDetail(event Event) string {
	return fmt.Sprintf("process=%s parent=%d exe=%q cwd=%q duration=%s backend=%s", sanitizeTerminalText(valueOrDash(event.ProcessID)), event.ParentPID, sanitizeTerminalText(event.Exe), sanitizeTerminalText(event.Cwd), time.Duration(event.DurationMillis)*time.Millisecond, sanitizeTerminalText(valueOrDash(event.Backend)))
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
