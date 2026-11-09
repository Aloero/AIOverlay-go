# Architecture · 0.2.8

Production: native Win32 overlay + embedded WebView2 Studio + Go execution engine.
Studio HTML/CSS/JS is compiled into the executable; its JSON requests call the existing handlers in-process via `internal/desktop/bridge.go`. A development HTTP listener is used only with `--headless`.

- `domain`: serializable pipeline, provider, credential, budget and trace values.
- `config`: validation and portable JSON load/save/import/export.
- `engine`: graph routing, credential/model selection, admission budgets, cancellation and stream ownership.
- `providers`: one endpoint attempt; adapter-specific payloads and normalized errors.
- `control`: UI commands, asynchronous JobManager, capture preview and trace endpoints.
- `desktop`: Win32 WebView2 host, native export dialog, in-process JSON bridge.
- `platform`: Win32 overlay/hotkeys and capture resource lifetime. Capture algorithms are injected for Linux tests; actual calls live in `capture_windows.go`.
- `catalog`: bundled Models.dev metadata. Metadata is not proof of live availability.
- `web`: embedded UI assets. All displayed model/config/error values are escaped; no CDN assets.

`CreateDIBSection` replaces GetDIBits; capture remains on a locked OS thread until the DIB's pixels have been copied and the handles released. JPEG encoding can then run independently.

Only one provisional model stream owns the display. A different successful winner explicitly replaces it. Vision transcription in a composite is silent. Traces and callbacks are synchronized; the runner cancels and joins its attempt workers before returning its immutable final trace.

The shared execution budget counts actual provider requests, including retries and credential changes. Error messages are redacted at both trace and final-return boundaries.

External runtimes: OpenCode is a separate process with its own storage/network behavior; WebView2 also creates subprocesses. Native branding is limited to our PE resources. Do not equate a successful cross-build or mock with a completed Windows desktop integration test.

## Lazy Studio lifecycle · 0.2.5

The application lifetime is owned by the Go core/overlay, not by a WebView2 message loop. `desktop.Window.Run` is a session manager with explicit per-session STA COM initialization. With `auto_open=false` it creates no WebView at startup. `Show` starts or foregrounds one isolated WebView2 session; Close/WM_CLOSE protect pending drafts, cancel the session, and synchronously close the controller/COM references and destroy HWND on the owning STA thread while its message loop still runs. The loop then unwinds and the isolated profile is removed with bounded retries. Upstream asynchronous Destroy is not used. A Show request received during teardown is queued and creates a fresh session after the old one is released.

Native WM_CLOSE asks the page to save a valid draft and then calls the in-process `/desktop/close` bridge. A failed save leaves Studio open. Closing Studio never calls the application Quit callback. Capture bypasses desktop coordination entirely when no WebView session exists; a currently visible Studio is still hidden only for the capture frame. Studio never applies window display affinity: it is a config editor, `stealth` belongs to the overlay, and affinity over WebView2 blanks presentation on some drivers, so a visible Studio window pauses background screen capture instead.

This reduces steady-state memory when Studio is not in use. It does not imply a one-process application: WebView2 is multiprocess while open, and Embedded OpenCode has its own independent runtime lifecycle.

## AI-only tunnel · 0.2.5

`domain.Config.VPN` belongs to the portable runtime config and defaults to disabled when omitted. `internal/tunnel` owns the optional bundled sing-box process. The manager is lazy: `enabled=false` starts no process; `enabled=true` starts sing-box only when a provider first asks for a proxy. Startup errors fail closed.

The user supplies one sing-box outbound (or a full config containing exactly one usable outbound). `tunnel.BuildConfig` discards pasted inbounds, TUN, routing and system-proxy settings and creates its own authenticated `mixed` inbound bound only to `127.0.0.1` on a random port. `set_system_proxy` is forced false. Native provider HTTP transports receive that proxy explicitly. Embedded OpenCode receives the same URL through `HTTP_PROXY`/`HTTPS_PROXY`, while `NO_PROXY` keeps its own loopback control API local. When VPN is disabled, OpenCode retains the existing IPv4 loopback CONNECT path.

