# Changelog

## 0.1.0 — 2026-11-06

Published to PyPI on 2026-11-06 from the tree described below (the first tag was cut after the
post-review fixes, so this release already contains them).

Included fixes (post-review, 2026-11-05):
- `BackgroundCommand.connect()` follows the real stream: `ExecEvent.seq` from the SSE id, `gap`
  and terminal `done` events, `connect(from_seq=…)` to re-attach; 404 ends the iterator.
- `BackgroundCommand.wait()` / `connect()` and `Pty.connect()` run without the 60 s default
  deadline (`NO_DEADLINE`); `exec_stream` keeps `timeout + 30 s`.
- `freeze(wait=True)` keeps polling on `running`, raises `ConflictError` on `failed`/`exited`.
- `Files.read` uses the idempotent-GET retry policy.
- `DiskLostError.recovery` / `.resumable` expose the 410 body so callers can branch on it.
- Package and examples pass `mypy --strict`; tests are isolated from `SBBOX_*` env vars.


First release: sync and async clients covering sandboxes (create / list / get / kill / freeze / resume /
rollback / clone / reset / timeout / metrics / sharing), commands (exec, streaming exec, background
commands, run-code), files, PTY, templates (build, wait, list, sharing) and account calls (regions,
limits, usage). Typed errors for every documented status code.
