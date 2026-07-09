package main

import "time"

const (
	BackendAuto               = "auto"
	BackendPoll               = "poll"
	BackendLinuxProcConnector = "linux-proc-connector"
)

const (
	EventStart EventKind = "start"
	EventStop  EventKind = "stop"
	EventChurn EventKind = "churn"
	EventGap   EventKind = "gap"
)

const (
	EventModeBoth = "both"
)

const (
	FormatText   OutputFormat = "text"
	FormatJSONL  OutputFormat = "jsonl"
	FormatCSV    OutputFormat = "csv"
	FormatSQLite OutputFormat = "sqlite"
)

type EventKind string

type OutputFormat string

type Config struct {
	Backend        string
	Format         OutputFormat
	OutputPath     string
	PollInterval   time.Duration
	ChurnWindow    time.Duration
	ChurnThreshold int
	TUI            bool
	RunSelfTest    bool
	Filter         Filter
}

type Process struct {
	ID         string
	PID        int
	ParentPID  int
	Command    string
	Exe        string
	Cwd        string
	User       string
	StartedAt  time.Time
	CapturedAt time.Time
}

type ProcessSummary struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
	Exe     string `json:"exe,omitempty"`
	User    string `json:"user,omitempty"`
}

type Event struct {
	Kind           EventKind        `json:"kind"`
	Time           time.Time        `json:"time"`
	PID            int              `json:"pid,omitempty"`
	ParentPID      int              `json:"parent_pid,omitempty"`
	Command        string           `json:"command,omitempty"`
	Exe            string           `json:"exe,omitempty"`
	Cwd            string           `json:"cwd,omitempty"`
	User           string           `json:"user,omitempty"`
	DurationMillis int64            `json:"duration_ms,omitempty"`
	ExitCode       *int             `json:"exit_code,omitempty"`
	Backend        string           `json:"backend"`
	Count          int              `json:"count,omitempty"`
	WindowMillis   int64            `json:"window_ms,omitempty"`
	Message        string           `json:"message,omitempty"`
	ParentChain    []ProcessSummary `json:"parent_chain,omitempty"`
}

type ProcessSnapshot map[string]Process

type Filter struct {
	Include     []string
	Exclude     []string
	User        string
	CwdContains string
	ExeContains string
	ParentPID   int
	EventMode   string
	MinDuration time.Duration
	MaxDuration time.Duration
}

func validEventMode(mode string) bool {
	switch mode {
	case string(EventStart), string(EventStop), string(EventChurn), EventModeBoth:
		return true
	default:
		return false
	}
}

func validOutputFormat(format OutputFormat) bool {
	switch format {
	case FormatText, FormatJSONL, FormatCSV, FormatSQLite:
		return true
	default:
		return false
	}
}
