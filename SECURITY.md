# Security and privacy boundaries

## Portable configuration

`config.json` contains API keys and optional VPN credentials in plaintext by design. Keep it private. It is ignored by Git; only `examples/config.example.json` is a public template. Schema 2 preserves unknown fields, so a future extension can contain secrets too. Do not rely solely on names recognized by automated preflight. Review exported JSON before sharing it.

The current pre-commit hook checks staged config shapes and rejects runtime config/artifact paths and non-empty credential fields. Build preflight rejects private VPN profiles, credentials, runtime statistics, missing runtime licenses, mismatched binary SHA-256 and Git LFS pointers. It does not claim a universal secret scanner and does not rewrite Git history. The historical 0.2.5 config contains no real API keys or VPN profile; verify history again before publishing private work.

## Local interfaces

Production Studio communicates with Go in-process; no browser fallback, HTTP listener or debugging port is started by normal mode. Headless development mode is explicit, loopback-only, Bearer-authenticated and validates Host, Origin, Content-Type and full request size. The printed development token is private. These controls protect the application boundary, not a compromised same-user/admin process capable of reading memory and files.

## Provider output and cancellation

Raw provider response bodies are never used as public error strings. Structured codes and HTTP status produce normalized errors. Trace snapshots and display text redact configured credentials, including values split across incremental chunks. No redaction system can infer an unknown secret supplied under arbitrary user text; do not put secrets in prompts/labels.

Cancellation owns capture and inference as one operation. A pending screenshot is discarded after cancellation. Cooperative provider workers are joined; an adapter failing to stop within 1500 ms gets a visible cleanup-timeout event. Cancellation of our transport does not prove the remote provider has stopped work or billing. Budgets count application adapter admissions, not a packet-level audit of SDK internal retries and external runtime housekeeping.

## Capture

Capture uses CreateDIBSection → BitBlt → GdiFlush → copied pixels → Go JPEG in RAM. Our capture never creates a screenshot file. OpenCode is a separate runtime and can persist request/session data; abort/delete after attempts is best effort and failures are logged without payloads. Cloud provider retention is outside this application's capture guarantee.

Requested Windows capture protection, successful API readback and universal invisibility are different things. Diagnostics exposes the first two; OBS, WGC, DXGI and particular Windows drivers still need direct acceptance testing. Protection is applied before first show and reasserted when showing/recovering windows.

## Optional AI VPN

`vpn.enabled` defaults false, including when the section is absent. The bundled sing-box engine is launched lazily for an enabled AI request. A random authenticated listener is bound to 127.0.0.1, not LAN. The application never enables TUN, sets the Windows system proxy or alters system routing. Imported inbounds/services/routes are not executed. The standalone outbound's necessary DNS is retained; IPv4 endpoint resolution is enforced. The local authenticated handshake is not a reachability/quality check of the remote tunnel.

Native provider transports use the private proxy explicitly. OpenCode gets it in its isolated HTTP_PROXY/HTTPS_PROXY environment. Failure of our enabled VPN path never silently switches to Direct. However this is not an OS-enforced network sandbox for arbitrary code inside OpenCode. A Windows packet trace and the selected provider/SDK combination are required before making stronger egress claims. Regional access policies of providers are not bypassed by application logic.

Child process Job Objects control process lifetime, not network permissions. Processes remain visible to Windows tools. Signed/runtime provenance, hashes and upstream licenses are shipped; antivirus/firewall acceptance is not inferred from cross-compilation.

## Reporting

Describe the application version, outcome and redacted trace. Never attach live config.json, API keys, VPN secrets, screenshots or a crash dump containing private data unless deliberately reviewed and sanitized. There is no remote issue tracker configured in this local repository.
