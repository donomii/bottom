# Bottom Behavior Specification

## Summary

Bottom is a read-only process lifecycle recorder. It records process starts, executable replacements, stops, restart churn, correlated service restarts, and detected capture gaps. On Linux it also correlates exit status 137 with observed cgroup v2 `oom_kill` counter increases during the process lifetime.

The default interaction is an event stream suitable for terminals and pipelines. Optional recorders persist human-readable events to text or versioned sessions to JSONL, CSV, and SQLite. Recorded sessions can be queried, replayed, summarized, compared, and visualized.

The user runs `bottom completion` to write fish completions to standard output. The command explains the fish completion installation path through `bottom completion -h`. Release archives include the `bottom(1)` manual page, which lists commands, recording formats, default files, and where to find every option description.

The repository includes checksum-pinned Homebrew and Scoop recipes under `packaging/`. Their package identifier is `bottom-events`, while the installed command remains `bottom`. Each recipe names a published version, exact platform archive URLs, and the corresponding release checksums.

OpenTelemetry export is disabled by default. An explicit loopback OTLP/HTTP logs endpoint enables bounded background export; without that option Bottom creates no OpenTelemetry client and makes no export requests.

## User Interactions

### Continuous recording

The user runs `bottom`, `bottom record`, `bottom watch`, or `./run.sh`.

With no options, Bottom:

- selects the automatic process source;
- uses a 100 millisecond polling fallback;
- writes full-timestamp text events to standard output;
- includes start, exec, stop, churn, restart, and gap events;
- treats processes lasting at most 5 seconds as restart-loop candidates;
- keeps running until an operating-system interruption is received;
- flushes and closes every recorder before exiting.

The user runs `bottom -tui` for an interactive timeline. If `-output` is also set, the same redacted event value is sent to the TUI and persistent recorder.

The user runs `bottom -h`, `bottom trace -h`, `bottom query -h`, `bottom replay -h`, `bottom report -h`, or `bottom compare -h` to print command-specific help and exit successfully. Trace help does not require a command boundary. The user runs `bottom -test` to execute deterministic built-in checks and exit. The user runs `bottom -version` or `bottom version` to print the build version, source commit, and build date, then exit. Tagged builds inject all three fields. Other builds use the Go module version and VCS revision when available; their build date remains unknown unless the builder injects it.

### Scoped tracing

The user runs `bottom trace [trace options] -- command [command arguments]`.

Bottom reads an initial snapshot, starts the command directly without a command shell, records the root process immediately, discovers descendants through 10 millisecond native snapshots by default, and retains descendants that later change parent. It records root exit status when available.

Trace rejects TUI mode because the traced command shares the terminal streams. The recording path and optional Perfetto path are resolved and rejected before command execution when they identify the same file. Bottom retains the event list in memory only when Perfetto export is requested.

After the root exits, Bottom waits up to the configured 2 second tail for observed descendants. If the tail ends with descendants still present, Bottom records a gap naming their process identifiers and exits without acting on those processes.

After a successful command start, Bottom always waits for and reaps the root command's natural exit before returning, including when a recorder write fails. It does not alter the root command or surviving descendants.

An optional Perfetto export represents starts as begin events, stops as end events, and other lifecycle records as instant events. The export is created as a new owner-only file and does not replace an existing path.

### Reading recordings

The user runs:

- `bottom query` to filter and render recorded events as text, JSONL, or CSV;
- `bottom replay` to reproduce recorded order and relative timing, optionally through the TUI;
- `bottom report` to summarize sessions, kinds, gaps, failures, executable counts, parent counts, ancestry edges, and shortest lifetimes;
- `bottom compare` to compare process fingerprints, ancestry, counts, failures, and average lifetimes between two SQLite recordings.

Each repeated `-input` supplies a SQLite recording to query, replay, or report. With no input, the command reads `bottom.sqlite`. A command accepts at most 64 explicitly supplied recordings, rejects paths that resolve to the same file, and opens every input read-only without migration or modification. A recording with an older schema must first be opened by the current recorder, which owns migrations.

