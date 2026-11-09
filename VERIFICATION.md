# Verification · AIOverlay-go 0.2.8

Host: Linux amd64. The latest audit below covers the working copy on 2026-11-09. Earlier dated sections are historical evidence. Current configuration schema is 5; minimum Go version is 1.27.3.

## Current audit · 2026-11-09

The initial Go run passed 595 test/subtest events across 22 packages with tests. After the audit fixes, `scripts/check.sh --race` passed all Go packages, race detection, module consistency/checksums, formatting, native and Windows vet, Windows amd64 cross-build, JavaScript syntax and public-config preflight.

`npm test` passed 38 smoke and 9 regression checks. The complete `npm run test:all` passed 167 checks across all six DOM/Chromium suites with no JavaScript errors. Three new UI regressions first reproduced import/save data loss and now pass. Provider redirects, child-process pipe ownership, malformed provider URLs, oversized configuration writes, preprocessor admission, image snapshot ownership, configuration export and private source inventory have dedicated regressions with recorded failing/passing runs.

Linux/amd64 and Windows/amd64 source scans with govulncheck 1.9.0 found no known vulnerabilities after raising Go to 1.27.3. npm audit found zero vulnerabilities. The separately bundled sing-box was updated to verified official 1.14.4. Both previously skipped tunnel integration tests passed with a verified Linux 1.14.4 runtime, together with the rest of the tunnel package under race detection. These tests use local validation and an authenticated loopback handshake, not a remote VPN endpoint.

The sing-box binary scan still reports 26 advisory IDs in 10 upstream dependencies; its stdlib findings are resolved. This is a remaining distribution risk, distinct from the clean AIOverlay source scan. Full details and limits are in [the audit report](docs/audits/2026-11-09.md) and [runtime findings](docs/audits/2026-11-09-runtime.json).

Local evidence is under `build/audit-2026-11-09/`: `repository-check-final.log`, `ui-full.log`, `tunnel-runtime-final.log`, the two `govulncheck-*-final.txt` files, and red/green regression logs. The new GitHub workflow passed actionlint and the shell entry point passed ShellCheck; GitHub-hosted CI was not triggered. Existing user changes were preserved. No commit/push, release archive regeneration, live provider call, remote VPN probe or physical Windows acceptance was performed.

## Historical SOLID refactor checks · 2026-09-19

This section records fresh checks on the uncommitted structural refactor based on
`ea156969f22b89e5e4c72f6b1767264cb68baeb3`, on `feature/audio-context-029`.
It is not release certification for the existing archives. Go 1.27.1, Node
v24.18.0 and the existing Zig C compiler were used from host `/tmp` toolchains;
no compiler or other toolchain was added to the repository.

The following completed successfully after the production changes:

```sh
go test ./... -count=1 -json
go vet ./...
CC=/tmp/aio-zig/cc CGO_ENABLED=1 go test -race -ldflags=-linkmode=external -p=2 ./... -count=1
go test ./internal/app ./internal/control ./internal/engine ./internal/session ./internal/config ./internal/tunnel ./internal/providers -count=20
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/aio-solid-build/AIOverlay.exe ./cmd/aioverlay
gofmt -l cmd internal web third_party/go-webview2
git diff --check
```

The ordinary Go run reported 595 passing test/subtest events and two explicit
host-binary skips: `TestAuditPreserveRequiredDNSOfFullProfile` and
`TestManagerStartsAuthenticatedLoopbackWithOverride`. The race run covered all
Go packages supported on this Linux host. All 26 `web/static/*.js` and
`tests/ui/*.cjs` files passed `node --check`. The formatting check had no output.
The Windows target was cross-built only; the resulting executable was not run.

Added regressions pin transport JSON fields, independent control capabilities,
benchmark snapshot ownership, attempt terminal/timeout/lease behavior, pending
transcript scope/wakeups and active-turn cancellation ownership. The pre-existing
post-semaphore cancellation leak was reproduced by a deterministic local test
before correction. Panic-unwind and timeout-callback ordering checks preserve
existing cleanup semantics during the extraction.

