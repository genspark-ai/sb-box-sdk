# Changelog

## 0.2.0 — 2026-11-06

### Breaking
- `ExecEvent` is now `#[non_exhaustive]` and gained `Gap { message }` and `Done`. Exhaustive
  `match` statements must add a `_ => {}` arm; background-command streams now end with `Done`
  (not `Exit`), so loops that waited for `Exit` on `connect()` must handle `Done`.
- `Error::DiskLost` gained a `recovery: Vec<String>` field. Patterns written as
  `Error::DiskLost { body }` must become `Error::DiskLost { body, recovery }` or
  `Error::DiskLost { body, .. }`.

### Added
- `BackgroundCommand::connect_from(from_seq)` and `connect_with_seq(from_seq) -> SeqEventStream`
  (yields `(Option<u64>, ExecEvent)`) for re-attaching after a disconnect.
- `UsageTotals` parses the camelCase `runSec` used by `GET /usage`.

### Fixed
- Offline tests no longer depend on `SBBOX_*` / `GSK_API_KEY` in the developer's shell.

### Docs
- 410 `disk_lost` guidance per endpoint; `connect()` after exit may yield `error` + `done` or 404.

## 0.1.0 — 2026-11-04

Initial release.

- Async `Client` (reqwest + rustls) with `templates()` and `sandboxes()` services and
  `regions / limits / usage`.
- `Sandbox` handle: `exec`, `exec_stream` (SSE), `run_code`, background `commands`, `files`
  (raw bytes), `pty`, lifecycle (`freeze` with wait, `resume`, `rollback`, `clone_sandbox`,
  `set_timeout`, `reset`, `metrics`, `kill`), `port_url`.
- Typed `Error` enum covering every documented status / code, with key redaction and
  automatic retries for idempotent calls.
- Optional features: `blocking` (sync wrapper), `ws` (WebSocket PTY).
