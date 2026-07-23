# Repository Settings

Apply these values at [https://github.com/donomii/bottom/settings](https://github.com/donomii/bottom/settings) after the repository changes are committed and pushed.

## Description

```text
A read-only process watcher that logs what starts and stops.
```

## Homepage

```text
https://github.com/donomii/bottom#readme
```

## Topics

```text
process-monitor
process-lifecycle
process-events
process-history
procfs
observability
go
cli
tui
terminal
linux
macos
windows
```

## Social preview

Upload `docs/social-preview.png`. Its source is `docs/social-preview.svg`; both are 1280 by 640 and kept together so the text remains editable.

## Security

At [https://github.com/donomii/bottom/settings/security_analysis](https://github.com/donomii/bottom/settings/security_analysis), enable private vulnerability reporting, Dependabot alerts and security updates, secret scanning, secret-scanning validity checks, and push protection. `.github/dependabot.yml` separately requests weekly Go-module and GitHub Actions version updates, with at most five open update requests for each ecosystem.

## Default branch rules

At [https://github.com/donomii/bottom/settings/rules](https://github.com/donomii/bottom/settings/rules), protect `master`, require a pull request, and require every `Test` workflow context, including the Linux race check, before merging.

## Actions permissions

At [https://github.com/donomii/bottom/settings/actions](https://github.com/donomii/bottom/settings/actions), set the default `GITHUB_TOKEN` permission to read repository contents and packages, and leave workflow pull-request creation and approval disabled. The test workflow requests only `contents: read`; the release job requests `contents: write` only after its Linux, macOS, Windows, and race checks pass.

## Package identifier

Use `bottom-events` for archives and package-manager entries. The installed command remains `bottom`.
