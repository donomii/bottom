# Contributing

Thanks for helping with bottom.

## Development

```sh
./test.sh
```

`./test.sh` runs `go test ./...` and `go run . -test`.

## Pull Requests

- Keep the CLI read-only.
- Update `SPEC.md` when user-visible behavior, output fields, config, or backend behavior changes.
- Add or update tests for new behavior.
- Keep process snapshot errors actionable: include the attempted task, expected result, received result, and useful values.

## Useful Starting Points

- Add packaged event backends for Linux eBPF, Windows WMI event subscriptions, or signed macOS Endpoint Security builds.
- Improve process identity on platforms that do not expose a stable start token.
- Add package recipes after release binaries exist.
