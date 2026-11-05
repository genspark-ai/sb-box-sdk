# SB-Box Python SDK

Cloud sandboxes for AI agents — create an isolated Linux machine in about a second, run commands and
code in it, read and write files, freeze it when idle and resume it later with memory intact.

```bash
pip install genspark-sbbox
# until the first PyPI release:
pip install "git+https://github.com/genspark-ai/sb-box-sdk.git#subdirectory=python"
```

Python 3.9+. The only dependency is `httpx`. Sync (`sbbox.Client`) and async (`sbbox.AsyncClient`)
clients expose the same surface.

## Quickstart

```python
import sbbox

client = sbbox.Client()  # reads SBBOX_API_KEY (or GSK_API_KEY)

with client.sandboxes.create() as sb:  # public "base" image; killed on exit
    print(sb.exec("python3 --version").stdout)

    sb.files.write("/home/user/hello.py", "print('hi from the sandbox')")
    print(sb.exec(["python3", "/home/user/hello.py"]).stdout)

    result = sb.run_code("import math; math.pi * 2")  # stateful interpreter (base image)
    print(result.text)  # "6.283185307179586"

    print(sb.port_url(8000))  # public URL for a service listening on :8000
```

Keep a machine around between sessions:

```python
sb = client.sandboxes.create("base", timeout=0)  # no deadline; the idle timer still applies
sb.exec("git clone https://github.com/you/project /home/user/project")
sb.freeze()  # memory + disk kept, no compute billed
...
sb = client.sandboxes.get("sbx-…")  # later, anywhere
sb.resume()  # ~1 s, processes still there
sb.exec("cd /home/user/project && make test")
sb.kill()
```

Get an API key from the Genspark console and keep it in `SBBOX_API_KEY`. Full guide:
<https://www.genspark.ai/sb-box/docs>.

## What you can do

| | |
|---|---|
| **Sandboxes** | `client.sandboxes.create(template="base", timeout=None, on_timeout="freeze"\|"kill", metadata=…, env_vars=…, network=…)`, `.list(state=…)`, `.get(id)`, `.shared()` |
| **Lifecycle** | `sb.freeze(wait=True)`, `sb.resume()`, `sb.rollback(version=0)`, `sb.clone()`, `sb.reset()`, `sb.set_timeout(seconds)`, `sb.kill()`, `sb.refresh()`, `sb.metrics()` |
| **Commands** | `sb.exec(cmd, cwd=…, env=…, user=…, timeout=60)` → `CommandResult(stdout, stderr, exit_code)`; `sb.exec_stream(cmd)` yields `ExecEvent`s; `sb.commands.start(cmd)` → background handle with `.input() .close() .wait() .kill() .connect()` |
| **Code** | `sb.run_code(code, language="python", context_id=…)` → `CodeResult(stdout, stderr, error, results)`; `result.text` is the main result |
| **Files** | `sb.files.write(path, bytes\|str)`, `.read(path)` → bytes, `.read_text(path)`, `.list(path)` |
| **PTY** | `sb.pty.open(rows=24, cols=80)` → `.input() .resize() .kill() .connect()` (yields `PtyEvent`s) |
| **Templates** | `client.templates.build(image=… \| dockerfile=…, cpu_count=1, memory_mb=2048, disk_size_gb=20)` then `client.templates.wait_ready(id)`; `.list() .shared() .get() .delete()` |
| **Sharing** | `sb.grant(user_id, perm="read"\|"write")`, `.grants()`, `.revoke(user_id)` — same on templates |
| **Account** | `client.regions()`, `client.limits()`, `client.usage(days=30)` |

A `str` command runs through `/bin/sh -c`; a `list` is executed as-is. All timeouts are **seconds**.
A non-zero exit code is returned in `CommandResult.exit_code`, not raised.

Streaming output:

```python
for ev in sb.exec_stream("for i in 1 2 3; do echo $i; sleep 1; done"):
    if ev.kind == "stdout":
        print(ev.data, end="")
    elif ev.kind == "exit":
        print("exit", ev.exit_code)
```

Async:

```python
async with sbbox.AsyncClient() as client:
    sb = await client.sandboxes.create()
    print((await sb.exec("uname -a")).stdout)
    await sb.kill()
```

## Errors

Every error derives from `sbbox.SbBoxError` and carries `status`, `code`, `message`, `detail`
(the raw response body), `retry_after` and `request_id`.

| Exception | When |
|---|---|
| `AuthenticationError` (401) | missing or invalid key |
| `PlanRequiredError` (403) | your plan does not include SB-Box — see `.plan`, `.upgrade` |
| `InsufficientCreditsError` (402) | out of credits — `.balance` |
| `LimitError` (409) | concurrency / spec / build limit — `.limit`, `.current`, `.requested` |
| `NotFoundError` (404) | unknown or killed sandbox / template; `RegionRetiredError` subclass |
| `CommandTimeoutError` (408) | `exec` exceeded its timeout (`.killed`) |
| `MemoryExpiredError` (428) | the frozen memory image expired — call `sb.reset()` |
| `DiskLostError` (410) | the disk is gone — create a new sandbox (rollback and reset are refused too) |
| `InterpreterUnavailableError` / `InterpreterStartFailedError` | `run_code` on an image without the interpreter |
| `ValidationError` (400/422), `ConflictError` (409), `ServerError` (5xx), `TransportError` | the usual |
| `TemplateBuildError`, `WaitTimeout` | raised by the client-side waits |

API keys are redacted from every error message.

## Configuration

| Setting | Argument | Environment | Default |
|---|---|---|---|
| API key | `Client(api_key=…)` | `SBBOX_API_KEY`, then `GSK_API_KEY` | required |
| Base URL | `Client(base_url=…)` | `SBBOX_BASE_URL` | `https://www.genspark.ai/api/sb-box/v1` |
| Request timeout | `Client(timeout=60)` | — | 60 s (`exec`/`run_code` use their own timeout + 30 s) |
| Retries | `Client(retries=3)` | — | reads and idempotent lifecycle calls retry on 502/503/504 and connection errors; `create`, `exec`, `run_code`, file writes never retry |

## License

MIT
