# Changelog

## 0.1.1 — 2026-11-06

### Added
- `ExecEvent.Seq`; `BackgroundCommand.Connect` follows the real server stream (`stdout` /
  `stderr` with sequence numbers, `gap`, terminal `done`) and `ConnectFrom(ctx, fromSeq)`
  re-attaches after a disconnect.
- `(*DiskLostError).Recovery()` / `.Resumable()` expose the server's 410 `disk_lost` hints.

### Fixed
- `Freeze(ctx, id, true)` keeps polling while the record still reads `running` after a 202 and
  returns a `*ConflictError` for `failed` / `exited` / `unknown` instead of succeeding silently.

### Docs
- 410 `disk_lost` guidance per endpoint; `Connect` after exit may yield `error` + `done` or 404.

## 0.1.0 — 2026-11-04

Initial release.

- Client with API-key / base-URL / timeout / retry options and `SBBOX_API_KEY`,
  `GSK_API_KEY`, `SBBOX_BASE_URL` environment fallbacks.
- Account: regions, limits, usage.
- Templates: build, wait for ready, list, shared, get, delete, grants.
- Sandboxes: create, list, shared, get, kill, freeze (with wait), resume,
  rollback, clone, timeout, reset, metrics, grants.
- Sandbox handle: exec, streaming exec (SSE), run code, background commands,
  files (write / read / list), PTY over SSE and WebSocket.
- Typed errors mapped from every documented status / code.
