# Bottom Behavior Specification

## Summary

Bottom is a read-only process watcher. It logs process starts, executable replacements, stops, and detected capture gaps. It never acts on observed processes.

## Default behavior

The user runs `bottom`, `bottom watch`, or `./run.sh`.

Bottom selects the best available process source, falls back to polling when necessary, and writes one readable line per event to standard output:

```text
Start: PID      command
Exec:  PID      command
Stop:  PID      command
Gap:            diagnostic
```

The event and PID fields have fixed widths so the command or diagnostic begins at the same column and remains unbounded. Command and diagnostic text escapes terminal control bytes. The process log is the only stored representation supplied by Bottom. A user who wants a file redirects standard output through the shell.

Bottom runs until its context is cancelled or its event source closes. It closes the optional TUI before returning.

## Configuration

- `backend`: `auto` by default; alternatives are `poll`, `linux-proc-connector`, `windows-etw`, and `macos-endpoint-security`.
- `parent-exe`: includes the parent executable name in a fixed-width column before the command. It is enabled by default in the TUI and disabled by default in readable line output. `-parent-exe=false` disables it explicitly.
- `poll`: 100 milliseconds by default and must be positive.
- `ppid`: disabled by default; when enabled, readable process lines and TUI views include a fixed-width parent PID column after PID. The option also applies to trace output.
- `tui`: disabled by default; replaces readable log lines with the interactive display.
- `version`: prints version, commit, and build date and exits.

## Trace behavior

The user runs `bottom trace [options] -- command [arguments]`.

Bottom starts the command directly without a shell, logs the root start immediately, discovers descendants at the configured polling interval, and keeps tracking descendants that change parent. It logs observed starts, executable replacements, and stops with the same readable format as the ordinary watcher.

The default descendant polling interval is 10 milliseconds. After the root exits, Bottom watches observed descendants for up to the configured 2 second tail. If descendants remain at the deadline, it logs a gap and returns without acting on them. Once the command starts, Bottom waits for its natural exit before returning.

## Event data

An event carries:

- kind and source time;
- stable process identity, PID, parent PID, command, executable, current directory, owner, UID, terminal, and process session when available;
- operating-system observation time and backend name;
- process lifetime and exit status when available;
- captured parent summaries;
- gap count and diagnostic text when applicable.

This data is internal. The default log intentionally prints only the event name, PID, and command, except that gap lines print their diagnostic. With `-ppid`, Start, Exec, and Stop lines also print the parent PID. With `-parent-exe`, they include the executable name from the captured direct-parent summary. Missing parent metadata is shown as `-`.

## Process capture

Snapshot sources preserve the first observation and operating-system start time for an unchanged process generation. Each successful comparison emits starts, executable replacements, and stops. A failed initial snapshot aborts startup. A later snapshot failure emits a gap and retains the previous snapshot.

Linux uses the process connector when available and `/proc` for snapshots and enrichment. Windows uses ETW when available and Tool Help snapshots. macOS uses Endpoint Security when the binary is entitled and native snapshots otherwise. Other Unix systems use `ps` and exclude the exact `ps` child used to collect each snapshot while preserving unrelated `ps` processes.

Native sources subscribe before their initial snapshot, report sequence or queue losses as gaps, and reconcile against snapshots. Automatic selection logs a gap and switches to polling when a native source cannot start or later fails.

## TUI behavior

The TUI is used only when `-tui` is explicitly supplied. Its first line shows the process source and event count, followed only by active pause, search, navigation, view, and sorting states. Parent executable name is shown by default and can be hidden with `-parent-exe=false`. Parent PID is shown in every view only when `-ppid` is supplied. Gap rows explain interruptions directly instead of adding a separate counter. It retains at most 2048 recent events and supports pause, older/newer navigation, editable search, details, column selection, and sorting. Escape cancels active search input and quits outside search. `q`, Ctrl-C, and Ctrl-D immediately stop Bottom from any TUI state without acting on observed processes.

## Errors

Invalid options stop before capture and identify the expected and received values. Backend errors identify the backend and operation. Failures to write the process log are returned instead of being ignored.
