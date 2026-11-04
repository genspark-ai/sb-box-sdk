import { errorFromResponse, redact, SbBoxConnectionError, SbBoxError } from './errors.js'

export const SDK_VERSION = '0.1.0'
export const DEFAULT_BASE_URL = 'https://www.genspark.ai/api/sb-box/v1'

export interface HttpOptions {
  apiKey: string
  baseUrl: string
  /** Default per-request timeout in seconds. */
  timeout: number
  /** Retries after the first attempt for retryable calls (0 = no retry). */
  retries: number
  userAgent: string
  fetch: typeof fetch
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE'
  query?: Record<string, string | number | undefined>
  json?: unknown
  body?: Uint8Array | string
  contentType?: string
  headers?: Record<string, string>
  /** Seconds; overrides the client default. */
  timeout?: number
  /** Retry on connection errors and 502/503/504 (default false). */
  retry?: boolean
  /** Honour `Retry-After` on 503 until `deadlineMs`. */
  retryOn503Until?: number
  /** Return the raw Response (streams, bytes). */
  raw?: boolean
  /**
   * Raw responses only: when false, the timeout covers just the headers and the
   * caller owns the body for as long as it likes (unbounded server streams such
   * as `connect`). Default true: the timeout bounds the whole body; past it the
   * stream fails with `SbBoxConnectionError`.
   */
  bodyDeadline?: boolean
  signal?: AbortSignal
}

const BACKOFF_MS = [500, 1000, 2000]

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => { clearTimeout(t); reject(signal.reason) }, { once: true })
  })
}

export class Http {
  readonly opts: HttpOptions

  constructor(opts: HttpOptions) {
    this.opts = opts
  }

  url(path: string, query?: RequestOptions['query']): string {
    const u = new URL(this.opts.baseUrl.replace(/\/+$/, '') + path)
    if (query) {
      for (const [k, v] of Object.entries(query)) {
        if (v !== undefined && v !== null && v !== '') u.searchParams.set(k, String(v))
      }
    }
    return u.toString()
  }

  async request<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
    const res = await this.send(path, { ...opts, raw: false })
    return res as T
  }

  async raw(path: string, opts: RequestOptions = {}): Promise<Response> {
    return (await this.send(path, { ...opts, raw: true })) as Response
  }

  private async send(path: string, opts: RequestOptions): Promise<unknown> {
    const method = opts.method ?? 'GET'
    const headers: Record<string, string> = {
      'X-Api-Key': this.opts.apiKey,
      'User-Agent': this.opts.userAgent,
      Accept: opts.raw ? '*/*' : 'application/json',
      ...opts.headers,
    }
    let body: BodyInit | undefined
    if (opts.json !== undefined) {
      headers['Content-Type'] = 'application/json'
      body = JSON.stringify(opts.json)
    } else if (opts.body !== undefined) {
      headers['Content-Type'] = opts.contentType ?? 'application/octet-stream'
      body = opts.body as BodyInit
    }
    const url = this.url(path, opts.query)
    const timeoutMs = (opts.timeout ?? this.opts.timeout) * 1000
    const maxAttempts = opts.retry ? 1 + Math.max(0, this.opts.retries) : 1

    let attempt = 0
    while (true) {
      attempt++
      const ac = new AbortController()
      const onAbort = () => ac.abort(opts.signal?.reason)
      opts.signal?.addEventListener('abort', onAbort, { once: true })
      const timer = setTimeout(() => ac.abort(new Error(`request timed out after ${timeoutMs / 1000}s`)), timeoutMs)
      let res: Response
      try {
        res = await this.opts.fetch(url, { method, headers, body, signal: ac.signal })
      } catch (e) {
        clearTimeout(timer)
        opts.signal?.removeEventListener('abort', onAbort)
        if (opts.signal?.aborted) throw e
        if (opts.retry && attempt < maxAttempts) {
          await sleep(jitter(BACKOFF_MS[Math.min(attempt - 1, BACKOFF_MS.length - 1)]))
          continue
        }
        const msg = e instanceof Error ? e.message : String(e)
        throw new SbBoxConnectionError(`${method} ${path}: ${redact(msg)}`, e)
      }
      if (opts.raw && res.ok) {
        const release = () => {
          clearTimeout(timer)
          opts.signal?.removeEventListener('abort', onAbort)
        }
        if (opts.bodyDeadline === false || !res.body) {
          release()
          return res
        }
        return withBodyDeadline(res, ac.signal, release, `${method} ${path}: stream timed out after ${timeoutMs / 1000}s`)
      }
      if (res.ok) {
        try {
          return await this.parseBody(res)
        } finally {
          clearTimeout(timer)
          opts.signal?.removeEventListener('abort', onAbort)
        }
      }

      let parsed: unknown
      try {
        parsed = await this.parseBody(res)
      } catch {
        parsed = undefined
      } finally {
        clearTimeout(timer)
        opts.signal?.removeEventListener('abort', onAbort)
      }
      const err = errorFromResponse(res.status, parsed, res.headers)

      if (res.status === 503 && opts.retryOn503Until !== undefined) {
        const waitMs = (err.retryAfter ?? 2) * 1000
        if (Date.now() + waitMs <= opts.retryOn503Until) {
          await sleep(waitMs, opts.signal)
          continue
        }
      }
      if (opts.retry && attempt < maxAttempts && [502, 503, 504].includes(res.status)) {
        await sleep(jitter(BACKOFF_MS[Math.min(attempt - 1, BACKOFF_MS.length - 1)]), opts.signal)
        continue
      }
      throw err
    }
  }

  private async parseBody(res: Response): Promise<unknown> {
    const ct = res.headers.get('content-type') ?? ''
    const text = await res.text()
    if (!text) return undefined
    if (ct.includes('json')) {
      try { return JSON.parse(text) } catch { return text }
    }
    try { return JSON.parse(text) } catch { return text }
  }
}

/**
 * Re-wrap a streaming response so the request's abort signal keeps bounding the
 * body: an abort surfaces to the reader as `SbBoxConnectionError`, and the timer
 * is released once the body ends (normally or not).
 */
function withBodyDeadline(res: Response, signal: AbortSignal, release: () => void, timeoutMsg: string): Response {
  const reader = res.body!.getReader()
  const body = new ReadableStream<Uint8Array>({
    async pull(controller) {
      let chunk: ReadableStreamReadResult<Uint8Array>
      try {
        chunk = await reader.read()
      } catch (e) {
        release()
        const err = signal.aborted
          ? new SbBoxConnectionError(redact(timeoutMsg), e)
          : new SbBoxConnectionError(redact(e instanceof Error ? e.message : String(e)), e)
        controller.error(err)
        return
      }
      if (chunk.done) {
        release()
        controller.close()
        return
      }
      controller.enqueue(chunk.value)
    },
    async cancel(reason) {
      release()
      await reader.cancel(reason).catch(() => undefined)
    },
  })
  return new Response(body, { status: res.status, statusText: res.statusText, headers: res.headers })
}

function jitter(ms: number): number {
  return ms * (0.75 + Math.random() * 0.5)
}

export function ensureStringMap(name: string, m?: Record<string, unknown>): Record<string, string> | undefined {
  if (m === undefined) return undefined
  for (const [k, v] of Object.entries(m)) {
    if (typeof v !== 'string') {
      throw new SbBoxError({ status: 0, message: `${name}.${k} must be a string (got ${typeof v})` })
    }
  }
  return m as Record<string, string>
}