Within each file, SQLite readers stream versioned events, versioned gaps, legacy events, and legacy gaps through separate indexes and merge them by exact time, sequence, and a deterministic row tie-breaker. Multiple files are then merged by exact time, sequence, explicit input order, and source rank. Safe time, kind, parent, exit, and limit predicates run in SQLite; filters that require decoded context continue lazily until the requested number of matches is reached. A non-time row validation error is surfaced only when that row is next in merged order, including across files, so a positive limit can finish before a later error. An invalid normalized time or time key fails immediately because the row cannot be ordered safely.

Normalized columns are authoritative for filtering and returned events. Normalized times must be non-zero and agree with their indexed keys; versioned rows require event schema version 1 and every row requires a nonempty backend. An events-table kind must be start, exec, stop, or churn; gap belongs only in the gaps table. Nonempty raw versioned JSON must decode to an Event object with event schema version 1, a valid event kind, non-zero time, and nonempty backend. A nonempty normalized parent chain in a versioned row must be JSON after surrounding whitespace is removed. Legacy rows accept the pre-version-3 `PID[:command] <- PID[:command]` parent-chain format and reject malformed segments.

Reports and comparisons keep exact unique-name aggregates in automatically removed, file-backed temporary SQLite storage. In-memory state is bounded by scalar totals, the displayed top results, and the shortest-lifetime list rather than the number of unique process names.

## Configuration

### Capture and output

- `backend`: `auto` by default; alternatives are `poll`, `linux-proc-connector`, `windows-etw`, and `macos-endpoint-security`. Platform-specific choices return an error on other operating systems. Automatic selection emits a gap and uses polling when the native event backend cannot start or later fails.
- `poll_interval`: 100 milliseconds by default and must be positive.
- `format`: text by default; alternatives are JSONL, CSV, and SQLite.
- `output_path`: empty by default for text, JSONL, and CSV; SQLite defaults to `bottom.sqlite`.
- `tui`: disabled by default; enables interactive display without disabling file output.
- `recorder_buffer`: 1024 events by default and must be positive.
- `sqlite_batch`: 128 events by default and must be positive.
- `sqlite_flush`: 250 milliseconds by default and must be positive.
- `retention`: disabled at zero; when the recorder opens, a positive duration removes older SQLite event and gap rows, then removes ended sessions with no retained rows.
- `rotate_size`: disabled at zero; a positive byte count rotates text, JSONL, or CSV output.
- `rotate_interval`: disabled at zero; a positive duration rotates text, JSONL, or CSV output.
- `redact`: empty by default; each repeated exact value is replaced with `[REDACTED]` in free-text and context fields before fan-out.
- `otel_endpoint`: empty by default and makes no network requests. An explicit `http` or `https` URL must name `localhost` or a literal loopback address, include a port, and use `/v1/logs`; a missing path becomes `/v1/logs`. Export uses the recorder buffer, SQLite batch, and SQLite flush settings for bounded queue capacity, batch size, and partial-batch delay.

Output rotation is rejected for SQLite. Retention is rejected for non-SQLite output. Rotation and triggered recording require an output path.

### Event filtering

- `events`: all by default; accepts start, exec, stop, churn, restart, gap, all, and the backward-compatible both alias.
- `include`: repeated case-insensitive literal alternatives; at least one must match when supplied.
- `exclude`: repeated case-insensitive literal alternatives; any match rejects the event.
- `include_regex`: repeated case-sensitive regular expressions matched against original-case text; at least one must match when supplied.
- `exclude_regex`: repeated case-sensitive regular expressions matched against original-case text; any match rejects the event.
- `user`: matches either resolved user name or numeric UID.
- `parent_pid`: matches the immediate parent.
- `ancestor_pid`: matches the immediate parent or any captured ancestor.
- `cwd`, `exe`, `container`, and `unit`: literal field filters.
- `minimum_duration` and `maximum_duration`: non-negative stop-event bounds; a zero bound is disabled and the minimum cannot exceed a positive maximum.
- `since` and `until`: recording-reader bounds expressed as full timestamps or non-negative durations before the reader starts.
- `exit_code`: an optional exact stop-event exit code; zero is distinct from an unset filter.

