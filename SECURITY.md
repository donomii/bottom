# Security Policy

## Supported Versions

Security fixes target the latest commit on the default branch until tagged releases exist.

## Reporting

Please report security issues privately through the [GitHub security advisory flow](https://github.com/donomii/bottom/security/advisories/new).

If that form is unavailable, open an issue containing no vulnerability details and ask the maintainer to enable a private reporting channel.

## Project Boundary

Bottom is a read-only process lifecycle recorder. It should not terminate, suspend, inject into, or modify processes.

Trace mode starts only the command explicitly supplied after `--`; it does not act on unrelated processes or surviving descendants.

## Recording Privacy

Process arguments, paths, user names, service units, and container identifiers can contain sensitive values. New files request owner-only access on Unix. Use repeated `-redact` values for exact text that must not reach the TUI, persistent recording, or Perfetto export. The default preserves exact event data and performs no rewriting.
