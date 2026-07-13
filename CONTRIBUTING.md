# Contributing

Thanks for helping with bottom.

## Development

```fish
./test.sh
./benchmark.sh
```

`./test.sh` runs `go vet ./...`, `go test ./...`, and `go run . -test`.

Before submitting a platform change, run the native test suite on that platform. CI builds and tests Linux, macOS, and Windows, exercises natural-exit polling on every platform and native event delivery on Linux and Windows, runs Linux race coverage, and verifies portable release SBOM generation.

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
- `platform_events_linux.go`, `platform_events_windows_etw.go`, and `platform_events_darwin_endpoint.go`: native lifecycle events and reconciliation.
- `snapshot_*.go`: native platform snapshots.
- `recorder*.go`: output pipelines, session metadata, SQLite, rotation, retention, redaction, and triggers.
- `recording_*.go`: query, report, replay, and comparison.
- `trace.go`: scoped descendant tracing and Perfetto export.
- `churn.go`, `filters.go`, and `tui.go`: classification and interactive presentation.

## Contribution-Sized Work

- Add fixture coverage for unusual macOS process argument layouts.
- Add unusual macOS process-argument fixtures and signed Endpoint Security smoke coverage.
- Add POSIX-shell and PowerShell completion generation alongside Fish.
- Add automatic release updates for the public Homebrew tap and Scoop bucket without broad repository credentials.
- Add follow mode for querying an active SQLite recording.
- Add terminal-width and key-sequence fixtures for the interactive timeline.

## Releases

Before tagging, replace the `Unreleased` changelog heading with the exact version and date, such as `## 0.2.0 - 2026-07-13`. The release workflow runs `go run ./tools/releasenotes`; it refuses to publish a tag without a nonempty matching changelog section. Published archives include checksums, SPDX JSON SBOMs, and GitHub build provenance.
