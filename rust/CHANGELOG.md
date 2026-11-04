# Changelog

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
