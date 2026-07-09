# Bottom Behavior Specification

## Summary

Bottom records process lifecycle changes. It reports process start events, process stop events, and repeated-start churn events.

## User Interactions

The user runs `bottom` or `./run.sh`.

With no options, bottom:

- selects `-backend auto`;
- uses a 100ms polling fallback;
- prints text events to stdout;
- reports start, stop, and churn events;
- keeps running until the user exits the process from the terminal.

The user runs `bottom -test` to execute built-in checks and exit.

The user runs `bottom -tui` to see a live terminal screen containing:

- a rolling event timeline with time, kind, pid, user, and command;
- top churners counted during the session.

## Configuration Options

`-backend` selects the process source. Values are:

- `auto`: use the best available event backend, falling back to polling;
- `poll`: use snapshot polling only;
- `linux-proc-connector`: subscribe to Linux process connector events.

`-poll` sets the polling interval. Values use Go duration syntax, including milliseconds such as `25ms`, `100ms`, and `500ms`.

`-format` selects output: `text`, `jsonl`, `csv`, or `sqlite`.

`-output` selects an output file. Empty output writes text, JSONL, and CSV to stdout. SQLite defaults to `bottom.sqlite`.

`-include` keeps events whose searchable fields contain the text.

`-exclude` removes events whose searchable fields contain the text.

`-events` keeps `start`, `stop`, `churn`, or `both`.

`-user`, `-ppid`, `-cwd`, and `-exe` filter by owner, immediate parent pid, current directory text, and executable path text.

`-min-duration` and `-max-duration` apply to stop events.

`-churn-window` and `-churn-threshold` configure repeated-start detection.

## Data Types

Process:

- `id`: pid plus OS start token when available;
- `pid`: process id;
- `parent_pid`: immediate parent process id;
- `command`: command line or command name;
- `exe`: executable path when available;
- `cwd`: current directory when available;
- `user`: user name or numeric id when available;
- `started_at`: OS start time when available;
- `captured_at`: time bottom observed the process.

Event:

- `kind`: `start`, `stop`, or `churn`;
- `time`: event timestamp;
- `pid`: process id when the event is process-specific;
- `parent_pid`: immediate parent process id;
- `command`: command line or command name;
- `exe`: executable path;
- `cwd`: current directory;
- `user`: user name or numeric id;
- `duration_ms`: lifetime for stop events;
- `exit_code`: exit code when the backend provides it;
- `backend`: backend that produced the event;
- `count`: repeated-start count for churn events;
- `window_ms`: churn window size for churn events;
- `message`: diagnostic text for churn events;
- `parent_chain`: parent process summaries from immediate parent upward.

## Process Snapshot Algorithm

To compare snapshots:

```text
read initial process snapshot
repeat:
  wait for backend signal or polling interval
  read next process snapshot
  for each process in next but not previous:
    emit start event with parent chain
  for each process in previous but not next:
    emit stop event with duration and exit code when known
  replace previous with next
```

## Churn Algorithm

For each start event:

```text
remove remembered starts older than churn window
remember this start time by command
if remembered start count equals churn threshold:
  emit churn event with command, count, and window
```

## Output Files

Text, JSONL, and CSV can write to stdout or append to `-output`.

SQLite creates or appends to the database at `-output`. The default path is `bottom.sqlite`.

SQLite table `events` contains:

- `id`;
- `time`;
- `kind`;
- `pid`;
- `parent_pid`;
- `user`;
- `command`;
- `exe`;
- `cwd`;
- `duration_ms`;
- `exit_code`;
- `backend`;
- `count`;
- `window_ms`;
- `message`;
- `parent_chain`.

## Error Behavior

Invalid CLI values stop startup with an error naming the expected value and received value.

Backend failures in `auto` mode are logged and replaced with polling.

Snapshot failures are logged to stderr and bottom continues on the next interval or backend signal.
