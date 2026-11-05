import { NotFoundError, SbBoxError, WaitTimeoutError } from './errors.js'
import { ensureStringMap, type Http } from './http.js'
import { parseJsonData, parseSse } from './sse.js'
import type {
  CodeResult, CommandResult, ConnectOptions, ExecEvent, ExecOptions, FileEntry, Grant, GrantOptions, MetricPoint,
  ProcessInfo, PtyEvent, PtyOptions, RunCodeOptions, SandboxInfo, SandboxState, SandboxVersion,
  StartCommandOptions, WaitOptions,
} from './types.js'

const DEFAULT_SHELL = ['/bin/sh', '-c']

function argv(command: string | string[]): string[] {
  if (typeof command === 'string') return [...DEFAULT_SHELL, command]
  if (!Array.isArray(command) || command.length === 0) throw new TypeError('command must be a non-empty argv array or a shell string')
  return command
}

function execBody(command: string | string[], opts: ExecOptions | StartCommandOptions, withTimeout: boolean, max?: number) {
  const body: Record<string, unknown> = { command: argv(command) }
  if (opts.cwd) body.cwd = opts.cwd
  const env = ensureStringMap('envVars', opts.envVars)
  if (env) body.envVars = env
  if (opts.user) body.user = opts.user
  if (withTimeout) {
    const t = (opts as ExecOptions).timeout ?? 60
    if (max !== undefined && t > max) throw new RangeError(`timeout must be <= ${max} seconds`)
    body.timeout = t
  }
  return body
}

async function* execEvents(res: Response): AsyncGenerator<ExecEvent> {
  if (!res.body) return
  let execId: string | undefined
  for await (const m of parseSse(res.body)) {
    const d = parseJsonData<Record<string, unknown>>(m) ?? {}
    switch (m.event) {
      case 'execId':
        execId = typeof d.execId === 'string' ? d.execId : undefined
        break
      case 'stdout':
      case 'stderr': {
        const seq = m.id !== undefined && /^\d+$/.test(m.id) ? Number(m.id) : undefined
        yield { kind: m.event, data: String(d.data ?? ''), execId, ...(seq !== undefined ? { seq } : {}) }
        break
      }
      case 'exit':
        yield { kind: 'exit', exitCode: Number(d.exitCode ?? 0), execId }
        return
      case 'gap':
        yield { kind: 'gap', data: String(d.error ?? m.data), execId }
        break
      case 'done':
        yield { kind: 'done', data: String(d.status ?? ''), execId }
        return
      case 'error':
        yield { kind: 'error', data: String(d.error ?? m.data), stage: typeof d.stage === 'string' ? d.stage : undefined, execId }
        return
      default:
        break
    }
  }
}

/** A background command started with `sandbox.commands.start()`. */
export class BackgroundCommand {
  constructor(
    private readonly http: Http,
    readonly sandboxId: string,
    readonly execId: string,
    readonly pid: number,
  ) {}

  private p(suffix: string) { return `/sandboxes/${encodeURIComponent(this.sandboxId)}/commands/${encodeURIComponent(this.execId)}${suffix}` }

  async input(data: string): Promise<number> {
    const r = await this.http.request<{ written: number }>(this.p('/input'), { method: 'POST', json: { data } })
    return r.written
  }

  async close(): Promise<void> {
    await this.http.request(this.p('/close'), { method: 'POST' })
  }

  /** Block until the command exits; returns its exit code. `timeout` is seconds (default 600). */
  async wait(timeout = 600): Promise<number> {
    const r = await this.http.request<{ exitCode: number }>(this.p('/wait'), { method: 'POST', timeout: timeout + 30 })
    return r.exitCode
  }

  async kill(signal = 9): Promise<void> {
    await this.http.request(`/sandboxes/${encodeURIComponent(this.sandboxId)}/commands/kill`, {
      method: 'POST', json: { pid: this.pid, signal },
    })
  }

