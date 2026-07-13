package main

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

type recordingCount struct {
	name  string
	count int
}

type recordingReportSummary struct {
	counts       map[string]map[string]int
	sessions     map[string]struct{}
	eventCount   int
	startCount   int
	execCount    int
	stopCount    int
	churnCount   int
	restartCount int
	gapCount     int
	failures     int
	shortest     []Event
	first        time.Time
	last         time.Time
}

func runRecordingReport(config RecordingReadConfig) error {
	if err := rejectRecordingOutputAliases("bottom report", config.OutputPath, config.InputPaths); err != nil {
		return err
	}
	stream, err := openRecordingFileEventStream("bottom report", config.InputPaths, config.Filter, config.Limit)
	if err != nil {
		return err
	}
	summary, err := newRecordingReportSummary()
	if err != nil {
		return joinRecorderErrors(err, stream.Close())
	}
	if err := stream.Stream(summary.observe); err != nil {
		return err
	}
	writer, closer, err := openOutput(config.OutputPath)
	if err != nil {
		return err
	}
	reportErr := summary.write(writer)
	var closeErr error
	if closer != nil {
		closeErr = closer.Close()
	}
	return joinRecorderErrors(reportErr, closeErr)
}

func newRecordingReportSummary() (*recordingReportSummary, error) {
	return &recordingReportSummary{
		counts: map[string]map[string]int{
			"executable": {},
			"parent":     {},
			"edge":       {},
		},
		sessions: map[string]struct{}{},
		shortest: []Event{},
	}, nil
}

func (summary *recordingReportSummary) observe(event Event) error {
	summary.sessions[event.SessionID] = struct{}{}
	if event.Exe != "" && (event.Kind == EventStart || event.Kind == EventExec) {
		summary.increment("executable", event.Exe)
	}
	if event.Kind == EventStart {
		summary.increment("parent", reportParent(event))
		child := reportExecutable(event)
		for _, ancestor := range reportAncestry(event) {
			summary.increment("edge", ancestor+" -> "+child)
			child = ancestor
		}
	}
	summary.eventCount++
	summary.incrementKind(event.Kind)
	if summary.first.IsZero() || event.Time.Before(summary.first) {
		summary.first = event.Time
	}
	if summary.last.IsZero() || event.Time.After(summary.last) {
		summary.last = event.Time
	}
	if event.Kind == EventStop {
		summary.shortest = append(summary.shortest, event)
		sort.SliceStable(summary.shortest, func(left int, right int) bool {
			return summary.shortest[left].DurationMillis < summary.shortest[right].DurationMillis
		})
		if len(summary.shortest) > 10 {
			summary.shortest = summary.shortest[:10]
		}
		if event.ExitCode != nil && *event.ExitCode != 0 {
			summary.failures++
		}
	}
	return nil
}

func (summary *recordingReportSummary) increment(category string, name string) {
	summary.counts[category][name]++
}

func (summary *recordingReportSummary) incrementKind(kind EventKind) {
	switch kind {
	case EventStart:
		summary.startCount++
	case EventExec:
		summary.execCount++
	case EventStop:
		summary.stopCount++
	case EventChurn:
		summary.churnCount++
	case EventRestart:
		summary.restartCount++
	case EventGap:
		summary.gapCount++
	}
}

func writeRecordingReport(writer io.Writer, events []Event) error {
	summary, err := newRecordingReportSummary()
	if err != nil {
		return err
	}
	for _, event := range events {
		if err := summary.observe(event); err != nil {
			return err
		}
	}
	return summary.write(writer)
}

func (summary *recordingReportSummary) write(writer io.Writer) error {
	var builder strings.Builder
	builder.WriteString("bottom recording report\n")
	builder.WriteString(fmt.Sprintf("events=%d sessions=%d gaps=%d failed_exits=%d\n", summary.eventCount, len(summary.sessions), summary.gapCount, summary.failures))
	if summary.eventCount > 0 {
		builder.WriteString(fmt.Sprintf("from=%s until=%s\n", summary.first.Format(time.RFC3339Nano), summary.last.Format(time.RFC3339Nano)))
	}
	builder.WriteString("\nEvent kinds\n")
	for _, item := range []struct {
		kind  EventKind
		count int
	}{
		{kind: EventStart, count: summary.startCount},
		{kind: EventExec, count: summary.execCount},
		{kind: EventStop, count: summary.stopCount},
		{kind: EventChurn, count: summary.churnCount},
		{kind: EventRestart, count: summary.restartCount},
		{kind: EventGap, count: summary.gapCount},
	} {
		builder.WriteString(fmt.Sprintf("%8d  %s\n", item.count, item.kind))
	}
	summary.writeCounts(&builder, "Top executables", "executable", 10)
	summary.writeCounts(&builder, "Top parents", "parent", 10)
	summary.writeCounts(&builder, "Process ancestry edges", "edge", 20)
	builder.WriteString("\nShortest lifetimes\n")
	for _, event := range summary.shortest {
		builder.WriteString(fmt.Sprintf("%8s  pid=%d exe=%q cmd=%q\n", recordingDuration(event), event.PID, event.Exe, event.Command))
	}
	if _, err := io.WriteString(writer, builder.String()); err != nil {
		return fmt.Errorf("write process recording report: %w", err)
	}
	return nil
}

func (summary *recordingReportSummary) writeCounts(builder *strings.Builder, title string, category string, limit int) {
	counts := make([]recordingCount, 0, len(summary.counts[category]))
	for name, count := range summary.counts[category] {
		counts = append(counts, recordingCount{name: name, count: count})
	}
	sort.Slice(counts, func(left int, right int) bool {
		if counts[left].count != counts[right].count {
			return counts[left].count > counts[right].count
		}
		return counts[left].name < counts[right].name
	})
	if len(counts) > limit {
		counts = counts[:limit]
	}
	builder.WriteString("\n" + title + "\n")
	for _, item := range counts {
		builder.WriteString(fmt.Sprintf("%8d  %s\n", item.count, sanitizeTerminalText(item.name)))
	}
}

func reportParent(event Event) string {
	if len(event.ParentChain) > 0 {
		return reportProcessSummary(event.ParentChain[0])
	}
	return strconv.Itoa(event.ParentPID)
}

func reportAncestry(event Event) []string {
	if len(event.ParentChain) == 0 {
		return []string{strconv.Itoa(event.ParentPID)}
	}
	ancestry := make([]string, 0, len(event.ParentChain))
	for _, parent := range event.ParentChain {
		ancestry = append(ancestry, reportProcessSummary(parent))
	}
	return ancestry
}

func reportProcessSummary(process ProcessSummary) string {
	if process.Exe != "" {
		return process.Exe
	}
	if process.Command != "" {
		return process.Command
	}
	return strconv.Itoa(process.PID)
}

func reportExecutable(event Event) string {
	if event.Exe != "" {
		return event.Exe
	}
	fields := strings.Fields(event.Command)
	if len(fields) > 0 {
		return fields[0]
	}
	return strconv.Itoa(event.PID)
}
