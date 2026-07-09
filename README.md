# Bottom

[![Test](https://github.com/donomii/bottom/actions/workflows/test.yml/badge.svg)](https://github.com/donomii/bottom/actions/workflows/test.yml)

Bottom is the opposite of top: top shows what is alive right now, while bottom records what started, stopped, churned, or disappeared between snapshots.

![bottom demo](docs/demo.gif)

Bottom is for answering questions like:

- What short-lived command just ran?
- Which process is restarting over and over?
- What parent process launched this child?
- Which user, executable path, or working directory is involved?

This repository is `donomii/bottom`, a process lifecycle recorder. It is not the unrelated full system monitor at https://github.com/ClementTsang/bottom.

## Run

```sh
./run.sh
```

The launcher starts bottom with sensible defaults: automatic backend selection, one-second polling fallback, text output, and start/stop/churn/gap events printed to stdout.

## Build, Test, Demo, Install

```sh
./build.sh
./test.sh
./demo.sh
./install.sh
```

`build.sh` builds a local `bottom` binary. `test.sh` runs Go tests and the built-in `-test` checks. `demo.sh` runs the built-in checks and prints a copyable demo command. `install.sh` installs the command through `go install`.

## Options

Every option has a default and can be edited directly in `bottom.go`.

- `-backend auto`: chooses the best available event backend and falls back to polling. Values are `auto`, `poll`, `linux-proc-connector`, `linux-ebpf`, `windows-wmi`, and `macos-endpoint-security`.
- `-poll 1s`: sets the polling interval used by the polling backend and fallback mode.
- `-format text`: writes `text`, `jsonl`, `csv`, or `sqlite`.
- `-output PATH`: writes output to a file. Empty output writes text, JSONL, and CSV to stdout. SQLite defaults to `bottom.sqlite`.
- `-tui`: shows a live terminal timeline with recent events and top churners.
- `-include TEXT`: shows only events whose command, executable path, current directory, user, pid, parent pid, or parent chain contains the text. Repeat it to match more terms.
- `-exclude TEXT`: hides events matching the text. Repeat it to hide more terms.
- `-events both`: shows `start`, `stop`, `churn`, `gap`, or `both`.
- `-user USER`: shows events owned by one user name or numeric id.
- `-ppid PID`: shows events whose immediate parent process has this pid.
- `-cwd TEXT`: shows events whose current directory contains the text.
- `-exe TEXT`: shows events whose executable path contains the text.
- `-min-duration 0s`: for stop events, shows only processes that lived at least this long.
- `-max-duration 0s`: for stop events, shows only processes that lived no longer than this.
- `-churn-window 10s`: sets the time window used to group repeated command starts.
- `-churn-threshold 5`: reports a churn event after this many starts inside the churn window.
- `-test`: runs built-in checks for filtering, recorders, churn detection, and snapshot diffing.

## Readouts

Text output uses one line per event:

```text
12:00:01.123 start pid=421 ppid=22 user=jer cmd="compiler --input main.go" parent=22:shell
12:00:02.456 stop  pid=421 duration=83ms exit= cmd="compiler --input main.go"
12:00:03.000 churn count=5 window=10s cmd="compiler --input main.go"
12:00:04.000 gap   backend=poll message="process snapshot failed; expected a complete process table, received error ..."
```

- `start` means a process appeared.
- `stop` means a process disappeared; duration is measured from the best available start time or first observation time.
- `churn` means the same command started repeatedly inside the churn window.
- `gap` means bottom detected a backend or snapshot problem and resynced when possible.
- `parent` shows the parent chain from immediate parent upward.

## Backend Notes

- Linux `auto` prefers `linux-proc-connector`, which subscribes to kernel process connector events and refreshes the process snapshot on each event.
- Linux `poll` reads `/proc` directly and avoids shell helpers.
- macOS and other Unix polling uses `ps` because Endpoint Security requires a signed entitled build.
- Windows polling uses WMI command output for process snapshots.
- `linux-ebpf`, `windows-wmi`, and `macos-endpoint-security` are explicit backend names so packaging can grow into those event sources without changing the CLI.

## Output Formats

JSONL writes one JSON object per event. CSV writes a header plus one row per event. SQLite creates an `events` table with time, kind, pid, parent pid, user, command, executable path, current directory, duration, exit code, backend, churn count, churn window, message, and parent chain.
