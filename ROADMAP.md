# Roadmap

## Current implementation

- Trustworthy process lifetimes and stable identities.
- Direct Linux lifecycle events with explicit coverage gaps.
- Native Linux, macOS, and Windows snapshots.
- Versioned private recordings with query, replay, report, comparison, and Perfetto export.
- Semantic restart detection and triggered ring capture.
- Cross-platform CI and automation for checksummed release archives.

## Next

- Native Windows lifecycle events with exit attribution.
- A distributable native macOS event source with documented entitlement requirements and polling fallback.
- Windows owner identity and richer command-line visibility.
- Shell completion generation and manual pages.
- Package recipes using the `bottom-events` identifier after the first tagged release.

## Later

- Optional local OpenTelemetry export with no network activity unless explicitly configured.
- Additional local event correlation for memory-pressure termination and service restart activity.
- Recording views that merge multiple explicitly supplied host files without a central service.
