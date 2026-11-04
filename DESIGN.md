# SB-Box SDKs — one design, three languages

Packages (all `0.1.0`, MIT, publishable on their own):

| Language | Package | Import | Runtime |
|---|---|---|---|
| Python | `genspark-sbbox` (PyPI) | `import sbbox` | 3.9+, `httpx` only dependency; sync `Client` + async `AsyncClient` |
| TypeScript | `@genspark/sb-box` (npm) | `import { SbBox } from '@genspark/sb-box'` | Node 18+ (global `fetch`, `ReadableStream`); ESM + CJS builds, `.d.ts` |
| Go | `github.com/genspark-ai/sb-box-sdk/go` | `import sbbox "github.com/genspark-ai/sb-box-sdk/go"` | Go 1.21+, stdlib only |
| Rust | `genspark-sbbox` (crates.io, lib `sbbox`) | `use sbbox::Client;` | tokio + reqwest (rustls); features `blocking`, `ws` |

Layout in this repo: `{python,typescript,go,rust}/` at the root; each is self-contained (its own README,
LICENSE, tests, examples, CHANGELOG). `API.md` beside them is the contract; the SDKs never read the
backend code.

## Surface (identical shape; idiomatic casing per language)

```
client = Client(api_key=None, base_url=None, timeout=60, retries=3, user_agent_suffix=None)
client.regions()                       -> Regions
client.limits()                        -> Limits
client.usage(days=30)                  -> Usage
client.templates.build(...)            -> TemplateBuild (template_id, status, region)
client.templates.wait_ready(id, timeout=600, interval=5) -> Template
client.templates.list() / shared() / get(id) / delete(id)
client.templates.grant(id, user_id, perm="read", expires_at=None, reason=None) / grants(id) / revoke(id, user_id)
client.sandboxes.create(template="base"|None, template_id=None, timeout=None, on_timeout="freeze",
                        metadata=None, env_vars=None, network=None) -> Sandbox
client.sandboxes.list(state=None) -> [SandboxSummary]      # state: str | list[str]
client.sandboxes.shared() -> [Sandbox]
client.sandboxes.get(id) -> Sandbox                          # also client.sandboxes.connect(id) alias
client.sandboxes.kill(id) / freeze(id, wait=True) / resume(id, timeout=None) / rollback(id, version=0, timeout=None)
client.sandboxes.clone(id, version=0, metadata=None) -> Sandbox
client.sandboxes.set_timeout(id, seconds) / reset(id) / metrics(id, start=None, end=None)
client.sandboxes.grant / grants / revoke (as templates)

Sandbox (handle bound to the client; refreshable):
  .id .state .template_id .region .port_url(port) .metadata .versions .created_at .frozen_at .timeout_at
  .refresh() .kill() .freeze(wait=True) .resume(timeout=None) .rollback(version=0) .clone(version=0, metadata=None)
  .set_timeout(seconds) .reset() .metrics(start=None, end=None)
  .exec(command: list[str] | str, cwd=None, env=None, user=None, timeout=60) -> CommandResult(stdout, stderr, exit_code)
      # a str command runs as ["/bin/sh", "-c", str]
  .exec_stream(...) -> iterator of ExecEvent(kind: "stdout"|"stderr"|"exit"|"error", data, exit_code, exec_id)
  .run_code(code, language="python", context_id=None, env=None, timeout=60, images="base64") -> CodeResult
  .commands.start(command, cwd=None, env=None, user=None) -> BackgroundCommand(exec_id, pid)
      BackgroundCommand: .input(data) .close() .wait() -> int .kill(signal=9) .connect() -> ExecEvent iterator
  .commands.list() / .kill(pid, signal=9) / .send_stdin(pid, data)
  .files.write(path, data: bytes | str, user=None) -> int
  .files.read(path, user=None) -> bytes ; .files.read_text(path, encoding="utf-8", user=None)
  .files.list(path="/", user=None) -> [FileEntry(name, path, is_dir, size)]
  .pty.open(command=None, rows=24, cols=80, cwd=None, env=None) -> Pty(exec_id, pid)
      Pty: .input(bytes|str) .resize(rows, cols) .kill() .connect() -> iterator of PtyEvent(data | exit_code)
  (WebSocket PTY: TypeScript, Go and Rust (`ws` feature) ship `pty.connectWs()`; Python documents the frame protocol and
   leaves it to the caller — no extra dependency in 0.1.)
```

Errors: see `API.md` table — one base class `SbBoxError` with `status`, `code`, `message`, `detail`
(raw parsed body), `retry_after`, `request_id` (the `x-request-id` header when present).

Behaviour rules (all three):
- Key resolution: argument → `SBBOX_API_KEY` → `GSK_API_KEY`; missing key raises at client construction
  with a message that names both variables. Base URL: argument → `SBBOX_BASE_URL` → production.
- `User-Agent: sbbox-python-sdk/0.1.0 (+https://www.genspark.ai/sb-box/docs)` etc.
- Timeouts: per-request default 60 s; `exec`/`run_code` requests use `timeout + 30 s`; `create`/`clone`/
  `resume` use 120 s; `wait` calls use their own deadline.
- Retries exactly as in `API.md`; never retry non-idempotent calls.
- Redact `gsk-[A-Za-z0-9._-]+` in every error message and log line.
- No telemetry, no global state, no implicit `print`.

Tests (offline, required to pass in CI): a tiny in-process mock server per language that implements the
contract from `API.md` for the happy path and the documented error bodies, covering: auth header,
create/get/list/kill, freeze 200 + 202-then-poll, resume, rollback, clone 503 Retry-After, exec 200
(non-zero exit) + 408, sse/exec event parsing, run-code (results + error), files write/read/list
(bytes round-trip), commands start/input/wait, templates build + wait_ready (building → ready; →
error raises), limits/usage/regions, every error class mapping, redaction of the key in errors.

Smoke (manual, against a real environment): `examples/smoke.*` reads `SBBOX_API_KEY` / `SBBOX_BASE_URL`,
creates from `base`, runs `echo`, writes/reads a file, `run_code("1+1")`, freezes (wait), resumes, kills.

README per package: install, 10-line quickstart (create → exec → files → freeze/resume → kill), the
error classes, configuration (env vars), link to `/sb-box/docs`. No internal details (clusters,
providers, engines, pricing numbers).
