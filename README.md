# Bottom

[![Test](https://github.com/donomii/bottom/actions/workflows/test.yml/badge.svg)](https://github.com/donomii/bottom/actions/workflows/test.yml)

Bottom is the opposite of top: top shows what is alive right now, while bottom records what started, or stopped.

![bottom demo](docs/demo.gif)

## Run

```sh
./run.sh
```

## Build, Test, Demo, Install

```sh
./build.sh
./test.sh
./demo.sh
./install.sh
```

 `install.sh` installs the command through `go install`.

## Options


- `-backend auto`: chooses the best available backend. Values are `auto`, `poll`, and `linux-proc-connector`.
- `-poll 100ms`: sets the polling interval used by the polling backend and fallback mode. 
- `-format text`: writes `text`, `jsonl`, `csv`, or `sqlite`.
- `-output PATH`: writes output to a file. Empty output writes text, JSONL, and CSV to stdout. SQLite defaults to `bottom.sqlite`.
- `-tui`: shows a live terminal timeline with recent events and top churners.
- `-include TEXT`: shows only events that contains the text. 
- `-exclude TEXT`: hides events matching the text. 
- `-events both`: shows `start`, `stop`, `churn`, or `both`.
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
```

- `start` means a process appeared.
- `stop` means a process disappeared; duration is measured from the best available start time or first observation time.
- `churn` means the same command started repeatedly inside the churn window.
- `parent` shows the parent chain from immediate parent upward.

## Backend Notes

- Linux `auto` prefers `linux-proc-connector`, which subscribes to kernel process connector events and refreshes the process snapshot on each event.
- Linux `poll` reads `/proc` directly and avoids shell helpers.
- macOS and other Unix polling uses `ps`.
- Windows polling uses WMI command output for process snapshots.
