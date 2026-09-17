# Verification · AIOverlay-go 0.2.5

Date: 2026-09-17. Build host: Linux amd64. Application target: Windows 10/11 amd64, CGO disabled. This is a verification of the modified existing 0.2.2 project, not a new implementation.

## Automated checks

- `go test ./... -count=1`: PASS, 94 passing test/subtest events, zero failures.
- `go vet ./...`: PASS.
- `GOOS=windows GOARCH=amd64 go vet ./...`: PASS.
- `go test ./internal/engine ./internal/providers ./internal/app ./internal/control ./internal/desktop ./internal/platform ./internal/tunnel -count=20`: PASS.
- Race detector across all Go packages: PASS on Linux. Command on this host: `CGO_ENABLED=1 CC='/tmp/zig-linux-x86_64-0.13.0/zig cc -target x86_64-linux-gnu' go test -race -ldflags=-linkmode=external ./... -count=1`.
- `node --check` for every shipped Studio JS module and UI test script: PASS.
- DOM/native-binding simulation against real Go handlers: 23 assertions PASS, no JavaScript errors.
- Chromium rendering and interaction suite against real Go handlers through a simulated native binding: 41 assertions PASS, no JavaScript errors. Captured 820x614, 1024x768 and 1320x880 logical viewports. Bounds checks confirm the inspector, graph, save and help controls fit the 1024x768 viewport.
- Both UI suites forbid inference; no live model request was made. The isolated runner creates its own config and server, without changing the project or user runtime configuration.

The DOM suite uses jsdom 26.1.0. Rendering uses pinned Playwright 1.63.0 / Chromium on Linux. On this minimal host, required Chromium shared libraries and fonts were extracted into a temporary directory without modifying the application runtime. Neither browser test dependencies nor these libraries/fonts are shipped in the Windows archive.

## New regressions

Catalog-only execution works with an empty pinned list, falls through a failed model, retains the original screenshot, records both actual adapter attempts and masks credentials. Tests cover configured/disabled routes, FREE and missing/incomplete-price handling, image/text compatibility, non-text output exclusion, manual overrides, deduplication, unscored quality-floor enforcement, and the manual-before-catalog ordering tier.

Parallel/staggered tests enforce the smaller pool/global concurrency limit and actual adapter-call budget. A competing budget rejection does not cancel an already admitted successful answer. Existing winner-stream isolation, cancellation, fallback-image retention, credential rotation/cooldown, first-text timeout and redaction tests continue to pass.

OpenCode lease tests cover reuse for the same key, concurrent different-key requests, safe idle eviction, one start for concurrent same-key acquisitions and cleanup. Structured 401/429/503 OpenCode envelopes map to normalized outcomes without exposing raw error messages or response bodies. Environment tests verify that ordinary launch/TLS settings survive while provider secrets and arbitrary OpenCode/user settings are not inherited.

Configuration tests cover empty catalog pools and canvas-layout round trips, invalid selection mode/modality/provider/quality, and duplicate scoped providers. App/control tests cover provider-directory metadata, draft planning without inference or secrets, exact provider catalog filtering, exclusion of resource names/paths from inferred credential variables, independent provider saves, persistent activation, busy rejection and preservation of the warm registry/engine during activation.

UI tests cover provider discovery and automatic OpenCode identity/environment, multiple keys and literal newlines, deduplication/masking, catalog-only saving, visible effective budgets, full queue, panning and layout persistence, Alt hover/release, provider-specific model selection, new/existing pipeline activation and simultaneous attempt aliases. Warnings about plaintext secrets remain in the credential dialog, not the sidebar.

## Studio memory lifecycle

The default config no longer preloads Studio. Platform-independent lifecycle tests verify: first Show starts a session, repeated Show foregrounds it, Close tears it down, Show during teardown queues one reopen, and a completed normal Close leaves Studio unloaded. The Windows source contract requires `v.Destroy()` on session end and rejects the old WM_CLOSE-only-ShowWindow-hide behavior.

