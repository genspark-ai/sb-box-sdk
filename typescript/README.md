# @genspark/sb-box

TypeScript / JavaScript SDK for [Genspark SB-Box](https://www.genspark.ai/sb-box/docs) — fast
sandboxes you can create, run commands in, freeze and resume, from Node 18+ (or any runtime with
`fetch`).

```bash
npm install @genspark/sb-box
```

## Quickstart

```ts
import { SbBox } from '@genspark/sb-box'

const sbbox = new SbBox({ apiKey: process.env.SBBOX_API_KEY })

const sandbox = await sbbox.sandboxes.create({ timeout: 600 })        // public "base" template
const r = await sandbox.exec('python3 -c "print(6*7)"')
console.log(r.stdout, r.exitCode)                                      // "42\n" 0

await sandbox.files.write('/home/user/hello.txt', 'hi there')
console.log(await sandbox.files.readText('/home/user/hello.txt'))

const code = await sandbox.runCode('import sys; sys.version')         // stateful interpreter
console.log(code.results[0])

await sandbox.freeze()           // memory + disk kept; only disk is billed while frozen
await sandbox.resume()           // back in about a second, processes intact
await sandbox.kill()
```

Reconnect to an existing sandbox with `await sbbox.sandboxes.get(id)`.

## Configuration

| Option | Env var | Default |
|---|---|---|
| `apiKey` | `SBBOX_API_KEY` (or `GSK_API_KEY`) | — (required) |
| `baseUrl` | `SBBOX_BASE_URL` | `https://www.genspark.ai/api/sb-box/v1` |
| `timeout` | | 60 s per request |
| `retries` | | 3 retries (after the first attempt) for reads and idempotent lifecycle calls; `0` disables |

Create a key in the Genspark console. All lifetimes in this SDK are **seconds**.

## What's in the box

- `sbbox.regions()`, `sbbox.limits()`, `sbbox.usage(days)`
- `sbbox.templates` — `build()`, `waitReady()`, `list()`, `shared()`, `get()`, `delete()`, `grant()`, `grants()`, `revoke()`
- `sbbox.sandboxes` — `create()`, `list(state)`, `shared()`, `get()`, plus id-based `kill/freeze/resume/rollback/clone/setTimeout/reset/metrics`
- `Sandbox` handle
  - `exec(command, { cwd, envVars, user, timeout })` → `{ stdout, stderr, exitCode }` (non-zero exit is **not** an error)
  - `execStream(command)` → `for await (const ev of …)` with `stdout` / `stderr` / `exit` / `error` events
  - `runCode(code, { language, contextId, timeout, images })` → rich results (text, html, png, …)
  - `commands.start()` → background process with `input()`, `connect()`, `wait()`, `kill()`
  - `files.write(path, bytes | string)`, `files.read(path)`, `files.readText(path)`, `files.list(path)`
  - `pty.open()` (SSE output) and `pty.connectWs()` (WebSocket; needs Node 22+ or `npm i ws`)
  - `freeze({ wait })`, `resume()`, `rollback({ version })`, `clone()`, `reset()`, `setTimeout(seconds)`, `metrics()`
  - `portUrl(port)` — public URL of a service listening inside the sandbox
  - `grant(userId, { perm })` / `grants()` / `revoke(userId)` to share with teammates

Streaming a long command:

```ts
for await (const ev of sandbox.execStream(['npm', 'test'], { timeout: 300 })) {
  if (ev.kind === 'stdout') process.stdout.write(ev.data!)
  else if (ev.kind === 'exit') console.log('exit', ev.exitCode)
}
```

Building your own template:

```ts
const { templateId } = await sbbox.templates.build({ name: 'py312', image: 'python:3.12', cpuCount: 2, memoryMB: 4096 })
const tpl = await sbbox.templates.waitReady(templateId)          // throws TemplateBuildError on failure
const sb = await sbbox.sandboxes.create({ templateId: tpl.id })
```

## Errors

Every failure is an `SbBoxError` with `status`, `code`, `message`, `detail` (raw body) and
`retryAfter`. Catch the subclass you care about:

| Class | When |
|---|---|
| `AuthenticationError` | 401 — missing or invalid key |
| `PlanRequiredError` (`plan`, `upgrade`) | 403 — your plan cannot do this |
| `InsufficientCreditsError` (`balance`) | 402 |
| `LimitError` (`limit`, `current`) | 409 — concurrency / spec / build limits |
| `NotFoundError`, `RegionRetiredError` | 404 — unknown or killed sandbox, retired region |
| `CommandTimeoutError` (`killed`) | 408 — `exec` ran past its timeout |
| `MemoryExpiredError` (`recovery`) | 428 — frozen memory expired; `reset()` boots from disk |
| `DiskLostError` | 410 — the disk is gone; branch on `.recovery`: after `resume()` it is `["reset","recreate"]` (never frozen); after `reset()` it is `["resume","rollback","recreate"]` (`rollback(0)` restores the newest save point); otherwise create a new sandbox |
| `InterpreterUnavailableError`, `InterpreterStartFailedError` | `runCode` on an image without the interpreter |
| `ValidationError` | 400 / 422 |
| `ServerError`, `SbBoxConnectionError` | 5xx, network / timeout |
| `TemplateBuildError`, `WaitTimeoutError` | raised by the `wait*` helpers |

API keys are redacted from every error message.

## License

MIT