Searchable fields include kind, process identity, command, executable, working directory, user, UID, TTY, process session, cgroup, service unit, container, host, PID, parent PID, diagnostic message, and captured ancestor summaries.

Capture-gap records bypass ordinary event filters so persisted coverage remains explicit.

### Restart churn

- `churn_window`: 10 seconds by default and must be positive.
- `churn_threshold`: 5 qualifying lifetimes by default and must be positive.
- `churn_cooldown`: 10 seconds by default and must not be negative.
- `churn_max_keys`: 4096 groups by default and must be positive.
- `churn_max_life`: 5 seconds by default and must not be negative; zero counts starts immediately.

### Triggered recording

- `ring_buffer`: disabled at zero; a positive event count bounds retained pre-trigger events.
- `trigger`: churn by default; alternatives are gap, failed-exit, and `regex:EXPRESSION`.
- `post_trigger`: 10 seconds by default and must not be negative.

### Replay

- `input_paths`: `bottom.sqlite` by default; `-input` may be repeated up to 64 times to merge distinct recordings.
- `speed`: 1 by default and must be positive.
- `maximum_delay`: 1 second by default and must not be negative; zero preserves full recorded delays.
- `limit`: zero by default and must not be negative; a positive count stops after that many matching events.

## Data Types

### Process

- `id`: stable process generation identity, normally PID plus operating-system start token.
- `pid`: process identifier.
- `parent_pid`: immediate parent identifier.
- `command`: command line or command name.
- `exe`: executable path or name when visible.
- `cwd`: current working directory when visible.
- `user`: resolved user name when cached, otherwise numeric UID.
- `uid`: numeric user identifier when available.
- `tty`: terminal identity when available.
- `session`: operating-system process session when available.
- `cgroup`: Linux cgroup path when available.
- `systemd_unit`: service, scope, or slice derived from Linux cgroup membership.
- `container_id`: recognized container identity derived from Linux cgroup membership.
- `started_at`: operating-system start time when available.
- `captured_at`: first time Bottom observed this process generation.

### Event

- `schema_version`: event contract version.
- `session_id`: logical recording session identity.
- `sequence`: monotonically increasing event sequence inside a session.
- `host`: host name.
- `boot_id`: operating-system boot identity when available.
- `kind`: start, exec, stop, churn, restart, or gap.
- `time`: best available source event time.
- `observed_at`: time Bottom received or inferred the event.
- `process_id`: stable process generation identity.
- process context: PID, parent PID, command, executable, working directory, user, UID, TTY, session, cgroup, service unit, container, and parent chain.
- `duration_ms`: process lifetime for stop events.
- `exit_code`: exit status when supplied by the backend.
- `backend`: process source that produced the event.
- `count` and `window_ms`: churn or gap count and applicable window.
- `message`: human-readable diagnostic or grouping explanation.

A restart event copies the new process context, names the service unit and previous process in its message, counts correlated restarts in the last 30 seconds, and uses a 30 second window. A memory-pressure correlation remains a stop event and adds the observed cgroup `oom_kill` count and observation time to its message.

### Recording session

- `schema_version`: storage schema version.
- `id`: physical recording session identity.
- `started_at` and `ended_at`: session bounds.
- `hostname`, `boot_id`, operating system, architecture, and the source active when the session recorder opened. Later backend transitions are represented by gap and event records.

## Process Capture Behavior

### Snapshot polling

```text
read the initial native process snapshot
repeat at the configured interval:
  read the next native process snapshot
  if the read fails:
    emit a structured gap and retain the previous snapshot
  otherwise:
    preserve first captured time and start time for unchanged stable identities
    index both snapshots by PID once
    emit execs for unchanged identities whose command or executable changed
    emit starts for identities present only in the next snapshot
    emit stops for identities present only in the previous snapshot
    replace the previous snapshot
```

Duration uses operating-system start time when available. Otherwise it uses the preserved first observation, never the most recent refresh.

### Linux connector

