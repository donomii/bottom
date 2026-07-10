# Contributing

Thanks for helping with bottom.

## Development

```sh
./test.sh
./benchmark.sh
```

`./test.sh` runs `go vet ./...`, `go test ./...`, and `go run . -test`.

Before submitting a platform change, run the native test suite on that platform. CI builds and tests Linux, macOS, and Windows; Linux also runs race coverage.

## Pull Requests

- Keep the CLI read-only.
- Update `SPEC.md` when user-visible behavior, output fields, config, or backend behavior changes.
- Add or update tests for new behavior.
- Keep process snapshot errors actionable: include the attempted task, expected result, received result, and useful values.
- Preserve structured gap reporting whenever capture completeness can change.
- Keep new recording fields versioned and migrate existing SQLite databases without rewriting invalid data.
- Do not make tests depend on internet access.

## Source Map

- `backend_poll.go` and `lifecycle_helpers.go`: snapshot lifecycle and stable identity.
- `platform_events_linux.go`: direct Linux connector lifecycle events.
- `snapshot_*.go`: native platform snapshots.
- `recorder*.go`: output pipelines, session metadata, SQLite, rotation, retention, redaction, and triggers.
- `recording_*.go`: query, report, replay, and comparison.
- `trace.go`: scoped descendant tracing and Perfetto export.
- `churn.go`, `filters.go`, and `tui.go`: classification and interactive presentation.

## Contribution-Sized Work

- Add fixture coverage for unusual macOS process argument layouts.
- Add Windows owner lookup through the existing background identity resolver.
- Add a native event source for macOS or Windows while retaining polling fallback and structured gaps.
- Add Fish, POSIX-shell, and PowerShell completion generation.
- Add package recipes after the first tagged release establishes stable archive URLs.
