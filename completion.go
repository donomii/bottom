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
	{name: "watch", description: "Watch process lifecycle events"},
	{name: "trace", description: "Watch one command and its descendants"},
	{name: "version", description: "Print build identity"},
	{name: "completion", description: "Write fish shell completions"},
}

var (
	watchCompletionCommands = []string{"watch"}
)

var completionOptions = []completionOption{
	completionValueOption("backend", "Select auto, poll, linux-proc-connector, windows-etw, or macos-endpoint-security capture", watchCompletionCommands),
	completionValueOption("poll", "Set the polling interval", watchCompletionCommands),
	completionToggleOption("ppid", "Include the parent PID in readable event lines", watchCompletionCommands),
	completionToggleOption("tui", "Use the interactive terminal timeline", watchCompletionCommands),
	completionToggleOption("version", "Print build identity", watchCompletionCommands),
	completionValueOption("poll", "Set the descendant snapshot interval", []string{"trace"}),
	completionToggleOption("ppid", "Include the parent PID in readable event lines", []string{"trace"}),
	completionValueOption("tail", "Observe descendants this long after root exit", []string{"trace"}),
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
	helpCommands := "watch trace completion"
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
