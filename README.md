# bottom — process changes in your terminal

[![Test](https://github.com/donomii/bottom/actions/workflows/test.yml/badge.svg)](https://github.com/donomii/bottom/actions/workflows/test.yml)
[![Release](https://github.com/donomii/bottom/actions/workflows/release.yml/badge.svg)](https://github.com/donomii/bottom/actions/workflows/release.yml)

`top` shows what is running. `bottom` logs what starts and stops.

```text
Start: 421: compiler --input main.go
Exec: 421: compiler --input main.go
Stop: 421: compiler --input main.go
```

Bottom only observes processes. It does not terminate, suspend, inject into, or otherwise alter them.

## Install

Install with Homebrew:

```fish
brew install donomii/tap/bottom-events
```

Install with Scoop:

```text
scoop bucket add donomii https://github.com/donomii/scoop-bucket
scoop install bottom-events
```

Install from source with Go 1.25 or newer:

```fish
go install github.com/donomii/bottom@latest
```

From a checkout:

```fish
./run.sh
```

## Use

Run `bottom` with no arguments for the readable process log:

```fish
bottom
```

The log goes to standard output, so ordinary shell redirection handles files:

```fish
bottom >process.log
```

`-backend NAME` selects `auto`, `poll`, `linux-proc-connector`, `windows-etw`, or `macos-endpoint-security`. `-poll DURATION` changes the polling and fallback interval. `-tui` explicitly selects the interactive display instead of the readable log.

Run one command and log only it and its observed descendants:

```fish
bottom trace -- make test
```

`bottom trace -poll 10ms -tail 2s -- COMMAND` controls descendant discovery and how long Bottom watches surviving descendants after the root command exits.

## Platform support

| Platform | Automatic source | Polling fallback |
|---|---|---|
| Linux | Process connector | `/proc` |
| macOS | Endpoint Security when the signed binary is entitled | Native process table |
| Windows | ETW | Tool Help process table |
| Other Unix | Process table polling | `ps` |

Native source failures are logged and automatically fall back to polling. Capture gaps are printed explicitly.

The macOS requirements and signing procedure are in [docs/endpoint-security.md](docs/endpoint-security.md).

## Build and test

```fish
./build.sh
./test.sh
./benchmark.sh
./demo.sh
./install.sh
```

`build.sh` creates `/Users/jer/mygit/bottom/bottom`. `run.sh` starts the program without flags or configuration.

This is [donomii/bottom](https://github.com/donomii/bottom), not the unrelated current-state system monitor at [ClementTsang/bottom](https://github.com/ClementTsang/bottom). Release packages use the `bottom-events` name while the installed command remains `bottom`.