The sing-box generated config is written mode 0600 to an isolated temporary directory because the external runtime requires a config file; it is removed when the process stops. AIOverlay never enables sing-box TUN and never changes Windows proxy settings.

`build` remains in the portable JSON as pre-build input for compatibility, but runtime Studio does not edit it. `cmd/buildmeta` generates PE resources and a compiled build identity before `go build`; the running EXE uses that generated identity rather than mutable runtime JSON.

## Native overlay chrome

The frameless Win32 overlay owns its chrome instead of relying on a framework title bar. The top 36 px form a visual drag strip in Edit Mode. `WM_NCHITTEST` resolves resize edges first, then the settings hit zone, then the remaining header as `HTCAPTION`. The settings button is painted and handled inside the same native window and invokes the in-process Studio callback; it does not spawn another UI process.

## Studio/catalog scheduling · 0.2.5

`ModelPoolConfig.Selection` is an optional, additive source selector. Old configs with no selection retain manual pools. A catalog-only pool is valid without pinned slots. The engine owns ordering and fallback; providers still execute one adapter attempt.

`catalog/routes.go` separates Models.dev identity from configured transport. `ProviderID` is a configuration mapping, not a scheduler branch. Directory metadata supplies provider name, model count and credential environment. `engine/selection.go` builds a candidate plan from configured routes, modality, cost filter, explicit quality and overrides. The same pure planner is used by the Studio draft preview and execution. Preview does not consume keys, rotate cursors or send model requests.

Admission records distinct providers together with actual adapter calls and credentials. Typed budget scopes distinguish whole-pool exhaustion from a single provider/key constraint. An already admitted race winner may still complete when a competing candidate reaches the call budget. Candidate/hedge events are distinct from admitted attempt_start events. Trace references carry an attempt ID, transport and credential alias.

`providers/runtime_pool.go` leases OpenCode instances by credential. Concurrent attempts with another key cannot stop an in-use process; only one idle process per provider is retained. Process configuration passes only the selected provider's key, with isolated HOME/XDG directories, denied tools and the existing IPv4 CONNECT proxy. OpenCode error envelopes are parsed structurally; raw response bodies are not shown. Session abort/delete is best-effort cleanup, not an assurance of zero third-party disk persistence.

`app/settings.go` serializes provider-only changes and pipeline activation against execution. Activating an existing saved pipeline writes the selected ID without rebuilding the provider registry or resetting scheduler state. The application resolves an empty run ID against the current active pipeline.

Frontend modules: app.js retains common UI and catalog/trace rendering; connections.js owns provider setup; pool-editor.js owns source and dispatch controls; pipeline-view.js owns graph-page/edge controls; studio-interactions.js owns viewport, activation, context help and live-attempt presentation. All are embedded locally in DesktopHTML. Canvas layout is part of the portable config; provider-dialog drafts do not leak into an unsaved graph.

A hidden or iconic Studio is not hidden/restored during capture. Only a visible non-iconic window is temporarily hidden. The GDI capture and native overlay privacy path remain unchanged.

Historical 0.2.3–0.2.5 used schema 1. This was superseded by forward-preserving schema 2 in 0.2.6 and independent scheduling schema 3 in 0.2.7; 0.2.8 retains schema 3 and both migration paths.


## Audit stabilization · 0.2.6

