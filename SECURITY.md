# Security Policy

## Supported Versions

Security fixes target the latest tagged release and the default branch. Users should run the latest release because process capture fixes are not backported indefinitely.

## Reporting

Please report security issues privately through the [GitHub security advisory flow](https://github.com/donomii/bottom/security/advisories/new).

If that form is unavailable, open an issue containing no vulnerability details and ask the maintainer to enable a private reporting channel.

## Project Boundary

Bottom is a read-only process watcher. It should not terminate, suspend, inject into, or modify processes.

Trace mode starts only the command explicitly supplied after `--`; it does not act on unrelated processes or surviving descendants.

## Log Privacy

Process arguments, paths, and user names can contain sensitive values. Redirected logs preserve observed values, so store and share them accordingly.
