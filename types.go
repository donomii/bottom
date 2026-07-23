package main

import "time"

const (
	BackendAuto               = "auto"
	BackendPoll               = "poll"
	BackendLinuxProcConnector = "linux-proc-connector"
	BackendWindowsETW         = "windows-etw"
	BackendMacOSEndpoint      = "macos-endpoint-security"
	BackendTrace              = "trace"
)

const (
	EventStart EventKind = "start"
	EventExec  EventKind = "exec"
	EventStop  EventKind = "stop"
	EventGap   EventKind = "gap"
)

type EventKind string

type Config struct {
	Backend      string
	PollInterval time.Duration
	ShowPPID     bool
	TUI          bool
	ShowVersion  bool
}

type TraceConfig struct {
	PollInterval time.Duration
	ShowPPID     bool
	Command      []string
	Tail         time.Duration
}

type Process struct {
	ID         string
	PID        int
	ParentPID  int
	Command    string
	Exe        string
	Cwd        string
	User       string
	UID        string
	TTY        string
	Session    string
	StartedAt  time.Time
	CapturedAt time.Time
}

type ProcessSummary struct {
	PID       int
	ProcessID string
	Command   string
	Exe       string
	User      string
}

type Event struct {
	Kind           EventKind
	Time           time.Time
	ObservedAt     time.Time
	ProcessID      string
	PID            int
	ParentPID      int
	Command        string
	Exe            string
	Cwd            string
	User           string
	UID            string
	TTY            string
	Session        string
	DurationMillis int64
	ExitCode       *int
	Backend        string
	Count          int
	Message        string
	ParentChain    []ProcessSummary
}

type ProcessSnapshot map[string]Process
