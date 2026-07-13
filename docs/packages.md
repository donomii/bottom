# Package Recipes

The package identifier is `bottom-events`; the installed command is `bottom`. Public recipes are pinned to a published release and verify its platform archive checksum.

## Homebrew

The formula is published at [https://github.com/donomii/homebrew-tap](https://github.com/donomii/homebrew-tap) and supports macOS and Linux on amd64 and arm64. Install it with:

```fish
brew install donomii/tap/bottom-events
```

The formula installs the command and `bottom(1)` manual page.

The macOS archive includes the native Endpoint Security backend. It remains subject to Apple's entitlement, signing, Full Disk Access, and privilege requirements described in `docs/endpoint-security.md`; automatic backend selection falls back to polling when they are unavailable.

## Scoop

The manifest is published at [https://github.com/donomii/scoop-bucket](https://github.com/donomii/scoop-bucket) and supports Windows on amd64 and arm64:

```text
scoop bucket add donomii https://github.com/donomii/scoop-bucket
scoop install bottom-events
```

## Updating recipes

After publishing a release, update every version, archive URL, and checksum in both local recipes and their public package repositories. Validate the Ruby syntax, JSON syntax, and every checksum against the release assets before committing the update.

## Verifying a release

`checksums.txt` covers every archive and SPDX JSON SBOM. From a directory containing the downloaded release assets:

```fish
shasum -a 256 -c checksums.txt
for archive in bottom-events_*.tar.gz bottom-events_*.zip
    gh attestation verify $archive --repo donomii/bottom
end
```

The checksum detects changed bytes. The GitHub attestation additionally verifies that each archive was produced by this repository's release workflow.
