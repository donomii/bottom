# Package Recipes

The package identifier is `bottom-events`; the installed command is `bottom`. Recipes are pinned to a published release and verify its platform archive checksum.

## Homebrew

The formula at `packaging/homebrew/bottom-events.rb` supports macOS and Linux on amd64 and arm64. From a checkout, install it with:

```fish
brew install --formula ./packaging/homebrew/bottom-events.rb
```

The formula installs the command and `bottom(1)` manual page.

## Scoop

The manifest at `packaging/scoop/bottom-events.json` supports Windows on amd64 and arm64. Install that manifest with `scoop install ./packaging/scoop/bottom-events.json` from a checkout.

## Updating recipes

After publishing a release, update every version, archive URL, and checksum in both recipes. Validate the Ruby syntax, JSON syntax, and every checksum against the release assets before committing the update.