DOM tests verify that the Studio close control saves a valid dirty draft before calling `/desktop/close`. The existing recovery entry points still call `Show`, which now creates a new session when needed. Windows cross-compilation verifies the new manager/WebView2 code, but actual process-memory reclamation and child-process exit timing are not physically measured on Windows in this environment.

## AI VPN / sing-box

The bundled Windows amd64 sing-box 1.14.1 archive was downloaded from the official SagerNet release and its archive SHA-256 matched `5197f16d492d93202dc623622149a6ed040f8eca263128f91d603f2b901baa89`. The release files are stored under `runtime/sing-box/`; the upstream license and version provenance are shipped with them.

Pure tests verify that generated tunnel configuration binds only `127.0.0.1`, uses authentication, forces `set_system_proxy=false`, ignores pasted TUN/inbound/route configuration, rejects direct as a VPN outbound, and rejects ambiguous full configs with multiple tunnel outbounds. Provider tests verify explicit HTTP proxy wiring, that native adapters do not contact their target when the tunnel resolver fails, and that OpenCode does not start when the tunnel is unavailable; OpenCode environment tests verify that both HTTP proxy variables receive the application proxy while its local control API stays in `NO_PROXY`.

For host-side semantic verification, the official sing-box 1.14.1 Linux amd64 archive was downloaded temporarily and its GitHub release digest `12cb2816b52febb356f6a885b740cc8758c3f30b8ae0ca8edba80f0d2d35343f` was verified. `sing-box check` accepted a config generated by `internal/tunnel`, and the tunnel manager integration test started an authenticated loopback mixed proxy and shut it down. The Linux binary is verification-only and is not included in either release archive. No real VLESS/Hysteria/provider account was contacted.

## Capture and desktop

Capture remains CreateDIBSection → BitBlt → GdiFlush → copied BGRA → Go image/JPEG in memory. GetDIBits is not reintroduced. Existing capture allocation, conversion, errors and cleanup-order tests pass.

The visibility decision is separately tested: hidden and minimized Studio windows are not hidden/restored by screenshot capture. A visible non-minimized Studio is temporarily hidden to exclude settings from the source frame. Existing native overlay chrome, resize, Ghost Mode, tray recovery and privacy source-contract tests remain in place. Studio lifecycle tests now cover lazy start, close-to-unload, queued reopen and an unloaded default state.

## Cross-build and release integrity

The application is clean-built for `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` using the existing build script. PE icon/version/manifest resources are regenerated from the 0.2.5 config and extracted back from the final EXE. ProductName, FileDescription, CompanyName, OriginalFilename, FileVersion and an icon are checked.

Both release archives receive fresh MANIFEST.sha256 files, CRC tests and clean-directory extraction. Every extracted file is checked against its manifest, including coverage for unexpected/missing files. Go tests and vet are repeated from the extracted Source ZIP. The final ZIP hashes are recorded in `releases/SHA256SUMS.txt`; machine-readable build/archive results are in `BUILD.json` and `releases/RELEASE.json`.

## Not physically verified

No real Windows 10/11 desktop, WebView2 instance, GDI display/driver capture, native Alt/hotkey/focus behavior, OBS/WGC/DXGI recording or Task Manager grouping was tested on this Linux host. Chromium/native-binding simulation is not a Windows integration test. No real provider credentials, cloud inference or embedded Windows OpenCode executable were exercised.

The Studio connection flow is API-key based. It does not implement CLI OAuth/device login or all provider-specific enterprise identities. Configured/READY is not proof of remote access, credits, availability or region support.

The external OpenCode bridge remains buffered; it does not expose measured incremental TTFT/TPS. OpenCode may persist session data on disk; abort/delete is best effort, not proof of zero disk writes. Engine attempt events describe application adapter calls, not a packet-level audit of every request the external runtime may perform internally.
