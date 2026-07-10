package main

import "time"

const (
	BackendAuto               = "auto"
	BackendPoll               = "poll"
	BackendLinuxProcConnector = "linux-proc-connector"
	BackendTrace              = "trace"
)

const (
	EventStart EventKind = "start"
	EventExec  EventKind = "exec"
	EventStop  EventKind = "stop"
	EventChurn EventKind = "churn"
	EventGap   EventKind = "gap"
)

const (
	EventModeBoth = "both"
	EventModeAll  = "all"
)

const EventSchemaVersion = 1

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
	ChurnCooldown  time.Duration
	ChurnMaxKeys   int
	ChurnMaxLife   time.Duration
	RecorderBuffer int
	SQLiteBatch    int
	SQLiteFlush    time.Duration
	Retention      time.Duration
	RotateSize     int64
	RotateInterval time.Duration
	Redact         []string
	RingBuffer     int
	Trigger        string
	PostTrigger    time.Duration
	TUI            bool
	RunSelfTest    bool
	ShowVersion    bool
	Filter         Filter
}

type RecordingReadConfig struct {
	InputPath  string
	OutputPath string
	Format     OutputFormat
	Filter     Filter
	Limit      int
	Speed      float64
	MaxDelay   time.Duration
	TUI        bool
}

type RecordingCompareConfig struct {
	BeforePath string
	AfterPath  string
	OutputPath string
}

type TraceConfig struct {
	Recorder     Config
	Command      []string
	Tail         time.Duration
	PerfettoPath string
}

type Process struct {
	ID          string
	PID         int
	ParentPID   int
	Command     string
	Exe         string
	Cwd         string
	User        string
	UID         string
	TTY         string
	Session     string
	Cgroup      string
	SystemdUnit string
	ContainerID string
	StartedAt   time.Time
	CapturedAt  time.Time
}

type ProcessSummary struct {
	PID       int    `json:"pid"`
	ProcessID string `json:"process_id,omitempty"`
	Command   string `json:"command"`
	Exe       string `json:"exe,omitempty"`
	User      string `json:"user,omitempty"`
}

type Event struct {
	SchemaVersion  int              `json:"schema_version"`
	SessionID      string           `json:"session_id,omitempty"`
	Sequence       uint64           `json:"sequence,omitempty"`
	Host           string           `json:"host,omitempty"`
	BootID         string           `json:"boot_id,omitempty"`
	Kind           EventKind        `json:"kind"`
	Time           time.Time        `json:"time"`
	ObservedAt     time.Time        `json:"observed_at,omitempty"`
	ProcessID      string           `json:"process_id,omitempty"`
	PID            int              `json:"pid,omitempty"`
	ParentPID      int              `json:"parent_pid,omitempty"`
	Command        string           `json:"command,omitempty"`
	Exe            string           `json:"exe,omitempty"`
	Cwd            string           `json:"cwd,omitempty"`
	User           string           `json:"user,omitempty"`
	UID            string           `json:"uid,omitempty"`
	TTY            string           `json:"tty,omitempty"`
	Session        string           `json:"session,omitempty"`
	Cgroup         string           `json:"cgroup,omitempty"`
	SystemdUnit    string           `json:"systemd_unit,omitempty"`
	ContainerID    string           `json:"container_id,omitempty"`
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
	Include           []string
	Exclude           []string
	IncludeRegex      []string
	ExcludeRegex      []string
	User              string
	CwdContains       string
	ExeContains       string
	ContainerContains string
	UnitContains      string
	ParentPID         int
	AncestorPID       int
	EventMode         string
	MinDuration       time.Duration
	MaxDuration       time.Duration
	Since             time.Time
	Until             time.Time
	HasExitCode       bool
	ExitCode          int
}

func validEventMode(mode string) bool {
	switch mode {
	case string(EventStart), string(EventExec), string(EventStop), string(EventChurn), string(EventGap), EventModeBoth, EventModeAll:
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