- `third_party/go-webview2`: narrow maintained MIT fork of the previous pin. The host owns COM initialization; the fork provides synchronous destruction, reference release, initialization errors instead of process exits, initially hidden windows and guarded dispatch. Its native test is opt-in; Linux tests cannot establish native memory reclamation.
- `childprocess`: one `cmd.Wait` per instance; Done/Alive are synchronized. Windows startup is suspended until kill-on-close Job Object membership exists. Process exit releases descendants and OS handles once; Stop is idempotent.
- `atomicfile`: temp file in the destination directory, flush, and replace without delete-first. Windows uses ReplaceFileW for existing files and MoveFileEx for new files. Sharing violations retain the last-good file. Directory durability and physical power-loss behavior are platform/filesystem acceptance concerns.
- `hotkey`: one grammar shared by portable validation and RegisterHotKey. Transactional registration restores previous bindings on failure; editor suspension has a short lease.
- `domain`: schema 2 extension codecs retain unknown RawMessage fields, including nested structures; it remains independent of HTTP, Win32, provider requests and UI. `config` migrates schema 1, rejects duplicate JSON keys/trailing bytes/excess depth and validates graph reachability, enum semantics and bounded budgets before writing.
- `control.JobManager`: owns capture preparation, inference and benchmark batches. A cancelled capture cannot start inference later. Clear discards stale output. Both legacy run and benchmark routes use the same single-flight gate. Long-lived work belongs to application/job context, not a transient WebView session.
- `control`: separate trusted DesktopHandler and authenticated loopback HTTP Handler. Byte limit is enforced by max+1 read, not EOF synthesized by LimitReader. Prompt preview calls the same engine renderer.
- `engine`: original input is independent from previous and vision outputs. The screenshot remains in execution context for fallback; known text-only requests drop the image payload. Key admission filtering precedes key-attempt truncation. Exhausted edges are skipped. Effective timeout precedence is centralized. Cooperative provider channel closure is joined after cancellation, with a traced 1500 ms bound for broken adapters.
- `engine/stream`: stage ownership resets on first output; winners never concatenate competing/stale stage content. Redaction preserves a possible secret prefix across chunk boundaries; cached per-run secrets avoid repeated JSON parsing. The final answer and metadata pass through the same redactor.
- `trace`: List/Get/Add and Job snapshots copy nested event/attempt/winner data. Returning a trace never exports a mutable stored slice.
- `app`: writes are serialized against runs; UI-only saves do not recreate connections. Network identity comparison ignores labels/policy/default changes but notices credential/endpoint changes. The same Engine survives validated reconfiguration. Optional statistics persist only when enabled and never replace quality scores. Catalogue refresh is bounded/contextual, atomically saved before publication and joined at shutdown.
- `tunnel`: the application owns the authenticated mixed loopback listener. A SOCKS username/password handshake, not merely an open TCP port, confirms local startup. Imported standalone DNS dependencies are retained; endpoint resolution is IPv4-only. Multi-outbound detours are rejected. No TUN, system proxy or arbitrary route injection. This remains userspace routing, not an OS per-process egress firewall.
- `web/static/studio-lifecycle.js`: dialog drafts and graph revisions are separate. Close coalesces requests, asks save/discard/cancel for a connection draft, and refuses to close over changes made during PUT. Normalization converts optional/null arrays to editable collections without dropping extension fields.
- `benchmark`: batch progress and exact/unverified fixture grading are explicit. Reports separate requested and completed rows; even sample counts use the average of the middle latencies.
- `releaseguard` / `releasepack` / `cmd/release`: no runtime config in Git, no credentials or LFS pointers in packaged inputs, explicit inventory, deterministic ZIP timestamps, full manifest coverage/CRC/clean extraction and reproducible Windows EXE from extracted source. No CDN, private configuration upload, runtime Python or test-browser payload is added.

The original layer boundaries remain: adapters execute one request, the engine chooses fallback, app/control own operations and platform/desktop own Windows. Details and evidence boundaries for every audit issue are in AUDIT_FIXES.md and VERIFICATION.md.


## Studio UX and independent scheduling · 0.2.7

Schema 3 adds ModelPoolConfig.Order and Dispatch, independently validated and resolved by domain helpers. The engine dispatch switch and the policy order selector consume these separately; the old Policy field remains a compatibility input when explicit fields are absent. Migration materializes old behavior recursively, including vision preprocessors. No provider adapter chooses fallback.

Pure preview ordering works on a copied statistics state. `engine/simulation.go` runs a bounded virtual clock over the shared candidate planner, order/limits/admission and graph-edge rules, with explicit hypothetical outcomes and no provider registry or external effects. App handlers inject snapshots of current stats/credential cooldowns, not mutable references. Simulation is deliberately not a prediction of network/SDK retries, billing or provider quality.