Seventeen source-validation cases pin valid inputs, error text and validation
order. Twenty-three permanent VPN import fixtures were captured from the old
implementation. A temporary differential check against that implementation also
matched complete results and errors for 3,150 locally generated URI combinations;
the temporary duplicate implementation was removed afterward. These are parser
and scheduling checks, not proof of real VPN or provider behavior.

The application, Chromium, physical Windows, live cloud providers, real VPN
endpoints and local Whisper inference were not run. Provider adapters, native
Windows code, web assets, pinned runtimes, dependency versions and configuration
schema were not changed. Existing `dist/` and `releases/` artifacts were not
regenerated and do not certify this working tree. The R2 plan HTML was not found
in the supplied checkout/search path; this pass preserves the existing R2
regressions and makes no new claim that the whole R2 plan is complete.

Host logs are `/tmp/aio-solid-final-test.jsonl`,
`/tmp/aio-solid-final-vet.log`, `/tmp/aio-solid-final-race.log`,
`/tmp/aio-solid-final-repeat.log`, `/tmp/aio-solid-windows-build.log` and
`/tmp/aio-solid-import-parity.log`. They are local verification evidence, not
release payloads.

## Historical requested-scope checks · 2026-09-19

For the current R2 branch state, `go test ./... -count=1` and `go vet ./...`
passed. All shipped JavaScript/CJS files passed Node syntax checks. The permanent
A01–A19 `R2Audit` regressions passed under the race detector in app, catalog,
control, engine, platform/audio, providers, releasepack and session packages,
five consecutive runs per package. Focused realtime, lifecycle and cancellation
regressions were additionally repeated at higher counts while their slices were
implemented. Current configuration schema is 5; schema-2/3 statements later in
this document are historical release notes, not the current config version.

Per the requested scope, this pass did **not** run the application, Chromium/UI
harnesses, Windows-target vet, a Windows desktop, a live cloud provider or a live
VPN endpoint. `cmd/release` therefore keeps DOM/Chromium (`--ui`) and Windows-target
vet (`--windows-verify`) opt-in instead of default release gates. Building the
Windows EXE remains an artifact-production step, not evidence of Windows runtime
acceptance.

Runtime materialization is now complete and content-verified. OpenCode v1.18.31
was downloaded from the official GitHub release; the ZIP SHA-256 matched GitHub's
release digest and the extracted `opencode.exe` exactly matched the pinned LFS
OID/manifest SHA-256. sing-box 1.14.1 was downloaded from its official Windows
amd64 release; the archive SHA-256 matched `runtime/sing-box/VERSION.txt`, and
both `sing-box.exe` and `libcronet.dll` exactly matched their pinned LFS OIDs.
The three payloads are also present in the local Git LFS object cache.
`go run ./cmd/preflight --runtimes` now passes. Release generation remains
fail-closed if any payload is missing, reverts to a pointer, or fails its hash.

## Historical 0.2.8 snapshot checks · not rerun for current HEAD

The repaired working tree passed `go test ./... -count=1`: **194 test/subtest pass events, 0 failures**. Two sing-box integration tests are skipped in this ordinary environment-only run because they require an explicit host binary. They were also executed separately using the verified Linux sing-box 1.14.1 test runtime, **five consecutive runs**, including generated config check, preserved DNS dependencies, IPv4 endpoint policy, authenticated local proxy readiness/reuse and shutdown. No external tunnel server or provider account was contacted.

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

## Historical native Windows code evidence · not rerun for current HEAD

`internal/desktop/acceptance_windows_test.go` provides an opt-in interactive test of ten actual WebView2 open/close cycles and checks destruction of each old HWND. It requires `AIOVERLAY_WINDOWS_ACCEPTANCE=1` on a Windows desktop with WebView2 installed. The test binary was cross-compiled, not run on Linux.

`internal/atomicfile/write_windows_test.go` checks that a real Windows sharing violation prevents replacement while preserving the prior config. Its test binary was cross-compiled. This is not a claim about simulated power loss or every filesystem/antivirus combination.