  /**
   * Attach to the command's output stream (SSE; user guide §8.3 / §15.4). Yields `stdout` /
   * `stderr` events carrying `seq`, a `gap` event when the server had to drop unread output,
   * `error` on a server-side failure, and a final `done` event once the command has exited.
   * Detaching never kills the command. Pass `fromSeq` (the last `seq` you processed) to
   * re-attach without replay. A command the server already reaped yields nothing (404) or an
   * `error` event followed by `done`.
   */
  async *connect(opts: ConnectOptions = {}): AsyncGenerator<ExecEvent> {
    let res: Response
    try {
      res = await this.http.raw(this.p('/connect'), {
        headers: { Accept: 'text/event-stream' }, timeout: 3600, bodyDeadline: false,
        query: opts.fromSeq ? { from_seq: opts.fromSeq } : undefined,
      })
    } catch (e) {
      if (e instanceof NotFoundError) return
      throw e
    }
    yield* execEvents(res)
  }
}

export class Commands {
  constructor(private readonly http: Http, private readonly sandboxId: string) {}
  private p(suffix: string) { return `/sandboxes/${encodeURIComponent(this.sandboxId)}/commands${suffix}` }

  async start(command: string | string[], opts: StartCommandOptions = {}): Promise<BackgroundCommand> {
    const r = await this.http.request<{ execId: string; pid: number }>(this.p('/start'), {
      method: 'POST', json: execBody(command, opts, false), timeout: 120,
    })
    return new BackgroundCommand(this.http, this.sandboxId, r.execId, r.pid)
  }

  async list(): Promise<ProcessInfo[]> {
    return this.http.request<ProcessInfo[]>(this.p(''), { retry: true })
  }

  async kill(pid: number, signal = 9): Promise<void> {
    await this.http.request(this.p('/kill'), { method: 'POST', json: { pid, signal } })
  }

  async sendStdin(pid: number, data: string): Promise<void> {
    await this.http.request(this.p('/send-stdin'), { method: 'POST', json: { pid, data } })
  }
}

export class Files {
  constructor(private readonly http: Http, private readonly sandboxId: string) {}
  private p(suffix: string) { return `/sandboxes/${encodeURIComponent(this.sandboxId)}/files${suffix}` }

  /** Write bytes (or a UTF-8 string). Returns the byte count written. */
  async write(path: string, data: Uint8Array | string, opts: { user?: string } = {}): Promise<number> {
    const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data
    const r = await this.http.request<{ written: number }>(this.p('/stream'), {
      method: 'POST', body: bytes, query: { path, user: opts.user }, timeout: 600,
    })
    return r.written
  }

  async read(path: string, opts: { user?: string } = {}): Promise<Uint8Array> {
    const res = await this.http.raw(this.p('/stream'), { query: { path, user: opts.user }, timeout: 600, retry: true })
    return new Uint8Array(await res.arrayBuffer())
  }

  async readText(path: string, opts: { user?: string; encoding?: string } = {}): Promise<string> {
    const bytes = await this.read(path, opts)
    return new TextDecoder(opts.encoding ?? 'utf-8').decode(bytes)
  }

  async list(path = '/', opts: { user?: string } = {}): Promise<FileEntry[]> {
    return this.http.request<FileEntry[]>(this.p('/list'), { query: { path, user: opts.user }, retry: true })
  }
}

/** An interactive terminal opened with `sandbox.pty.open()`. */
export class PtySession {
  constructor(
    private readonly http: Http,
    readonly sandboxId: string,
    readonly execId: string,
    readonly pid: number,
  ) {}
  private p(suffix: string) { return `/sandboxes/${encodeURIComponent(this.sandboxId)}/pty/${encodeURIComponent(this.execId)}${suffix}` }

  async input(data: Uint8Array | string): Promise<void> {
    const bytes = typeof data === 'string' ? new TextEncoder().encode(data) : data
    await this.http.request(this.p('/input'), { method: 'POST', body: bytes })
  }

  async resize(rows: number, cols: number): Promise<void> {
    await this.http.request(this.p('/resize'), { method: 'POST', json: { rows, cols } })
  }

  async kill(): Promise<void> {
    await this.http.request(this.p('/kill'), { method: 'POST' })
  }

