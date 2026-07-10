package main

import (
	"fmt"
	"io"
	"strings"
)

type completionCommand struct {
	name        string
	description string
}

type completionOption struct {
	name          string
	description   string
	requiresValue bool
	commands      []string
}

var completionCommands = []completionCommand{
	{name: "record", description: "Record process lifecycle events"},
	{name: "watch", description: "Record process lifecycle events"},
	{name: "trace", description: "Record one command and its descendants"},
	{name: "query", description: "Filter a SQLite recording"},
	{name: "replay", description: "Replay a SQLite recording"},
	{name: "report", description: "Summarize a SQLite recording"},
	{name: "compare", description: "Compare two SQLite recordings"},
	{name: "version", description: "Print build identity"},
	{name: "completion", description: "Write fish shell completions"},
}

var (
	recordCompletionCommands   = []string{"record", "watch"}
	filterCompletionCommands   = []string{"record", "watch", "query", "replay", "report"}
	readerCompletionCommands   = []string{"query", "replay", "report"}
	recorderCompletionCommands = []string{"record", "watch", "trace"}
	formatCompletionCommands   = []string{"record", "watch", "trace", "query"}
	outputCompletionCommands   = []string{"record", "watch", "trace", "query", "report", "compare"}
)

var completionOptions = []completionOption{
	completionValueOption("backend", "Select auto, poll, or linux-proc-connector capture", recordCompletionCommands),
	completionValueOption("include", "Keep events containing this text; repeatable", filterCompletionCommands),
	completionValueOption("exclude", "Remove events containing this text; repeatable", filterCompletionCommands),
	completionValueOption("include-regex", "Keep events matching this expression; repeatable", filterCompletionCommands),
	completionValueOption("exclude-regex", "Remove events matching this expression; repeatable", filterCompletionCommands),
	completionValueOption("user", "Keep events for this user name or numeric id", filterCompletionCommands),
	completionValueOption("cwd", "Keep events whose working directory contains this text", filterCompletionCommands),
	completionValueOption("exe", "Keep events whose executable contains this text", filterCompletionCommands),
	completionValueOption("container", "Keep events whose container id contains this text", filterCompletionCommands),
	completionValueOption("unit", "Keep events whose service unit contains this text", filterCompletionCommands),
	completionValueOption("ppid", "Keep events with this immediate parent process id", filterCompletionCommands),
	completionValueOption("ancestor-pid", "Keep events descended from this process id", filterCompletionCommands),
	completionValueOption("events", "Select start, exec, stop, churn, gap, all, or both", filterCompletionCommands),
	completionValueOption("min-duration", "Keep stop events with at least this lifetime", filterCompletionCommands),
	completionValueOption("max-duration", "Keep stop events with no more than this lifetime", filterCompletionCommands),
	completionValueOption("since", "Keep recorded events after this time", readerCompletionCommands),
	completionValueOption("until", "Keep recorded events before this time", readerCompletionCommands),
	completionValueOption("exit-code", "Keep stop events with this exit code", readerCompletionCommands),
	completionValueOption("poll", "Set the polling or descendant snapshot interval", recorderCompletionCommands),
	completionValueOption("format", "Select the output format", formatCompletionCommands),
	completionValueOption("output", "Write output to this path", outputCompletionCommands),
	completionToggleOption("tui", "Use the interactive terminal timeline", []string{"record", "watch", "replay"}),
	completionValueOption("churn-window", "Set the restart-loop grouping window", recordCompletionCommands),
	completionValueOption("churn-threshold", "Set starts required for a churn event", recordCompletionCommands),
	completionValueOption("churn-cooldown", "Set the delay between repeated churn reports", recordCompletionCommands),
	completionValueOption("churn-max-keys", "Set the maximum retained process groups", recordCompletionCommands),
	completionValueOption("churn-max-life", "Set the longest restart-loop process lifetime", recordCompletionCommands),
	completionValueOption("recorder-buffer", "Set buffered events before backpressure", recorderCompletionCommands),
	completionValueOption("sqlite-batch", "Set events per SQLite transaction", recorderCompletionCommands),
	completionValueOption("sqlite-flush", "Set the partial SQLite transaction delay", recorderCompletionCommands),
	completionValueOption("retention", "Remove older SQLite events when opening", recordCompletionCommands),
	completionValueOption("rotate-size", "Rotate text output after this many bytes", recordCompletionCommands),
	completionValueOption("rotate-interval", "Rotate text output after this duration", recordCompletionCommands),
	completionValueOption("redact", "Replace this exact recorded text; repeatable", recorderCompletionCommands),
	completionValueOption("ring-buffer", "Retain this many events before a trigger", recordCompletionCommands),
	completionValueOption("trigger", "Select churn, gap, failed-exit, or regex trigger", recordCompletionCommands),
	completionValueOption("post-trigger", "Keep recording for this duration after a trigger", recordCompletionCommands),
	completionToggleOption("test", "Run built-in checks", recordCompletionCommands),
	completionToggleOption("version", "Print build identity", recordCompletionCommands),
	completionValueOption("tail", "Observe descendants this long after root exit", []string{"trace"}),
	completionValueOption("perfetto", "Write a Perfetto-compatible timeline", []string{"trace"}),
	completionValueOption("input", "Read this SQLite recording; repeatable up to 64 times", readerCompletionCommands),
	completionValueOption("limit", "Stop after this many matching events", readerCompletionCommands),
	completionValueOption("speed", "Set the replay speed multiplier", []string{"replay"}),
	completionValueOption("max-delay", "Cap the delay between replayed events", []string{"replay"}),
	completionValueOption("before", "Read this baseline SQLite recording", []string{"compare"}),
	completionValueOption("after", "Read this comparison SQLite recording", []string{"compare"}),
}

