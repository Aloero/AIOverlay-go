# Verification · AIOverlay-go 0.2.7

Host: Linux amd64. Target: Windows 10/11 x64. Date: 2026-09-17. This document separates executed automation from Windows desktop acceptance. The underlying 0.2.5 project and Git history have been preserved; changes address the audit baseline `261f569`.

## Executed checks

The repaired working tree passed `go test ./... -count=1`: **172 test/subtest pass events, 0 failures**. Two sing-box integration tests are skipped in this ordinary environment-only run because they require an explicit host binary. They were also executed separately using the verified Linux sing-box 1.14.1 test runtime, **five consecutive runs**, including generated config check, preserved DNS dependencies, IPv4 endpoint policy, authenticated local proxy readiness/reuse and shutdown. No external tunnel server or provider account was contacted.

`go vet ./...`, Windows-target vet and Windows x64 CGO-disabled cross-build passed. Race detection across the Go packages passed with the available Zig C compiler. The release command repeats critical engine/providers/app/control/desktop/platform/tunnel/hotkey tests 20 times and records the result rather than assuming the previous run applies. Per-command logs are in `releases/verification/` after release generation.

All shipped JavaScript and UI test scripts passed Node syntax checks. The browser checks use the real Go control handlers with an isolated config and explicit development bearer token:

- DOM/native-binding simulation: **23 assertions passed**.
- Additional lifecycle audit regressions: **6 scenarios passed**, covering edits during Save+Close, pending provider draft protection, Save/Discard/Cancel choices, and catalog pools with null models.
- Chromium rendering/interaction: **45 assertions passed**, including prior graph/provider/Alt/layout flows plus benchmark report rendering and hotkey-editor core commands.

The added Studio UX suite passed **56 assertions** against Chromium and real Go handlers: applied/draft separation, independent order/dispatch, bounded draft undo/redo and restore, favorites, virtual simulation with no inference, explicit probe consent, URI import, compact VPN editing, inheritance, graph fallback visibility, resize/panel state, run summaries, journal preservation and 1024/820 viewport bounds. AI-provider and VPN network probe buttons are stubbed in this browser suite; confirmation wiring is tested, not real external connectivity.

Browser viewports include 820×614, 1024×768 and 1320×880. These are Linux Chromium rendering checks with a simulated native binding, not real WebView2/Windows focus tests. Browser tests forbid inference. Mock providers and local HTTP fixtures are explicitly test-only.

`go mod verify` passed. Windows-target `govulncheck` after updating `golang.org/x/sys` to v0.44.0 reported **no vulnerabilities found** in the scanned Go graph. The maintained local WebView2 host fork is included in that graph; external OpenCode/sing-box executable internals, libcronet.dll and the installed WebView2 browser are not covered by this Go scan.

## Regressions integrated into ordinary tests

The audit's original engine/config/provider/control/trace/tunnel reproductions now live in `audit_regression_test.go` files. They verify independent prompt scopes, validator fallback screenshot retention, allowed credential reuse at the key cap, provider timeout defaults, stage streaming isolation, cooperative cancellation cleanup, fastest-healthy ordering, exhausted edge fallback, strict config semantics, unknown-field retention, cancel-during-capture, headless authorization/Origin and true size boundary, malformed Gemini URL, runtime liveness, structured authentication outcomes, redaction before public errors, immutable snapshots, and VPN DNS/IPv4 policy.

Additional tests verify cross-chunk secret redaction; disabled explicit pipelines; warm state after UI/label saves; batch benchmark cancellation/grading/median; optional statistics not replacing quality; JSON schema migration/duplicate fields/unknown large numbers; forbidden credential environment overrides; atomic-file failure retaining last good data; private config/LFS release guards; staged Git index inspection; ZIP CRC, extraction, traversal rejection and exact manifest coverage.

## Native Windows code: compiled, not physically executed here

`internal/desktop/acceptance_windows_test.go` provides an opt-in interactive test of ten actual WebView2 open/close cycles and checks destruction of each old HWND. It requires `AIOVERLAY_WINDOWS_ACCEPTANCE=1` on a Windows desktop with WebView2 installed. The test binary was cross-compiled, not run on Linux.