STA initialization, synchronous controller/reference release, session-generation dispatch, initially hidden privacy setup, Job Object assignment before child resume, RegisterHotKey rollback, editor suppression, capture-affinity readback and NUL-safe DrawText are reviewed/cross-compiled native code. Source-contract tests confirm important boundaries but cannot prove correct physical desktop behavior by themselves.

Actual Windows 10/11 launch, WebView2 COM behavior, child-process memory reclamation, native capture/driver cleanup, real global hotkeys/focus, Windows Firewall/antivirus, OBS/WGC/DXGI invisibility, Task Manager grouping and live VPN/cloud-provider traffic were **not physically tested**. No RAM reduction percentage or byte count is asserted.

## Release verification

Use `go run ./cmd/release --race` with Node and a working C compiler for the default release workflow. DOM/Chromium checks are opt-in with `--ui` and are not a default gate for the current scope. The checked-in command defaults to the public `examples/config.example.json`, never a user's live config. It rejects private configuration and LFS pointer payloads, regenerates PE resources and compile-time identity, runs clean build/tests/vet/JS syntax checks plus optional UI checks, extracts PE resources from the finished EXE, and verifies ProductName, FileDescription, CompanyName, OriginalFilename, four-part FileVersion and icon.

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


## Studio follow-up review 0.2.8

The additional `studio-review.cjs` suite passed **14 scenarios**, with zero JavaScript errors and no model/capture/probe requests. It covers focused Ctrl+S, edge save and undo/redo, changing inherited-limit labels, the VLM cursor key, terminal graph failure, exact history actions without activation, explicit-only last prompt loading, actual recent usage, smaller reserve nodes, nested trace disclosures, untried counts and compact viewport bounds. The previous DOM 23, lifecycle 6, Chromium 45 and UX 56 suites remain in the runner and passed too.

New Go regressions cover all 12 order/dispatch combinations with concurrency limits, real/simulated VLM-to-text-catalog agreement, graph-aware input states, final Validator failure metadata, untried models independent of retries, private-RNG preview isolation, redacted/non-trace prompt snapshots, bounded last-run retention and late-clear protection, actual usage persistence, and all four configuration mutation routes during blocked screenshot preparation. The latter test reproduced a HTTP 200 settings change during an active capture on 0.2.7, and now expects HTTP 409 with unchanged configuration.

The versioned `UX_REVIEW.md` maps all nine requested UX areas to behavior and evidence. New native platform behavior is not inferred from Linux UI checks. The private user config is hash-checked before/after this work; it is not the safe release template. Config schema remains 3.

## R2 development branch: context and capture ownership (2026-09-18)

This is a development snapshot of `feature/audio-context-029`, not completion of
R2 and not a new stable release tag. The public build profile still has the
existing version label. Identify a generated archive by `BUILD.json.source_commit`
and `releases/RELEASE.json`, not by assuming the old version label denotes the
unchanged v0.2.8 code.

Capture now pins the local context generation before work enters a queue. Audio
queued before Clear cannot be relabelled with the new generation; Clear also
resets unfinished speech and pre-roll on the producer's next callback. Old gaps
cannot overwrite the new conversation or its diagnostics. Screen capture uses
the same boundary: a delayed frame from the old generation is discarded, while
an unchanged frame captured after Clear remains eligible for the new context.
A manual command whose selected context is cleared during preparation is
cancelled before AI dispatch. A subsequent screenshot remains available, and the
legacy no-context screenshot does not acquire an audio device.

Native audio ownership is separate from membership in the source-runtime map.
A capture lease survives Pause until the native producer has actually returned.
Local preview cannot overlap that teardown, a second preview, or a new capture
for the same source. Lease release is idempotent and cannot free a replacement
owner. Preview has a core deadline and an input-duration clamp; cancellation does
not return a partial recording as success. These tests use synthetic capture,
including a deliberately blocked producer, not a physical Windows driver.

