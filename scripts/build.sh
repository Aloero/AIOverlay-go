#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; cd "$ROOT"
GO="${GO:-go}"
CONFIG="${1:-config.json}"
[[ -f "$CONFIG" ]] || { echo "missing $CONFIG"; exit 1; }
TOOL_DIR="$ROOT/.build-tools"
WINRES="$TOOL_DIR/go-winres"
if [[ ! -x "$WINRES" ]]; then
  mkdir -p "$TOOL_DIR"
  GOBIN="$TOOL_DIR" "$GO" install github.com/tc-hib/go-winres@v0.3.3
fi
mkdir -p build
EXE="$($GO run ./cmd/buildmeta --config "$CONFIG" --out build/winres.json | tail -n1)"
rm -f cmd/aioverlay/rsrc_windows_*.syso
"$WINRES" make --in build/winres.json --arch amd64 --out cmd/aioverlay/rsrc
"$GO" test ./...
"$GO" vet ./...
rm -rf dist
mkdir -p dist/runtime/{opencode,catalog,sing-box}
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$GO" build -trimpath -ldflags '-s -w -H=windowsgui' -o "dist/$EXE" ./cmd/aioverlay
cp "$CONFIG" dist/config.json
cp README.md VERIFICATION.md THIRD_PARTY_NOTICES.txt dist/
cp scripts/Install-WebView2.ps1 dist/Install-WebView2.ps1
cp runtime/opencode/opencode.exe runtime/opencode/LICENSE dist/runtime/opencode/
cp runtime/sing-box/sing-box.exe runtime/sing-box/libcronet.dll runtime/sing-box/LICENSE runtime/sing-box/VERSION.txt dist/runtime/sing-box/
cp runtime/catalog/models-dev-api.json runtime/catalog/opencode-enabled.json dist/runtime/catalog/
echo "Built dist/$EXE"
