#!/bin/zsh
set -euo pipefail
ROOT="${0:a:h:h}"
APP="$ROOT/build/Agent Phone.app"
mkdir -p "$APP/Contents/MacOS"
xcrun swiftc -parse-as-library -O -target arm64-apple-macosx14.0 -sdk "$(xcrun --sdk macosx --show-sdk-path)" "$ROOT/dashboard/AgentPhone.swift" -o "$APP/Contents/MacOS/AgentPhone"
python3 - "$APP" "$ROOT/build/agent-phone" <<'PY'
import plistlib,sys
from pathlib import Path
info={"CFBundleExecutable":"AgentPhone","CFBundleIdentifier":"dev.agent-phone.dashboard","CFBundleName":"Agent Phone","CFBundlePackageType":"APPL","CFBundleShortVersionString":"0.1.0","CFBundleVersion":"1","NSHighResolutionCapable":True,"LSMinimumSystemVersion":"14.0","AgentPhoneCLIPath":sys.argv[2]}
Path(sys.argv[1],"Contents/Info.plist").write_bytes(plistlib.dumps(info))
PY
codesign --force --sign - "$APP"
print "Built $APP"
