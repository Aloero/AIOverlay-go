# Agent rules (AIOverlay-go)

Development runs in Linux/WSL; the app targets Windows 10/11 x64 and is built by cross-compilation only. If a rule blocks you, stop and ask the user.

## 1. Never touch Windows from WSL

- No Windows interop: never run `cmd.exe`, `powershell.exe`, `wine` or any `.exe`; never execute the `*.ps1` scripts (they ship to Windows users).
- Never read or write `/mnt/c` or any Windows path; never change Windows registry, services, PATH or installed software (e.g. WebView2 Runtime).
- Testing the built `.exe` on Windows is the user's job; agents may only ask them to run it.
- Correct workflow: `scripts/check.sh`, `scripts/build.sh` (i.e. `GOOS=windows CGO_ENABLED=0 go build`), `go test ./...`. Line endings are fixed by `.gitattributes` (`eol=lf`) — don't change it.

## 2. Keep the repo clean

- No litter in the repo: temporary files, ad-hoc scripts and drafts go to `/tmp/opencode`, not here.
- Build outputs only in `build/`, `dist/`, `.build-tools/`, `releases/` (gitignored). Never drop `.exe`/`.zip`/logs/tmp files into the root or source dirs.
- Delete your own leftovers when done; before finishing, `git status --porcelain` must show only intended changes.
- No dead code, commented-out blocks, `*_old.*`, `.bak` or scratch files; remove what's unused.
- Respect `.gitignore`: never commit `config.json` (private API keys — never echo them), `BUILD.json`, `MANIFEST.sha256` or build artifacts.
- Docs: extend the canonical files (`README.md`, `ARCHITECTURE.md`, `VERIFICATION.md`, `SECURITY.md`, …) or `docs/`; don't create one-off markdown in the root.
- Run `go mod tidy` after dependency changes; don't touch other people's uncommitted work.
