# Changelog

## Unreleased

- Removed the persistent recording subsystem, structured file formats, session metadata, query, replay, report, comparison, and Perfetto export. Bottom now writes its readable process log directly to standard output.
- Removed automatic churn and service-correlation events, filtering, triggered ring capture, output rotation, redaction, asynchronous buffering, OpenTelemetry, SQLite, and container and service-unit metadata.
- Removed the public `-test` mode; development checks run through `go test`.
- Added `-ppid` to include each process's parent PID in readable watch and trace output.
- Added immediate TUI navigation, editable search, adaptive sizing, column layouts, and sortable views with a line-oriented fallback.
- Replaced the TUI's internal state dump with a compact source and event summary plus control hints.
- Made `q` stop the TUI immediately from any state and Escape cancel active search input or quit outside search.
- Added natural-exit lifecycle smoke checks for polling on every platform and native Linux and Windows event sources.
- Corrected Windows ETW private system logger creation to use its own session GUID rather than the reserved kernel logger GUID.
- Added release-note generation from the matching changelog section, per-archive SPDX JSON SBOMs, and keyless GitHub build provenance.
- Published checksum-pinned recipes through the `donomii/homebrew-tap` and `donomii/scoop-bucket` repositories.
- Updated the demo, platform documentation, completion metadata, contributor guidance, and supported-version policy.

## 0.1.2 - 2026-07-11

- Added Windows process-owner attribution using token SIDs with background account-name resolution.
- Added native Windows command-line capture with executable-name fallback when access is unavailable.
- Added native Windows ETW process start and exit capture with exact exit status, bounded delivery, and periodic reconciliation.
- Added an entitlement-gated macOS Endpoint Security backend with native fork, exec, and exit capture, sequence-gap reporting, documented signing requirements, and polling fallback.
- Added bounded chronological query, replay, and report views across up to 64 explicitly supplied recordings.
- Added disabled-by-default local OTLP/HTTP log export with bounded background batching and loopback-only endpoint validation.
- Added immediate systemd service-restart events and Linux cgroup v2 memory-pressure exit correlation.
- Added checksum-pinned Homebrew and Scoop recipes using the `bottom-events` package identifier.
- Added native macOS release builds for Intel and Apple silicon alongside portable Linux and Windows archives.

## 0.1.1 - 2026-07-10

- Added fish completion generation and a packaged `bottom(1)` manual page.

## 0.1.0 - 2026-07-10

- Replaced recurring helper-process polling on Linux, macOS, and Windows with native read-only process capture.
- Added direct Linux fork, exec, and exit capture with kernel timestamps, sequence-gap detection, receive-overflow diagnostics, and periodic resynchronization.
- Corrected process lifetimes by preserving first observation and native start times across refreshes.
- Added stable native macOS and Windows snapshots without recurring helper processes.
- Added backend selection with Linux process connector support and structured polling fallback transitions.
- Set the default polling fallback to 100ms and documented millisecond poll intervals.
- Added versioned capture-gap records, session identity, host, boot identity, sequence, source time, and observation time.
- Removed unimplemented backend names from the selectable backend list.
- Added owner-only text, JSONL, CSV, and SQLite outputs with bounded buffering, exact redaction, simultaneous TUI/file fan-out, retention, and rotation.
- Added SQLite schema migrations, session and gap tables, raw versioned event JSON, prepared batches, timed flush, and query indexes.
- Added literal, regular-expression, UID, ancestor, service, container, time, duration, and exit-code filters.
- Added bounded semantic restart-loop detection with short-lifetime correlation, TTL eviction, sustained-report cooldown, and source-context retention.
- Added pre-trigger ring recording for churn, gaps, failed exits, and regular-expression matches.
- Added parent-chain, cgroup, systemd unit, container, TTY, process-session, and numeric-UID attribution where available.
- Added interactive pause, search, scrolling, event details, coverage status, replay, and Unicode-safe display.
- Added SQLite query, replay, report, and comparison commands.
- Added read-only, nanosecond-ordered SQLite streaming with indexed prefilters, early limits, input/output alias rejection, and file-backed exact report aggregation.
- Added scoped descendant tracing with SQLite recording, optional Perfetto export, preflight output separation, and guaranteed natural root reaping.
- Added release and source-build identity reporting plus terminal-control escaping for human-readable output.
- Added graceful recorder closure on operating-system interruption.
- Added native-platform, connector-parser, lifecycle, storage, trigger, filter, churn, TUI, reader, trace, comparison, and race coverage.
- Added Linux, macOS, and Windows CI, release automation for checksummed archives, benchmarks, repository topic guidance, and a social preview.
