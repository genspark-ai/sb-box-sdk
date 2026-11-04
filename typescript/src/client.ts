import { SbBoxError } from './errors.js'
import { DEFAULT_BASE_URL, ensureStringMap, Http, SDK_VERSION } from './http.js'
import { Sandbox } from './sandbox.js'
import { Templates } from './templates.js'
import type {
  CreateSandboxOptions, Grant, GrantOptions, Limits, ListState, MetricPoint, Regions, SandboxInfo,
  SandboxSummary, Usage, WaitOptions,
} from './types.js'

export interface SbBoxOptions {
  /** API key. Falls back to `SBBOX_API_KEY`, then `GSK_API_KEY`. */
  apiKey?: string
  /** Falls back to `SBBOX_BASE_URL`, then the production API. */
  baseUrl?: string
  /** Default request timeout in seconds (60). */
  timeout?: number
  /** Retries after the first attempt for reads and idempotent lifecycle calls (3). Set 0 to disable. */
  retries?: number
  /** Appended to the User-Agent, e.g. your app name. */
  userAgentSuffix?: string
  /** Custom fetch (tests, proxies). */
  fetch?: typeof fetch
}

function env(name: string): string | undefined {
  const p = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process
  return p?.env?.[name]
}

export class Sandboxes {
  constructor(private readonly http: Http) {}

  async create(opts: CreateSandboxOptions = {}): Promise<Sandbox> {
    const json: Record<string, unknown> = {}
    if (opts.templateId) json.templateId = opts.templateId
    else json.templateName = opts.template ?? 'base'
    if (opts.timeout !== undefined) json.timeout = opts.timeout
    if (opts.onTimeout) json.lifecycle = { onTimeout: opts.onTimeout }
    const md = ensureStringMap('metadata', opts.metadata)
    if (md) json.metadata = md
    const ev = ensureStringMap('envVars', opts.envVars)
    if (ev) json.envVars = ev
    if (opts.network) json.network = opts.network
    const info = await this.http.request<SandboxInfo>('/sandboxes', { method: 'POST', json, timeout: 120 })
    return new Sandbox(this.http, info)
  }

  /** Compact rows. `state` filters (one or many); omitted = every live state. */
  async list(state?: ListState | ListState[]): Promise<SandboxSummary[]> {
    const s = state === undefined ? undefined : Array.isArray(state) ? state.join(',') : state
    const r = await this.http.request<{ sandboxes: SandboxSummary[] }>('/sandboxes', { query: { state: s }, retry: true })
    return r.sandboxes ?? []
  }

  async shared(): Promise<Sandbox[]> {
    const rows = await this.http.request<SandboxInfo[]>('/sandboxes/shared', { retry: true })
    return rows.map((r) => new Sandbox(this.http, r))
  }

  async get(sandboxId: string): Promise<Sandbox> {
    const info = await this.http.request<SandboxInfo>(`/sandboxes/${encodeURIComponent(sandboxId)}`, { retry: true })
    return new Sandbox(this.http, info)
  }

  /** Alias of `get()`. */
  connect(sandboxId: string): Promise<Sandbox> { return this.get(sandboxId) }

  private handle(id: string): Sandbox {
    return new Sandbox(this.http, { id, state: 'unknown' })
  }

  async kill(sandboxId: string): Promise<void> { return this.handle(sandboxId).kill() }
  async freeze(sandboxId: string, opts: { wait?: boolean } & WaitOptions = {}): Promise<Sandbox> { return this.handle(sandboxId).freeze(opts) }
  async resume(sandboxId: string, opts: { timeout?: number } = {}): Promise<Sandbox> { return this.handle(sandboxId).resume(opts) }
  async rollback(sandboxId: string, opts: { version?: number; timeout?: number } = {}): Promise<Sandbox> { return this.handle(sandboxId).rollback(opts) }
  async clone(sandboxId: string, opts: { version?: number; metadata?: Record<string, string>; timeout?: number } = {}): Promise<Sandbox> { return this.handle(sandboxId).clone(opts) }
  async setTimeout(sandboxId: string, seconds: number): Promise<void> { return this.handle(sandboxId).setTimeout(seconds) }
  async reset(sandboxId: string, opts: { timeout?: number } = {}): Promise<Sandbox> { return this.handle(sandboxId).reset(opts) }
  async metrics(sandboxId: string, opts: { start?: number; end?: number } = {}): Promise<MetricPoint[]> { return this.handle(sandboxId).metrics(opts) }
  async grant(sandboxId: string, userId: string, opts: GrantOptions = {}): Promise<Grant> { return this.handle(sandboxId).grant(userId, opts) }
  async grants(sandboxId: string): Promise<Grant[]> { return this.handle(sandboxId).grants() }
  async revoke(sandboxId: string, userId: string): Promise<void> { return this.handle(sandboxId).revoke(userId) }
}

/** Entry point: `const sbbox = new SbBox({ apiKey })`. */
export class SbBox {
  readonly templates: Templates
  readonly sandboxes: Sandboxes
  readonly baseUrl: string
  private readonly http: Http

  constructor(opts: SbBoxOptions = {}) {
    const apiKey = opts.apiKey ?? env('SBBOX_API_KEY') ?? env('GSK_API_KEY')
    if (!apiKey) {
      throw new SbBoxError({ status: 0, message: 'SB-Box API key missing: pass { apiKey } or set SBBOX_API_KEY (or GSK_API_KEY)' })
    }
    const f = opts.fetch ?? globalThis.fetch
    if (typeof f !== 'function') {
      throw new SbBoxError({ status: 0, message: 'global fetch is not available: use Node 18+ or pass { fetch }' })
    }
    this.baseUrl = (opts.baseUrl ?? env('SBBOX_BASE_URL') ?? DEFAULT_BASE_URL).replace(/\/+$/, '')
    const ua = `sbbox-typescript-sdk/${SDK_VERSION} (+https://www.genspark.ai/sb-box/docs)` + (opts.userAgentSuffix ? ` ${opts.userAgentSuffix}` : '')
    this.http = new Http({
      apiKey, baseUrl: this.baseUrl, timeout: opts.timeout ?? 60, retries: opts.retries ?? 3, userAgent: ua, fetch: f,
    })
    this.templates = new Templates(this.http)
    this.sandboxes = new Sandboxes(this.http)
  }

  regions(): Promise<Regions> {
    return this.http.request<Regions>('/regions', { retry: true })
  }

  limits(): Promise<Limits> {
    return this.http.request<Limits>('/limits', { retry: true })
  }

  usage(days = 30): Promise<Usage> {
    return this.http.request<Usage>('/usage', { query: { days }, retry: true })
  }
}