  /** Stream terminal output (SSE). Ends with a `exit` event. */
  async *connect(): AsyncGenerator<PtyEvent> {
    const res = await this.http.raw(this.p('/connect'), { headers: { Accept: 'text/event-stream' }, timeout: 3600, bodyDeadline: false })
    if (!res.body) return
    for await (const m of parseSse(res.body)) {
      const d = parseJsonData<Record<string, unknown>>(m) ?? {}
      if (m.event === 'data') yield { kind: 'data', data: String(d.data ?? '') }
      else if (m.event === 'exit') {
        yield { kind: 'exit', exitCode: d.exitCode === undefined ? undefined : Number(d.exitCode), reason: typeof d.reason === 'string' ? d.reason : undefined }
        return
      }
    }
  }
}

export interface PtyWsHandlers {
  onData?: (data: string, seq?: number) => void
  onExit?: (exitCode: number) => void
  onError?: (error: string) => void
}

/** A terminal over the WebSocket transport (`GET /sandboxes/{id}/ws/pty`). */
export class PtyWebSocket {
  constructor(private readonly ws: WsLike) {}

  input(data: string): void { this.ws.send(JSON.stringify({ type: 'stdin', data })) }
  resize(rows: number, cols: number): void { this.ws.send(JSON.stringify({ type: 'resize', rows, cols })) }
  kill(): void { this.ws.send(JSON.stringify({ type: 'kill' })) }
  close(): void { this.ws.close() }
}

interface WsLike {
  send(data: string): void
  close(): void
  addEventListener(type: string, listener: (ev: { data?: unknown; message?: string }) => void): void
}

export class Pty {
  constructor(private readonly http: Http, private readonly sandboxId: string) {}

  async open(opts: PtyOptions = {}): Promise<PtySession> {
    const json: Record<string, unknown> = {
      command: opts.command ?? ['/bin/bash'],
      rows: opts.rows ?? 24,
      cols: opts.cols ?? 80,
    }
    if (opts.cwd) json.cwd = opts.cwd
    const env = ensureStringMap('envVars', opts.envVars)
    if (env) json.envVars = env
    const r = await this.http.request<{ execId: string; pid: number }>(
      `/sandboxes/${encodeURIComponent(this.sandboxId)}/pty`, { method: 'POST', json, timeout: 120 },
    )
    return new PtySession(this.http, this.sandboxId, r.execId, r.pid)
  }

  /**
   * Open a terminal over WebSocket. Uses the global `WebSocket` (Node 22+, browsers) or the
   * optional `ws` package. Resolves once the socket is open and the start frame is sent.
   */
  async connectWs(opts: PtyOptions = {}, handlers: PtyWsHandlers = {}): Promise<PtyWebSocket> {
    const url = new URL(this.http.url(`/sandboxes/${encodeURIComponent(this.sandboxId)}/ws/pty`))
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
    const headers = { 'X-Api-Key': this.http.opts.apiKey, 'User-Agent': this.http.opts.userAgent }

    let ws: WsLike
    const G = globalThis as { WebSocket?: new (url: string, protocols?: string[]) => WsLike }
    if (G.WebSocket) {
      // Browser-style constructor cannot set headers: fall back to the query parameter.
      url.searchParams.set('api_key', this.http.opts.apiKey)
      ws = new G.WebSocket(url.toString())
    } else {
      let mod: { default?: unknown; WebSocket?: unknown }
      try {
        mod = (await import('ws')) as unknown as { default?: unknown; WebSocket?: unknown }
      } catch {
        throw new SbBoxError({ status: 0, message: 'pty.connectWs needs a WebSocket implementation: upgrade to Node 22+ or `npm install ws`' })
      }
      const Ctor = (mod.default ?? mod.WebSocket) as new (url: string, opts: { headers: Record<string, string> }) => WsLike
      ws = new Ctor(url.toString(), { headers })
    }

    await new Promise<void>((resolve, reject) => {
      ws.addEventListener('open', () => resolve())
      ws.addEventListener('error', (ev) => reject(new SbBoxError({ status: 0, message: `pty websocket error: ${ev.message ?? 'connection failed'}` })))
    })
    const start: Record<string, unknown> = {}
    if (opts.command) start.command = opts.command
    if (opts.cwd) start.cwd = opts.cwd
    if (opts.rows) start.rows = opts.rows
    if (opts.cols) start.cols = opts.cols
    if (opts.envVars) start.envVars = ensureStringMap('envVars', opts.envVars)
    ws.send(JSON.stringify(start))
    ws.addEventListener('message', (ev) => {
      let frame: Record<string, unknown>
      try { frame = JSON.parse(String(ev.data)) } catch { return }
      if (frame.type === 'stdout') handlers.onData?.(String(frame.data ?? ''), typeof frame.seq === 'number' ? frame.seq : undefined)
      else if (frame.type === 'exit') handlers.onExit?.(Number(frame.exitCode ?? 0))
      else if (frame.type === 'error') handlers.onError?.(String(frame.error ?? ''))
    })
    return new PtyWebSocket(ws)
  }
}

