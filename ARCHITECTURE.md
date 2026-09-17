# Architecture · 0.2.5

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

The application lifetime is owned by the Go core/overlay, not by a WebView2 message loop. `desktop.Window.Run` is a session manager. With `auto_open=false` it creates no WebView at startup. `Show` starts or foregrounds one isolated WebView2 session; Close/WM_CLOSE cancel that session, wait for its message loop to unwind, call `Destroy`, and delete its temporary profile. A Show request received during teardown is queued and creates a fresh session after the old one is released.

Native WM_CLOSE asks the page to save a valid draft and then calls the in-process `/desktop/close` bridge. A failed save leaves Studio open. Closing Studio never calls the application Quit callback. Capture bypasses desktop coordination entirely when no WebView session exists; a currently visible Studio is still hidden only for the capture frame. `SetStealth` updates both an active session and the value used by future sessions.

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

Schema remains version 1, with optional additive fields. Strict unknown-field rejection is retained from 0.2.2; general forward-preserving migrations are not implemented by this change. Configs using new fields require a version of the application that understands them.
