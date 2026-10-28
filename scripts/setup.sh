#!/bin/zsh
set -euo pipefail
ROOT="${0:a:h:h}"
mkdir -p "$ROOT/.tools" "$ROOT/.cache"
if [[ $(uname -m) != arm64 ]]; then
  print -u2 'This first backend requires an Apple Silicon Mac.'
  exit 1
fi
if [[ ! -x "$ROOT/.tools/go/bin/go" ]]; then
  GO_ARCHIVE="$ROOT/.cache/go1.26.10.darwin-arm64.tar.gz"
  curl -fL --output "$GO_ARCHIVE" https://go.dev/dl/go1.26.10.darwin-arm64.tar.gz
  print "0b5183492292ada170f9b561a834863808dd02edba599e4ad0e89899330020b3  $GO_ARCHIVE" | shasum -a 256 --check
  tar -xzf "$GO_ARCHIVE" -C "$ROOT/.tools"
fi
HELPER="$ROOT/.tools/SimulatorFrameworkBridge-iOS"
if [[ ! -x "$HELPER" ]]; then
  ARCHIVE="$ROOT/.cache/idb-companion-v1.6.1.tar.gz"
  curl -fL --output "$ARCHIVE" https://github.com/facebook/idb/releases/download/v1.6.1/idb-companion.macos-arm64.tar.gz
  print "f59cadedbb05fe21c11522ee297cd8827b758136e27837a647267682cb2c80bb  $ARCHIVE" | shasum -a 256 --check
  STAGING=$(mktemp -d)
  trap 'rm -rf "$STAGING"' EXIT
  tar -xzf "$ARCHIVE" -C "$STAGING" ./Resources/SimulatorFrameworkBridge-iOS
  cp "$STAGING/Resources/SimulatorFrameworkBridge-iOS" "$HELPER"
fi
if [[ ! -f "$ROOT/.tools/LICENSE.idb" ]]; then
  curl -fL --output "$ROOT/.tools/LICENSE.idb" https://raw.githubusercontent.com/facebook/idb/v1.6.1/LICENSE
fi
print 'Local Go toolchain and native accessibility reader ready. Run make build (Xcode required).'
