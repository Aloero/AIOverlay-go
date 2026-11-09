# Security and privacy boundaries

## Portable configuration

`config.json` contains API keys and optional VPN credentials in plaintext by design. Keep it private. It is ignored by Git; only `examples/config.example.json` is a public template. The current schema (5) preserves unknown fields, so a future extension can contain secrets too. Do not rely solely on names recognized by automated preflight. Review exported JSON before sharing it.

The current pre-commit hook checks staged config shapes and rejects runtime config/artifact paths and non-empty credential fields. Build preflight rejects private VPN profiles, credentials, runtime statistics, missing runtime licenses, mismatched binary SHA-256 and Git LFS pointers. It does not claim a universal secret scanner and does not rewrite Git history. The historical 0.2.5 config contains no real API keys or VPN profile; verify history again before publishing private work.

Configuration writes check the final formatted JSON against the same 8 MiB limit used when reading it; a rejected write leaves the previous file intact. The HTTP configuration export accepts GET and returns `Cache-Control: no-store`. Source archive inventory explicitly excludes `.env`, `.env.*` and `*.local.json/yaml/yml`, including when the source directory has no Git metadata.

## Local interfaces

Production Studio communicates with Go in-process; no browser fallback, HTTP listener or debugging port is started by normal mode. Headless development mode is explicit, loopback-only, Bearer-authenticated and validates Host, Origin, Content-Type and full request size. The printed development token is private. These controls protect the application boundary, not a compromised same-user/admin process capable of reading memory and files.

## Provider output and cancellation

Raw provider response bodies are never used as public error strings. Structured codes and HTTP status produce normalized errors. Trace snapshots and display text redact configured credentials, including values split across incremental chunks. No redaction system can infer an unknown secret supplied under arbitrary user text; do not put secrets in prompts/labels.

Cancellation owns capture and inference as one operation. A pending screenshot is discarded after cancellation. Cooperative provider workers are joined; an adapter failing to stop within 1500 ms gets a visible cleanup-timeout event. Cancellation of our transport does not prove the remote provider has stopped work or billing. Budgets count application adapter admissions, not a packet-level audit of SDK internal retries and external runtime housekeeping.

Provider HTTP clients follow redirects only within the original scheme, hostname and effective port. Cross-origin redirects are stopped before credentials or request media can be sent to the next server. The policy applies to OpenAI-compatible, Gemini, transcription and local OpenCode clients.

Child process shutdown releases the process group or Windows Job Object before waiting for inherited output pipes. Natural parent exit has a bounded pipe-drain wait, preventing a surviving descendant from holding shutdown indefinitely.

## Capture

Capture uses CreateDIBSection → BitBlt → GdiFlush → copied pixels → Go JPEG in RAM. Our capture never creates a screenshot file. OpenCode is a separate runtime and can persist request/session data; abort/delete after attempts is best effort and failures are logged without payloads. Cloud provider retention is outside this application's capture guarantee.

Requested Windows capture protection, successful API readback and universal invisibility are different things. Diagnostics exposes the first two; OBS, WGC, DXGI and particular Windows drivers still need direct acceptance testing. Protection is applied before first show and reasserted when showing/recovering windows.

## Optional AI VPN

`vpn.enabled` defaults false, including when the section is absent. The bundled sing-box engine is launched lazily for an enabled AI request. A random authenticated listener is bound to 127.0.0.1, not LAN. The application never enables TUN, sets the Windows system proxy or alters system routing. Imported inbounds/services/routes are not executed. The standalone outbound's necessary DNS is retained; IPv4 endpoint resolution is enforced. The local authenticated handshake is not a reachability/quality check of the remote tunnel.

Native provider transports use the private proxy explicitly. OpenCode gets it in its isolated HTTP_PROXY/HTTPS_PROXY environment. Failure of our enabled VPN path never silently switches to Direct. However this is not an OS-enforced network sandbox for arbitrary code inside OpenCode. A Windows packet trace and the selected provider/SDK combination are required before making stronger egress claims. Regional access policies of providers are not bypassed by application logic.

Child process Job Objects control process lifetime, not network permissions. Processes remain visible to Windows tools. Signed/runtime provenance, hashes and upstream licenses are shipped; antivirus/firewall acceptance is not inferred from cross-compilation.

## Reporting

Describe the application version, outcome and redacted trace. Never attach live config.json, API keys, VPN secrets, screenshots or a crash dump containing private data unless deliberately reviewed and sanitized. There is no remote issue tracker configured in this local repository.


## Explicit Studio tests and share links (0.2.7)

Saving a provider or importing a VPN share link does not send a model request or connect to its VPN server. The separate provider test requires confirmation, names a saved enabled key/model, requests no image and has one adapter-call admission. The VPN network test requires confirmation and uses HTTPS HEAD with no model credentials, no redirects and no direct fallback. Test automation stubs those two UI commands and separately uses mock Go providers; it does not exercise real accounts.

The virtual simulator never obtains a Provider registry and cannot invoke a transport. Its graph/config validation, virtual wall budget, 256-call/10000-step cap and request context limit apply independently of normal execution. A simulation result is not added to production run history or key/model health.

URI import supports an explicit subset only. Unknown security/transport parameters are rejected rather than discarded. Insecure TLS flags produce a visible warning, not an unannounced change. URI text is not included in error messages. VPN secrets remain in the user's portable JSON and are hidden in the collapsed editor; none are part of the public example or release profile.


## Explicit prompt inspection (0.2.8)

`/api/prompts/last` shares the existing authenticated HTTP/trusted native boundary. It is not polled or included in normal job/trace responses. Only an explicit preview action fetches last-run bounded, redacted template text. The volatile Go store is limited to 16 × 32 KiB payload, excludes image bytes, and is cleared by a new run or Clear. This is sensitive task text even after credentials are redacted; do not attach it to diagnostic reports automatically. No new persistent screenshot/prompt file is introduced.

## Dependency audit · 2026-11-09

The minimum application toolchain is Go 1.27.3. Source scans with govulncheck 1.9.0 reported no known vulnerabilities for Linux/amd64 and Windows/amd64 on this audit date; npm audit also reported zero findings.

The separately executed, unmodified sing-box runtime was updated from official 1.14.1 to official 1.14.4, with the archive digest and extracted payload hashes verified. This removes the old runtime's standard-library findings. Its binary scan still reports 26 advisory IDs across 10 upstream dependencies. Presence of vulnerable symbols is established; reachability through AIOverlay's restricted configuration has not been established. OpenCode/Bun and native libcronet are outside govulncheck's coverage. See [the audit report](docs/audits/2026-11-09.md) and [the machine-readable runtime findings](docs/audits/2026-11-09-runtime.json).
