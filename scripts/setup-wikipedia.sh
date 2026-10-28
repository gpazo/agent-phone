#!/bin/zsh
set -euo pipefail
ROOT="${0:a:h:h}"
SOURCE="$ROOT/.cache/wikipedia-ios"
REVISION=f212471a91279275bdaf77f7fe2c85508a6039ed # published releases/7.9.3
mkdir -p "$ROOT/.cache"
if [[ ! -d "$SOURCE/.git" ]]; then
  git clone --depth 1 --branch releases/7.9.3 https://github.com/wikimedia/wikipedia-ios.git "$SOURCE"
fi
if [[ "$(git -C "$SOURCE" rev-parse HEAD)" != "$REVISION" ]]; then
  print -u2 "Wikipedia checkout differs from the pinned demo revision. Preserve your changes and use releases/7.9.3 ($REVISION)."
  exit 1
fi
git -C "$SOURCE" rev-parse HEAD > "$ROOT/.cache/wikipedia-revision.txt"
# Simulator builds need neither a developer account nor the lint tool installers.
if [[ ! -f "$SOURCE/Configurations/OpenSourceDebug.xcconfig" ]]; then
  (cd "$SOURCE" && bash scripts/setup_bundle_id ci)
fi
print "Wikipedia source: $SOURCE"
print "Revision: $(cat "$ROOT/.cache/wikipedia-revision.txt")"