Bottom subscribes before reading the initial snapshot. It consumes direct process-level fork, exec, and exit notices, ignores thread-only lifecycle notices while retaining their sequence information, converts monotonic kernel timestamps to wall time, decodes exit status, and maintains a process cache.

Bottom emits a gap and resynchronizes when it detects:

- a receive queue overflow;
- a netlink overrun;
- a truncated or malformed message;
- a per-CPU sequence discontinuity;
- a failed periodic process-table resynchronization.

Periodic resynchronization occurs between 1 and 30 seconds depending on the polling setting. A provisional connector identity may align with a stable snapshot identity only when one identity is explicitly provisional and their start times agree within tolerance. Two different stable identities always represent different process generations.

### Windows ETW

Bottom starts a private real-time system logger for the kernel process provider, subscribes before its initial native snapshot, and consumes start and end records through an event-record callback. It reads process and parent identifiers plus exact 32-bit exit status through Trace Data Helper properties, converts system-time event timestamps, enriches starts from native process handles, and keeps a stable process cache for exit attribution.

The callback writes only to a bounded 4096-record queue. Queue overflow emits an exact dropped-record gap. A native snapshot reconciliation runs between 1 and 30 seconds based on the polling setting and repairs missed lifecycle state. Failure to create or consume the session returns an actionable Windows status; automatic selection records the failure and switches to polling.

### macOS Endpoint Security

Endpoint Security builds subscribe to notify-only fork, exec, and exit events before reading the initial native snapshot. Fork and exec records include stable start time, executable, command line, owner, session, terminal, and working directory when supplied by the framework. Exit wait status is decoded to the same process exit-code convention used by Linux.

The callback copies each message into a bounded 4096-record queue without retaining framework-owned memory. Global sequence numbers are checked when present; older messages use per-kind sequences. A discontinuity or queue overflow emits a gap and triggers native snapshot reconciliation. Periodic reconciliation runs between 1 and 30 seconds.

The macOS release archives contain this backend. Apple requires the `com.apple.developer.endpoint-security.client` entitlement, Full Disk Access, an entitled signing identity, and the privilege required by the installed macOS release. An explicit backend request returns the exact missing requirement. Automatic selection emits a gap and uses native polling when Endpoint Security cannot start. The source and signing procedure are documented in `docs/endpoint-security.md`; builds without CGO and the `endpointsecurity` build tag retain the same polling fallback.

### Platform snapshots

- Linux reads `/proc` directly.
- macOS uses native kernel process tables and native process-argument queries.
- Windows uses native Tool Help snapshots, process handles, command-line and executable-path queries, creation times, session identifiers, and process-token owner SIDs. Native command-line capture falls back to the executable name when access is unavailable. The SID is available immediately and background account-name resolution replaces it when the account can be resolved. Protected processes whose creation time cannot be read use a PID-only identity, so PID reuse can reduce lifecycle precision for those entries.
- Other Unix systems use a `ps` fallback with PID-only identity, so PID reuse can reduce lifecycle precision.

User-name resolution occurs in a dedicated background resolver. The snapshot path returns a numeric UID or Windows SID immediately until a cached name is available.

## Churn Behavior

```text
derive a group from executable, stable parent context, owner, service unit, and container
remember active starts by stable process identity
update active metadata on exec
on stop:
  determine the lifetime
  ignore lifetimes longer than the configured maximum
  add the start time to the group sliding window
remove starts older than the window
when count reaches or remains above the threshold:
  emit if the group cooldown has elapsed
expire inactive groups and active entries
when the group limit is full, evict the least recently touched group
```

Churn output retains the last process context, including parent, working directory, owner, service, container, and ancestry, so ordinary filters have the same meaning.

## Local Correlation Behavior

Bottom remembers a bounded set of at most 4096 systemd service units. A service start within 30 seconds after a stop for the same unit emits a restart event immediately. Restart counts retain only correlations inside the current 30 second window, and the least recently touched unit is removed when the bound is reached.

