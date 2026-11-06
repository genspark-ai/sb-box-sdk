# genspark-sbbox

Rust SDK for [SB-Box](https://www.genspark.ai/sb-box) — cloud sandboxes for agents and code
execution. A sandbox starts in about a second, keeps its memory when frozen, and exposes
commands, files, a stateful code interpreter and terminals over one API key.

```toml
[dependencies]
genspark-sbbox = "0.2"
tokio = { version = "1", features = ["macros", "rt-multi-thread"] }
```

## Quickstart

```rust
use sbbox::{Client, CreateSandboxRequest, ExecOptions};

#[tokio::main]
async fn main() -> sbbox::Result<()> {
    let client = Client::from_env()?;                       // SBBOX_API_KEY=gsk-...

    let mut sb = client.sandboxes()
        .create(&CreateSandboxRequest::base().timeout(600)) // public base image, 10 min
        .await?;

    let out = sb.exec("python3 -c 'print(6*7)'", &ExecOptions::default()).await?;
    println!("{}", out.stdout);                             // 42

    sb.files().write_text("/home/user/notes.md", "# hi\n", None).await?;
    println!("{}", sb.files().read_text("/home/user/notes.md", None).await?);

    sb.freeze(true).await?;                                 // memory + disk kept, no compute billed
    sb.resume(None).await?;                                 // back in about a second
    sb.kill().await?;
    Ok(())
}
```

Get a key from the [SB-Box console](https://www.genspark.ai/sb-box); the full API is described in
the [user guide](https://www.genspark.ai/sb-box/docs).

## What you can do

| Area | Calls |
|---|---|
| Account | `client.regions()`, `client.limits()`, `client.usage(days)` |
| Templates | `client.templates().build(..)`, `.wait_ready(id, ..)`, `.list()`, `.shared()`, `.get(id)`, `.delete(id)`, `.grant / .grants / .revoke` |
| Sandboxes | `client.sandboxes().create(..)`, `.list(&states)`, `.shared()`, `.get(id)`, `.kill(id)`, `.freeze(id, wait)`, `.resume(id, timeout)`, `.rollback(id, version, ..)`, `.clone_sandbox(id, version, metadata)`, `.set_timeout(id, secs)`, `.reset(id)`, `.metrics(id, start, end)` |
| Commands | `sb.exec(cmd, &opts)`, `sb.exec_stream(cmd, &opts)` (a `Stream` of `ExecEvent`), `sb.commands().start(..)` → `input / close / wait / kill / connect` |
| Code | `sb.run_code(code, &opts)` — stateful interpreter with `context_id`, rich `results` (text / html / png / …) |
| Files | `sb.files().write / write_text / read / read_text / read_stream / list` |
| PTY | `sb.pty().open(..)` → `input / resize / kill / connect`; `sb.pty().connect_ws(..)` with the `ws` feature |

`sb.exec` accepts a `&str` (run through `/bin/sh -c`) or an argv slice (`&["ls", "-la"]`).
A non-zero exit code is returned in `CommandResult::exit_code`, not raised; only a timeout
raises `Error::CommandTimeout`. All lifetimes and timeouts are **seconds**.

`sb.port_url(3000)` gives the public URL of a port the sandbox is listening on.

## Errors

Everything returns `sbbox::Result<T>`; `sbbox::Error` has one variant per API condition:

| Variant | When |
|---|---|
| `Authentication` | 401 — missing / invalid key |
| `InsufficientCredits { balance }` | 402 — add credits |
| `PlanRequired { plan, upgrade }` | 403 — the feature needs a higher plan |
| `Forbidden` | 403 — permission denied |
| `NotFound`, `RegionRetired { region }` | 404 — unknown or killed sandbox / template |
| `CommandTimeout { killed }` | 408 — `exec` exceeded its timeout |
| `Limit { info }` (`LimitInfo { code, plan, limit, current, requested }`) | 409 — concurrency / spec / build limit |
| `InterpreterUnavailable`, `InterpreterStartFailed` | `run_code` on a template without the interpreter |
| `Conflict` | 409 — operation conflicts with the sandbox's state |
| `DiskLost` | 410 — the disk is gone; branch on `recovery`: after `resume` it is `["reset","recreate"]` (never frozen); after `reset` it is `["resume","rollback","recreate"]` (`rollback(0)` restores the newest save point); otherwise create a new sandbox |
| `MemoryExpired { recovery }` | 428 — snapshot expired; `reset` boots from disk |
| `Validation { errors }` | 400 / 422 |
| `Server`, `ClientDisconnected`, `Transport` | 5xx / 499 / network |
| `TemplateBuild`, `WaitTimeout`, `Config` | client-side |

Every API variant carries an `ApiErrorBody` (`status`, `code`, `message`, raw `detail`,
`retry_after`, `request_id`); `e.status()`, `e.code()`, `e.is_not_found()` are shortcuts.
Idempotent calls retry automatically on connection errors and 502/503/504 (3 attempts,
jittered backoff); `create`, `exec`, `run_code` and writes are never retried. API keys are
redacted from every error message.

## Configuration

| Setting | Builder | Environment | Default |
|---|---|---|---|
| API key | `.api_key(..)` | `SBBOX_API_KEY`, then `GSK_API_KEY` | required |
| Base URL | `.base_url(..)` | `SBBOX_BASE_URL` | `https://www.genspark.ai/api/sb-box/v1` |
| Timeout | `.timeout(Duration)` | — | 60 s per request |
| Retries | `.retries(n)` | — | 3 |
| User agent | `.user_agent_suffix(..)` | — | `sbbox-rust-sdk/<version>` |
| HTTP client | `.http_client(reqwest::Client)` | — | default rustls client |

## Features

- `blocking` — `sbbox::blocking::Client` / `Sandbox`, a synchronous wrapper for scripts and tests.
- `ws` — `sb.pty().connect_ws(..)`: an interactive terminal over WebSocket (`tokio-tungstenite`).

## Examples

```sh
SBBOX_API_KEY=gsk-... cargo run --example quickstart
SBBOX_API_KEY=gsk-... cargo run --example smoke        # create → exec → files → run_code → freeze → resume → kill
```

## License

MIT
