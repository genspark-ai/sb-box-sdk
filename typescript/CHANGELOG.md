# Changelog

## 0.1.0 — 2026-11-04

Initial release.

- `SbBox` client: regions, limits, usage, templates (build / waitReady / list / shared / get /
  delete / grants) and sandboxes (create / list / shared / get / kill / freeze / resume / rollback /
  clone / setTimeout / reset / metrics / grants).
- `Sandbox` handle: `exec`, `execStream` (SSE), `runCode`, background `commands`, `files`
  (bytes/text), `pty` (SSE and WebSocket), `portUrl`.
- Typed error classes mapped from HTTP status and error codes; API keys redacted from messages.
- Built-in retries for reads and idempotent lifecycle calls; `Retry-After` honoured on clone / reset.
- ESM + CommonJS builds, bundled type declarations, zero runtime dependencies.