/** Handle on one sandbox, bound to the client that created it. */
export class Sandbox {
  readonly commands: Commands
  readonly files: Files
  readonly pty: Pty
  private _info: SandboxInfo

  constructor(private readonly http: Http, info: SandboxInfo) {
    this._info = info
    this.commands = new Commands(http, info.id)
    this.files = new Files(http, info.id)
    this.pty = new Pty(http, info.id)
  }

  /** The last record seen for this sandbox (call `refresh()` to update). */
  get info(): SandboxInfo { return this._info }
  get id(): string { return this._info.id }
  get state(): SandboxState { return this._info.state }
  get templateId(): string | undefined { return this._info.templateId ?? this._info.template_id }
  get region(): string | undefined { return this._info.region }
  get metadata(): Record<string, string> { return this._info.metadata ?? {} }
  get versions(): SandboxVersion[] { return this._info.versions ?? [] }
  get createdAt(): string | undefined { return this._info.createdAt ?? this._info.created_at }
  get frozenAt(): string | undefined { return this._info.frozenAt }
  get timeoutAt(): string | undefined { return this._info.timeoutAt }
  /** Token to reach a sandbox created with `allowPublicTraffic: false` (create response only). */
  get trafficAccessToken(): string | undefined { return this._info.trafficAccessToken }

  /** Public URL of a port inside the sandbox. */
  portUrl(port: number): string {
    const tpl = this._info.portUrl
    if (!tpl) throw new SbBoxError({ status: 0, message: `sandbox ${this.id} has no portUrl` })
    return tpl.replace('{port}', String(port))
  }

  private p(suffix = '') { return `/sandboxes/${encodeURIComponent(this.id)}${suffix}` }

  private update(info: SandboxInfo): this {
    this._info = { ...this._info, ...info }
    return this
  }

  async refresh(): Promise<this> {
    return this.update(await this.http.request<SandboxInfo>(this.p(), { retry: true }))
  }

  async kill(): Promise<void> {
    await this.http.request(this.p(), { method: 'DELETE', retry: true })
  }

  /** Freeze. The server may answer 202 (still freezing); with `wait` (default) we poll until `frozen`. */
  async freeze(opts: { wait?: boolean } & WaitOptions = {}): Promise<this> {
    const r = await this.http.request<SandboxInfo & { status?: string }>(this.p('/freeze'), { method: 'POST', retry: true, timeout: 120 })
    if (r.id) this.update(r)
    else if (r.state) this._info = { ...this._info, state: r.state }
    if (opts.wait === false || this.state === 'frozen') return this
    return this.waitForState('frozen', { timeout: opts.timeout ?? 90, interval: opts.interval ?? 2 })
  }

  async waitForState(target: SandboxState | SandboxState[], opts: WaitOptions = {}): Promise<this> {
    const targets = Array.isArray(target) ? target : [target]
    const deadline = Date.now() + (opts.timeout ?? 90) * 1000
    const interval = (opts.interval ?? 2) * 1000
    while (true) {
      await this.refresh()
      if (targets.includes(this.state)) return this
      if (Date.now() + interval > deadline) {
        throw new WaitTimeoutError(`sandbox ${this.id} is ${this.state}, not ${targets.join('/')}, after ${opts.timeout ?? 90}s`)
      }
      await new Promise((r) => setTimeout(r, interval))
    }
  }

  async resume(opts: { timeout?: number } = {}): Promise<this> {
    const json: Record<string, unknown> = {}
    if (opts.timeout !== undefined) json.timeout = opts.timeout
    return this.update(await this.http.request<SandboxInfo>(this.p('/resume'), { method: 'POST', json, retry: true, timeout: 120 }))
  }