On Linux, a dedicated background reader registers observed cgroup v2 paths and reads their `memory.events` files. Lifecycle handling performs no file input. When an `oom_kill` counter increases, the background reader stores its count and observation time. A later exit-status-137 stop is annotated only when that increase was observed during the process lifetime and for the same cgroup. Missing cgroup v2 files, unavailable counters, and unrelated status-137 exits remain unannotated.

## Triggered Recording Behavior

Before activation, the recorder retains an ordered circular buffer containing at most the configured number of events. Trigger decisions observe every lifecycle event before output filtering. The trigger event causes retained values to be considered in original order, while ordinary output filters still prevent unrelated values from reaching a sink. Events are then considered through the post-trigger deadline. A later trigger extends the deadline. The next event after the deadline starts a newly armed ring. Closing an untriggered recorder discards its in-memory ring but still closes the session cleanly.

## Recorder Behavior

Every persistent destination receives the same redacted Event value. The logical session wrapper fills missing event schema, session, sequence, host, boot, and observation fields. The asynchronous recorder has a fixed capacity and returns an actionable error when full; it never silently removes events.

When a local OpenTelemetry endpoint is configured, a separate bounded recorder exports the redacted and filtered events as OTLP/HTTP JSON log records. Resource attributes identify Bottom, its version, host, operating system, and architecture. Log attributes retain event kind, backend, recording and process identities, process context, duration, exit code, counts, and diagnostic message. Integer values use OTLP's decimal-string JSON representation. Export uses a dedicated worker, a 5 second request timeout, no proxy, and surfaces encoding, transport, response-body, close, and non-2xx errors. Closing flushes the final partial batch.

SQLite writes prepared batches in transactions, flushes partial batches on the configured interval and close, applies ordered migrations, validates newer schemas, enforces foreign keys, and indexes common session, time, identity, executable, parent, service, and container queries. Every event and gap time index uses a fixed-width UTC nanosecond key followed by sequence, so textual timestamp width and source time zone cannot change chronological order or range results.

New files request owner-only access. Existing permissions are not changed. Structural discriminators are not redacted.

## TUI Behavior

The TUI retains at most 2048 recent events and 4096 process-group counters. It renders 18 visible events, semantic top process groups, backend, capture-gap count, pause state, search, scroll position, and optional event details. Text truncation preserves Unicode encoding. Process-derived C0, DEL, and C1 terminal controls render as visible hexadecimal escapes so command arguments cannot inject terminal control sequences.

Input followed by Return performs:

- `p`: pause or resume rendering while continuing collection;
- `k` and `j`: older and newer navigation;
- `/text`: search;
- `clear`: clear search;
- `d`: toggle selected-event details;
- `?`: toggle help.

## Recording Files

### Text

Each event line contains a full timestamp, kind, session, sequence, backend, stable process identity, relevant context, and kind-specific fields.

### JSONL and CSV

Storage schema version 4 contains session start/end records plus event and gap records. Event values retain event schema version 1.

### SQLite

The database contains:

- `schema_migrations` for ordered storage migrations;
- `sessions` for physical recording sessions;
- `events` for normalized non-gap fields and raw versioned JSON;
- `gaps` for normalized coverage diagnostics and raw versioned JSON.

Event and gap rows include both their source timestamp and a fixed-width UTC timestamp key with nine fractional digits. Migration to storage schema version 4 backfills that key for existing rows and rebuilds every event and gap time-bearing index on the key and sequence.

## Error Behavior

Invalid CLI values stop before capture and name the expected and received values. Invalid option combinations are rejected before opening output. An initial snapshot failure stops before capture; a later snapshot failure records a gap and continues with the previous snapshot. Explicit backend failure stops capture. Automatic backend failure records a transition gap and continues with polling. Recorder, migration, query, replay, comparison, trace, close, and export errors identify the operation, path, event kind, process, session, or schema values available at the failure point.

## Diagnostics and Benchmarks

Gap events are part of the recording contract rather than ordinary log suppression. Backend diagnostics are also written to standard error for a person watching the command.

The benchmark launcher measures burst snapshot diffing and high-cardinality churn state with allocation reporting. Benchmarks do not change event semantics.
