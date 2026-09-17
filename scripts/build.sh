#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"; cd "$ROOT"
GO="${GO:-go}"
CONFIG="${1:-examples/config.example.json}"
[[ -f "$CONFIG" ]] || { echo "missing $CONFIG"; exit 1; }
"$GO" run ./cmd/preflight --config "$CONFIG" --runtimes
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
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$GO" build -buildvcs=false -trimpath -ldflags '-s -w -H=windowsgui' -o "dist/$EXE" ./cmd/aioverlay
cp "$CONFIG" dist/config.json
cp README.md ARCHITECTURE.md VERIFICATION.md THIRD_PARTY_NOTICES.txt dist/
for doc in AUDIT_FIXES.md SECURITY.md; do [[ ! -f "$doc" ]] || cp "$doc" dist/; done
cp runtime/manifest.json dist/runtime/
cp scripts/Install-WebView2.ps1 dist/Install-WebView2.ps1
cp runtime/opencode/opencode.exe runtime/opencode/LICENSE dist/runtime/opencode/
cp runtime/sing-box/sing-box.exe runtime/sing-box/libcronet.dll runtime/sing-box/LICENSE runtime/sing-box/VERSION.txt dist/runtime/sing-box/
if [[ -f runtime/sing-box/GPL-3.0.txt ]]; then cp runtime/sing-box/GPL-3.0.txt dist/runtime/sing-box/; fi
cp runtime/catalog/models-dev-api.json runtime/catalog/opencode-enabled.json dist/runtime/catalog/
echo "Built dist/$EXE"
