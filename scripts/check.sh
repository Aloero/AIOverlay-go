#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'USAGE'
Usage: scripts/check.sh [--race] [--dom | --ui]

Checks formatting, Go modules, tests, vet, Windows cross-build, JavaScript
syntax and the public example configuration without changing release files.

  --race  Enable Go's race detector for tests (requires a C compiler).
  --dom   Also run the DOM regression suites; install tests/ui dependencies first.
  --ui    Run all UI suites, including Chromium; includes --dom.
  --help  Show this help.

GO and NODE may name alternate executables, including paths containing spaces.
UI setup: npm ci --prefix tests/ui
Chromium setup: (cd tests/ui && npx playwright install --with-deps chromium)
USAGE
}

race=0
ui_mode=none
for option in "$@"; do
    case "$option" in
        --race) race=1 ;;
        --dom) if [[ "$ui_mode" != all ]]; then ui_mode=dom; fi ;;
        --ui) ui_mode=all ;;
        --help|-h) usage; exit 0 ;;
        *) printf 'Unknown option: %s\n' "$option" >&2; usage >&2; exit 2 ;;
    esac
done

root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$root"
go_bin=${GO:-go}
node_bin=${NODE:-node}
for executable in "$go_bin" "$node_bin"; do
    if ! command -v "$executable" >/dev/null 2>&1; then
        printf 'Required executable not found: %s\n' "$executable" >&2
        exit 1
    fi
done
export GO="$go_bin"
gofmt_bin="$("$go_bin" env GOROOT)/bin/gofmt"

if [[ "$ui_mode" != none ]]; then
    if ! "$node_bin" -e '
        const fromUI = require("node:module").createRequire(require("node:path").resolve("tests/ui/package.json"));
        fromUI.resolve("jsdom");
        if (process.argv[1] === "all") fromUI.resolve("playwright");
    ' "$ui_mode"; then
        printf 'Install UI dependencies first: npm ci --prefix tests/ui\n' >&2
        exit 1
    fi
fi

check_tmp=$(mktemp -d "${TMPDIR:-/tmp}/aioverlay-check.XXXXXX")
trap 'rm -rf -- "$check_tmp"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

printf '[check] Go formatting (read-only)\n'
mapfile -d '' -t go_files < <(find cmd internal web third_party/go-webview2 -type f -name '*.go' -print0)
if (( ${#go_files[@]} == 0 )); then
    printf 'No project Go sources found.\n' >&2
    exit 1
fi
unformatted=$("$gofmt_bin" -l "${go_files[@]}")
if [[ -n "$unformatted" ]]; then
    printf 'Run gofmt on these files:\n%s\n' "$unformatted" >&2
    exit 1
fi

printf '[check] Go module consistency and checksums\n'
"$go_bin" mod tidy -diff
"$go_bin" mod verify

printf '[check] Go tests\n'
test_args=(-count=1)
if (( race )); then test_args+=(-race); fi
"$go_bin" test "${test_args[@]}" ./...

printf '[check] Native and Windows static analysis\n'
"$go_bin" vet ./...
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$go_bin" vet ./...

printf '[check] Windows amd64 cross-build\n'
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$go_bin" build -trimpath -o "$check_tmp/AIOverlay.exe" ./cmd/aioverlay

printf '[check] JavaScript syntax\n'
while IFS= read -r -d '' source; do
    "$node_bin" --check "$source"
done < <(find web/static tests/ui -type d -name node_modules -prune -o -type f \( -name '*.js' -o -name '*.cjs' \) -print0)

printf '[check] Public configuration preflight\n'
"$go_bin" run ./cmd/preflight -root "$root" -config "$root/examples/config.example.json"

if [[ "$ui_mode" == all ]]; then
    printf '[check] Complete UI suite (DOM and Chromium)\n'
    "$node_bin" tests/ui/run.cjs
elif [[ "$ui_mode" == dom ]]; then
    printf '[check] DOM regression suites\n'
    "$node_bin" tests/ui/run.cjs --dom-only
fi
printf '[check] PASS\n'