Timestamp-error packets do not become ordinary speech. The previous span closes
as incomplete; samples from the packet with an unreliable capture range are
discarded with an explicit diagnostic and context gap. A regular discontinuity
still preserves the following valid packet as a separate span, and silent
packets are not classified as discontinuities. This conservative timestamp-error
policy does lose that packet's samples; the gap makes the loss visible.

Regression coverage lives in `internal/session/audio_generation_test.go`,
`audio_preview_test.go`, `audio_capture_gate_test.go`, `screen_generation_test.go`,
`internal/platform/audio/pcm_test.go` and `internal/control/audio_preview_test.go`.
Screen-generation and delayed-native-teardown tests use virtual time. The mixed
lifecycle soak accepts only the specific cancellation caused by a concurrent
Clear and checks that a fresh manual command still succeeds afterward.

R2 regressions now also cover bounded long-speech overlap with time-scoped
transcript deduplication, synthetic silence/noise/short-duration signal fixtures,
and separate context retention of short negation/number transcript text. These are
not claimed as a speech-recognition corpus. One active plus one pending manual
command uses separately pinned screenshots,
permission-generation fencing for late screen actions, proxy-credential
redaction on realtime fail-closed errors, per-stage context snapshots, independent
screen capture/context/AI cadence, parallel VLM+STT preprocessing with provenance,
and bounded OpenAI Live session/reconnect lifecycle.

The release command now repeats `internal/session` and `internal/platform/audio`
as well as the earlier critical packages. Run `go run ./cmd/release --race` with
a working C toolchain, Node, Playwright Chromium and its system font/library
dependencies. Environment repair is a host-toolchain step; no compiler, font,
browser profile, raw recording or test screenshot belongs in either archive.
Archive verification records tests/vet, the Windows artifact build, real race
detection, DOM/Chromium, CRC, every-entry hashes, clean extraction and a
reproducible EXE from the extracted source. Optional Windows-target vet runs only
with `--windows-verify`; it is not a default release gate for this scope. Consult
the generated report for the checks actually completed on that archive's commit.

The acceptance scope for this branch is automated Go/race/static/JS verification.
Windows-target compilation/vet and physical Windows audio/desktop acceptance are
not release gates for the scope requested here and are not implied by Linux tests. Process-only Windows
audio now has a separate native `ActivateAudioInterfaceAsync` path with the
process-loopback virtual device, Windows build capability gating (minimum 20348),
exact executable-path selection, unique process-tree root resolution, a held
process handle plus creation-time identity fence against PID reuse, bounded
restart/backoff, Studio/API capability and process identity views, and an explicit
release guard that forbids system-endpoint fallback. The Windows acceptance test
harness is opt-in via `AIO_PROCESS_LOOPBACK_EXE` and cross-compiles, but no real
Windows process-loopback/Zoom/Teams audio capture has been run in this Linux
verification, so T23/native quality and OS-driver behavior remain physically
unverified. Windows sleep/lock safety policy is now wired through the existing
overlay message loop: `PBT_APMSUSPEND` and WTS session lock safety-pause sensitive
background sources, expose `pause_reason`, and leave unlock/resume inert until the
user explicitly resumes background work. WTS registration state/error is exposed in
platform diagnostics. This policy is covered by pure/core/app/static tests only.
Per the current acceptance scope, Windows-target verification and a physical
suspend/lock/resume cycle were not run for this slice; native event delivery is
therefore not claimed. Local STT now has a
production keyless provider boundary for an explicitly configured compatible
helper executable and model resource: one long-lived child process, bounded
RAM-only PCM16 IPC, per-request cancellation, helper reuse/restart, missing-resource
status, local transcription preview, sanitized child environment and a release
guard against temp WAV/JPEG/base64/localhost media transport are automated. Local
resource lifetime is explicit rather than hidden: `keep_warm` is the backward-compatible
default, while `unload_on_pause` unloads only after the last active transcription;
resume before completion cancels a pending unload, and provider hot-swap inherits
the current background paused state. The
repository still does not bundle a whisper.cpp helper binary or model weights,
and no actual whisper.cpp inference/model-quality or physical Windows resource
benchmark has been run. OpenAI Live has an explicit persistent WebSocket
adapter and audio-source lifecycle covered by local TLS fixtures: audio append,
Responses delegation, shared session budgets, clean-session replacement on
Clear, fail-closed proxy routing, and opt-in stored WebSocket fork reconnect are
automated. Image input, semantic no_action and remote interrupt remain explicitly
unsupported for this adapter. Stored-session reconnect never replays an
ambiguous last audio/response event and is disabled unless remote storage is
explicitly enabled. Real cloud inference and real VPN packet-trace verification
remain `not run`; no real-provider or hardware-readiness claim follows from local
fixtures, mocks, or a Windows-target compilation.