Connection probes are confirmed single-model/key jobs with their own trace purpose and 32-token request ceiling. They use saved transports without replacing the active pipeline, and the native overlay ignores that job purpose. VPN URI parsing rejects unknown/incompatible settings; explicit HTTPS HEAD probes require a separate confirmation and never silently fall back to direct transport. Profile names do not participate in runtime identity comparisons.

ExecutionTrace now snapshots pipeline name, configuration revision, purpose and effective global budget; live JobManager receives that snapshot before events. Public snapshots are deep-copied and unrelated unknown budget fields are not included in execution traces. Credential status exposes IDs/outcomes/times only, not secrets.

Frontend responsibilities are split: studio-workspace owns applied/draft state, bounded in-memory undo and panel/search controls; schedule-view, pool-plan and simulation-view own dispatch editing/planning/rehearsal; connection-hub and catalog-view own discovery/routes/shortcuts/consented probes; model-settings handles inherited limits; vpn-view handles compact profile editing/import/verification; run-insights presents summaries and timing without replacing the raw trace. Original capture, native lifecycle, authorization and draft-close safeguards remain in their existing layers.


## Studio contract corrections · 0.2.8

`control.JobManager` uses a separate admission/settings mutex from its progress mutex. Full config, provider, import and activation commands cannot race a preparing screenshot. Cancellation and snapshots do not wait on configuration I/O.

`engine/input_context.go` computes bounded graph input states for previews. Execution and virtual simulation preserve the same previous-output rule for catalog modality. Mixed success/error paths produce an explicit conditional preview. Each engine owns a private locked random source; preview/simulation instances cannot consume a live random choice.

Terminal trace fields describe the graph, not its last intermediate success. Structured plan IDs account for candidates/actual attempted slots/untried slots per visit. Edge IDs and typed cancellation/budget reasons support UI navigation without parsing provider error strings. Worker completion precedes summary generation.

`engine.PromptSnapshot` and `control/prompt_history.go` form an opt-in inspection boundary: bounded/redacted text values, no image bytes and no automatic trace/job exposure. `prompt-preview.js` loads these only on explicit user action. `app/recent_models.go` records actual model references and persists them via the existing config save/exit lifecycle. User-edit history remains WebView-session-only.

## SOLID refactor · transport contracts and state ownership

`internal/contracts` owns transport-neutral application requests and reports.
`app` no longer imports `control`. The old exported `control.CatalogQuery`,
`CatalogPage`, `TestRequest`, benchmark types and `StudioState` remain Go type
aliases; the existing constructor/backend API and JSON tags remain compatible.
The benchmark snapshot copier lives with its report, with the old control helper
retained as a forwarding function. An application-layer import test guards this
boundary.

Control session handlers consume separate capability interfaces rather than one
16-method interface. Command execution, command preview, pause, status, context
clear, source control, realtime responses and audio operations can be implemented
independently. Missing capabilities still use the existing unsupported responses;
the legacy screenshot path remains available. Tests exercise minimal concrete
backends without embedding an unimplemented all-purpose interface.

One model attempt has separate responsibilities: `attempt.go` orchestrates an
admitted call, `attempt_request.go` constructs its request and prompt snapshot,
`attempt_stream.go` owns stream consumption and timers, `attempt_result.go`
records the terminal result, and `attempt_resources.go` owns semaphore/session
leases and cancellation cleanup. Capacity transfers to a teardown waiter when a
provider fails to close its channels within the existing cleanup limit. A
cancelled admission releases its semaphore even if cancellation arrives just
after acquisition; callback panics retain the previous deferred-cleanup behavior.
Graph routing and retry policy remain outside the provider stream consumer.

`session.Manager` delegates pending transcript membership/wakeups to
`pendingTranscripts` and active-turn registration/cancellation to
`activeTurnRegistry`. Each owns its lock and maps. Generation snapshots are copied
on registration, and cancellation callbacks run outside the registry lock.
Single-context and per-stage transcript waits now share one implementation with
the same cutoff, generation and timeout semantics.

Source validation is separated into context/reference, command, event-source,
realtime, screen and budget checks while preserving validation order and error
text. VPN share-link conversion separates URI parsing from protocol, TLS and
transport construction. These are local refactorings: they add no provider
protocol, runtime dependency, configuration schema, network probe or UI framework.
