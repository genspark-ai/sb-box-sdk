# Changelog

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