## Overlay resize repaint, invisible request errors and Studio remote-session blanking (2026-11-09)

User reports: header, answer text and the settings glyph stayed at pre-resize
coordinates; after the clear hotkey the overlay never reappeared; Studio opened
to a white window. Root causes and fixes. The overlay window class was
registered without `CS_HREDRAW|CS_VREDRAW`, so Windows stretched the stale
surface instead of repainting on resize; the class style is now set. The clear
hotkey cancels asynchronously and the previous job may still be winding down,
so the next request can return "a run is already active"; that error was only
written to the meta of a hidden window and looked like a dead hotkey — request
errors are now presented as visible overlay text with an `ERROR` state and the
window is shown. The Studio WebView2 window applies `WDA_EXCLUDEFROMCAPTURE`
for Stealth, and GPU composition under RDP both blank the client area to white
for the owner; in an interactive remote session the desktop window now skips
capture-exclusion affinity and starts WebView2 with software composition, and
a page-readiness probe over the existing `studioDispatch` bridge turns the
remaining silent failure into an explicit native error with recovery steps.
Automated: Windows-target vet and build, Linux suites, `check.sh`. Not run:
physical interactive Windows resize, hotkey-cascade and RDP/local Studio
rendering verification — these need a real Windows session from the user.

## Tray removal and explicit quit hotkey (2026-11-09)

The always-present notification-area icon was removed at the user's request:
`Shell_NotifyIconW`, the callback message handler and the popup menu are gone;
every former menu action keeps a hotkey (Studio, visibility, background pause)
and exit is now the dedicated `quit` action, registered as Win32 hotkey id 9
and defaulting to `Ctrl+Shift+Q`. Existing configurations without the key keep
it unregistered rather than guessing a binding. Automated: hotkey grammar and
duplicate tests, config validation, Windows-target vet/build. Not run: the
physical quit hotkey and taskbar/tray absence verification on a real desktop.

## Studio blank-window automatic software-composition retry (2026-11-09)

The bundled Studio document renders correctly under headless Chromium
(no page errors, themed shell, 19 scripts and 3 stylesheets inlined), so the
reported white WebView2 window is an environment failure, not a bundle one.
The readiness probe now distinguishes them: if the document never answers, the
session is destroyed once per process and restarted with
`WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS=--disable-gpu --disable-gpu-compositing`
before any dialog appears; in remote sessions software composition and the
local-only capture-exclusion skip are applied from the first session. A second
silent session reports itself through the native dialog instead of looping.
Automated: Windows-target vet/build. Not run: physical GPU-driver reproduction.

## Studio diagnostics file (2026-11-09)

Because the blank Studio window is environment-specific and cannot be
reproduced from the development machine, every Studio session now appends a
timestamped trace (WebView2 version, remote-session flag, browser arguments,
navigation success/webErrorStatus from the vendored go-webview2 fork,
window rect, protection status, readiness result and fallback decision) to
`studio-diagnostics.json` next to `config.json`. The file contains no keys,
prompts or request bodies and is written with owner-only permissions.
Automated: Windows-target vet/build, full check suite. Not run: capture of a
real trace on affected hardware (requires the user's machine).
