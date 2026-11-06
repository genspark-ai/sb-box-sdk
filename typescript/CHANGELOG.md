# Changelog

## 0.1.2 — 2026-11-06

### Added
- `BackgroundCommand.connect()` follows the real server stream: `stdout` / `stderr` events carry
  `seq`, `gap` events report lost output, and the stream ends with `done` (not `exit`).
- `connect({ fromSeq })` re-attaches after a disconnect from a given sequence number
  (`?from_seq=` / `Last-Event-ID`).
- `DiskLostError.recovery` / `.resumable` expose the server's 410 `disk_lost` recovery hints.

### Fixed
- `User-Agent` now reports the real package version (0.1.1 still sent `0.1.0`).
- Offline tests no longer depend on `SBBOX_*` / `GSK_API_KEY` in the developer's shell.

### Docs
- 410 `disk_lost` guidance per endpoint; `connect()` after exit may yield `error` + `done` or 404.

## 0.1.1

- No code changes. First release published through the repository's GitHub Actions trusted publisher (npm provenance).

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
