# macOS Endpoint Security

The macOS release archives include the native `macos-endpoint-security` backend. It receives fork, exec, and exit notifications with exact wait status, reports Endpoint Security sequence gaps, and periodically reconciles against the native process table.

Apple restricts Endpoint Security clients. The running binary must:

- be signed by a development team for which Apple granted the Endpoint Security entitlement;
- include `com.apple.developer.endpoint-security.client` as shown in `packaging/macos/bottom.entitlements`;
- have Full Disk Access;
- run with the privilege required by the installed macOS version.

After Apple grants the entitlement, sign the release binary with the corresponding Developer ID identity:

```fish
codesign --force --options runtime --entitlements packaging/macos/bottom.entitlements --sign "Developer ID Application: NAME (TEAMID)" ./bottom
```

Verify that the signed binary contains the entitlement:

```fish
codesign --display --entitlements - ./bottom
```

`bottom -backend macos-endpoint-security` requests the native backend explicitly and returns an actionable error if a requirement is missing. The default `bottom -backend auto` attempts Endpoint Security and emits a structured capture-gap record before falling back to 100 millisecond native process-table polling when the client cannot start.