  async rollback(opts: { version?: number; timeout?: number } = {}): Promise<this> {
    const json: Record<string, unknown> = { version: opts.version ?? 0 }
    if (opts.timeout !== undefined) json.timeout = opts.timeout
    return this.update(await this.http.request<SandboxInfo>(this.p('/rollback'), { method: 'POST', json, timeout: 120 }))
  }

  /** Clone into a new sandbox owned by the caller. Retries on 503 + Retry-After until `timeout` (s). */
  async clone(opts: { version?: number; metadata?: Record<string, string>; timeout?: number } = {}): Promise<Sandbox> {
    const json: Record<string, unknown> = { version: opts.version ?? 0 }
    const md = ensureStringMap('metadata', opts.metadata)
    if (md) json.metadata = md
    const total = opts.timeout ?? 120
    const info = await this.http.request<SandboxInfo>(this.p('/clone'), {
      method: 'POST', json, timeout: 120, retryOn503Until: Date.now() + total * 1000,
    })
    return new Sandbox(this.http, info)
  }

  /** Set the lifetime deadline in seconds from now; 0 clears it. */
  async setTimeout(seconds: number): Promise<void> {
    await this.http.request(this.p('/timeout'), { method: 'POST', json: { timeout: seconds }, retry: true })
  }

  async reset(opts: { timeout?: number } = {}): Promise<this> {
    const total = opts.timeout ?? 120
    return this.update(await this.http.request<SandboxInfo>(this.p('/reset'), {
      method: 'POST', timeout: 120, retryOn503Until: Date.now() + total * 1000,
    }))
  }

  async metrics(opts: { start?: number; end?: number } = {}): Promise<MetricPoint[]> {
    return this.http.request<MetricPoint[]>(this.p('/metrics'), { query: { start: opts.start, end: opts.end }, retry: true })
  }

  async grant(userId: string, opts: GrantOptions = {}): Promise<Grant> {
    const json: Record<string, unknown> = { perm: opts.perm ?? 'read' }
    if (opts.expiresAt) json.expires_at = opts.expiresAt
    if (opts.reason) json.reason = opts.reason
    return this.http.request<Grant>(this.p(`/grants/${encodeURIComponent(userId)}`), { method: 'PUT', json })
  }

  async grants(): Promise<Grant[]> {
    return this.http.request<Grant[]>(this.p('/grants'), { retry: true })
  }

  async revoke(userId: string): Promise<void> {
    await this.http.request(this.p(`/grants/${encodeURIComponent(userId)}`), { method: 'DELETE' })
  }

  /** Run a command and wait for it. A non-zero exit is returned, not thrown. */
  async exec(command: string | string[], opts: ExecOptions = {}): Promise<CommandResult> {
    const body = execBody(command, opts, true, 600)
    return this.http.request<CommandResult>(this.p('/exec'), {
      method: 'POST', json: body, timeout: (body.timeout as number) + 30,
    })
  }

  /** Run a command and stream its output as it is produced. */
  async *execStream(command: string | string[], opts: ExecOptions = {}): AsyncGenerator<ExecEvent> {
    const body = execBody(command, opts, true, 600)
    const res = await this.http.raw(this.p('/sse/exec'), {
      method: 'POST', json: body, headers: { Accept: 'text/event-stream' }, timeout: (body.timeout as number) + 30,
    })
    yield* execEvents(res)
  }

  /** Execute code in the sandbox's stateful interpreter. */
  async runCode(code: string, opts: RunCodeOptions = {}): Promise<CodeResult> {
    const timeout = opts.timeout ?? 60
    if (timeout > 540) throw new RangeError('runCode timeout must be <= 540 seconds')
    const json: Record<string, unknown> = { code, language: opts.language ?? 'python', timeout }
    if (opts.contextId) json.contextId = opts.contextId
    const env = ensureStringMap('envVars', opts.envVars)
    if (env) json.envVars = env
    if (opts.images) json.images = opts.images
    return this.http.request<CodeResult>(this.p('/run-code'), { method: 'POST', json, timeout: timeout + 30 })
  }
}