`internal/atomicfile/write_windows_test.go` checks that a real Windows sharing violation prevents replacement while preserving the prior config. Its test binary was cross-compiled. This is not a claim about simulated power loss or every filesystem/antivirus combination.

STA initialization, synchronous controller/reference release, session-generation dispatch, initially hidden privacy setup, Job Object assignment before child resume, RegisterHotKey rollback, editor suppression, capture-affinity readback and NUL-safe DrawText are reviewed/cross-compiled native code. Source-contract tests confirm important boundaries but cannot prove correct physical desktop behavior by themselves.

Actual Windows 10/11 launch, WebView2 COM behavior, child-process memory reclamation, native capture/driver cleanup, real global hotkeys/focus, Windows Firewall/antivirus, OBS/WGC/DXGI invisibility, Task Manager grouping and live VPN/cloud-provider traffic were **not physically tested**. No RAM reduction percentage or byte count is asserted.

## Release verification

Use `go run ./cmd/release --race` with Node/Chromium test dependencies and a working C compiler for the complete release workflow. The checked-in command defaults to the public `examples/config.example.json`, never a user's live config. It rejects private configuration and LFS pointer payloads, regenerates PE resources and compile-time identity, runs clean build/tests/vet/JS/UI checks, extracts PE resources from the finished EXE, and verifies ProductName, FileDescription, CompanyName, OriginalFilename, four-part FileVersion and icon.

Both `AIOverlay-go_Source.zip` and `AIOverlay-go_Windows.zip` receive fresh complete SHA-256 manifests. The command reads every ZIP entry (verifying CRC), extracts each into a fresh directory, rejects traversal/duplicates/symlinks/unlisted files and verifies every extracted hash. It then reruns tests/vet, regenerates resources and rebuilds the Windows EXE from the extracted Source ZIP. The release is published only when the EXE SHA-256 matches the Windows archive payload. Versioned release tooling excludes crash dumps, node_modules, build tools and private runtime files; source payload gets a separately checked public config.

Final per-archive SHA-256 values, checks run or explicitly skipped, source commit and EXE hash are generated in `releases/RELEASE.json`, `releases/SHA256SUMS.txt` and `BUILD.json`. They are not handwritten into this document. A successful cross-build or reproducible ZIP does not upgrade the native acceptance status above.

## Known implementation boundaries retained

OpenCode's bridge is buffered; measured incremental TTFT/TPS remain unavailable. Its internal requests and retry policies are not a packet-level audit of our adapter admissions. Output token ceiling is forwarded to a supported runtime setting but was not measured against live cloud inference. OpenCode may write session data; cancellation/deletion is best effort with errors logged.

VPN integration is an authenticated application-local userspace proxy with IPv4 endpoint resolution, no TUN or global proxy/route changes. It does not claim a Windows OS-enforced egress sandbox for arbitrary SDK behavior. A real Windows packet trace is still required for that acceptance question.


## Studio UX 0.2.7 regressions

Schema 2→3 recursively maps every legacy policy to independent order/dispatch fields. Tests cover combinations, explicit overrides and invalid enums. Read-only queue preview does not advance a live round-robin cursor. Virtual scheduling tests cover sequential fallback, staggered reserve/loser cancellation, real admission-budget accounting, key rotation, private copied cursor state and the exact 256-call safety bound. The simulator owns no provider registry and adds nothing to real run history.

Confirmed connection tests are exercised with mock Go providers: exactly one adapter attempt, the selected saved synthetic credential, no image, a 32-token request cap, no active-pipeline replacement, and safe credential status after completion. Unconfirmed tests are rejected before job creation. Catalog shortcuts and limit-source responses are checked through real control handlers. UI/profile-label saves preserve warm engine/transports.

VLESS, Hysteria2/hy2 and Trojan import fixtures are checked with the real Linux sing-box binary when explicitly enabled, including WebSocket and gRPC profiles. Unsupported/duplicate/conflicting link parameters are rejected without returning URI secrets. The separate HTTPS connection-test button is consent-tested with a browser fixture; no live VPN server, external HTTPS probe or cloud model request was sent during this verification.

The source tree keeps the local user config excluded. Release version/build identity comes from the public schema-3 example; the private user's prior schema-2 config is not overwritten by this development operation. See STUDIO_UX.md for user-visible semantics and limitations.