func completionValueOption(name string, description string, commands []string) completionOption {
	return completionOption{name: name, description: description, requiresValue: true, commands: commands}
}

func completionToggleOption(name string, description string, commands []string) completionOption {
	return completionOption{name: name, description: description, commands: commands}
}

func runCompletion(args []string, output io.Writer) error {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		help := "Usage: bottom completion\n\nWrites fish completions to standard output. " +
			"Save the output as ~/.config/fish/completions/bottom.fish."
		_, err := fmt.Fprintln(output, help)
		if err != nil {
			return fmt.Errorf("write completion help: %w", err)
		}
		return nil
	}
	if len(args) != 0 {
		return fmt.Errorf("bottom completion expected no arguments, received %q", strings.Join(args, " "))
	}
	return writeFishCompletion(output)
}

func writeFishCompletion(output io.Writer) error {
	var script strings.Builder
	for _, command := range completionCommands {
		fmt.Fprintf(
			&script,
			"complete -c bottom -f -n '__fish_use_subcommand' -a %s -d %s\n",
			fishQuote(command.name),
			fishQuote(command.description),
		)
	}
	for _, option := range completionOptions {
		requiresValue := ""
		if option.requiresValue {
			requiresValue = " -r"
		}
		condition := fishQuote("__fish_seen_subcommand_from " + strings.Join(option.commands, " "))
		fmt.Fprintf(&script, "complete -c bottom -n %s -l %s%s -d %s\n",
			condition, option.name, requiresValue, fishQuote(option.description))
	}
	helpCommands := "record watch trace query replay report compare completion"
	helpCondition := fishQuote("__fish_seen_subcommand_from " + helpCommands)
	fmt.Fprintf(&script, "complete -c bottom -n %s -s h -l help -d %s\n",
		helpCondition, fishQuote("Show command help"))
	_, err := io.WriteString(output, script.String())
	if err != nil {
		return fmt.Errorf("write fish completion: %w", err)
	}
	return nil
}

func fishQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "\\'") + "'"
}
