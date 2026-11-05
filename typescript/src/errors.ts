const KEY_RE = /gsk-[A-Za-z0-9._-]+/g

/** Replace any API key that leaked into a string. */
export function redact(text: string): string {
  return text.replace(KEY_RE, 'gsk-REDACTED')
}

export interface SbBoxErrorInit {
  status: number
  code?: string
  message: string
  detail?: unknown
  retryAfter?: number
  requestId?: string
}

/** Base class of every error raised by the SDK. */
export class SbBoxError extends Error {
  readonly status: number
  readonly code?: string
  /** Parsed response body (or the raw text when it was not JSON). */
  readonly detail: unknown
  /** Seconds from a `Retry-After` header, when the server sent one. */
  readonly retryAfter?: number
  readonly requestId?: string

  constructor(init: SbBoxErrorInit) {
    super(redact(init.message))
    this.name = new.target.name
    this.status = init.status
    this.code = init.code
    this.detail = init.detail
    this.retryAfter = init.retryAfter
    this.requestId = init.requestId
  }

  /** A named field from the error body (`plan`, `limit`, `balance`, `recovery`, …). */
  field<T = unknown>(name: string): T | undefined {
    const d = this.detail
    if (d && typeof d === 'object') {
      const o = d as Record<string, unknown>
      if (name in o) return o[name] as T
      const inner = o.detail
      if (inner && typeof inner === 'object' && name in (inner as object)) {
        return (inner as Record<string, unknown>)[name] as T
      }
    }
    return undefined
  }
}

export class ValidationError extends SbBoxError {}
export class AuthenticationError extends SbBoxError {}
export class InsufficientCreditsError extends SbBoxError {
  get balance(): number | undefined { return this.field<number>('balance') }
}
export class ForbiddenError extends SbBoxError {}
export class PlanRequiredError extends ForbiddenError {
  get plan(): string | undefined { return this.field<string>('plan') }
  get upgrade(): string | undefined { return this.field<string>('upgrade') }
}
export class NotFoundError extends SbBoxError {}
export class RegionRetiredError extends NotFoundError {
  get region(): string | undefined { return this.field<string>('region') }
}
export class CommandTimeoutError extends SbBoxError {
  get killed(): boolean { return this.field<boolean>('killed') === true }
}
export class ConflictError extends SbBoxError {}
export class LimitError extends ConflictError {
  get plan(): string | undefined { return this.field<string>('plan') }
  get limit(): number | undefined { return this.field<number>('limit') }
  get current(): number | undefined { return this.field<number>('current') }
  get requested(): number | undefined { return this.field<number>('requested') }
}
export class InterpreterUnavailableError extends ConflictError {}
/** 410 — the disk is gone. `recovery` lists the accepted next steps (`rollback` when save points exist). */
export class DiskLostError extends SbBoxError {
  get recovery(): string[] { return this.field<string[]>('recovery') ?? [] }
  get resumable(): boolean | undefined { return this.field<boolean>('resumable') }
}
export class MemoryExpiredError extends SbBoxError {
  get recovery(): string[] { return this.field<string[]>('recovery') ?? [] }
}
export class ClientDisconnectedError extends SbBoxError {}
export class ServerError extends SbBoxError {}
export class InterpreterStartFailedError extends ServerError {}
/** Network failure or request timeout before any HTTP status was received. */
export class SbBoxConnectionError extends SbBoxError {
  constructor(message: string, cause?: unknown) {
    super({ status: 0, message })
    if (cause !== undefined) (this as { cause?: unknown }).cause = cause
  }
}
/** `templates.waitReady` saw the build end in `error`. */
export class TemplateBuildError extends SbBoxError {
  readonly templateId: string
  readonly buildError?: string
  constructor(templateId: string, buildError?: string, detail?: unknown) {
    super({ status: 0, message: `template ${templateId} build failed: ${buildError ?? 'unknown error'}`, detail })
    this.templateId = templateId
    this.buildError = buildError
  }
}
/** A `wait` helper ran past its deadline. */
export class WaitTimeoutError extends SbBoxError {
  constructor(message: string) { super({ status: 0, message }) }
}

function pick(obj: unknown, key: string): unknown {
  if (obj && typeof obj === 'object' && key in (obj as object)) return (obj as Record<string, unknown>)[key]
  return undefined
}

/** Extract (code, message) from either `{"detail": …}` or `{"error": …}` envelopes. */
export function describeBody(body: unknown, fallback: string): { code?: string; message: string } {
  if (typeof body === 'string') return { message: body || fallback }
  if (!body || typeof body !== 'object') return { message: fallback }
  const detail = pick(body, 'detail')
  if (typeof detail === 'string') return { message: detail }
  if (detail && typeof detail === 'object') {
    const code = pick(detail, 'code')
    const msg = pick(detail, 'message') ?? pick(detail, 'error')
    return { code: typeof code === 'string' ? code : undefined, message: typeof msg === 'string' ? msg : JSON.stringify(detail) }
  }
  const err = pick(body, 'error')
  if (typeof err === 'string') {
    const code = pick(body, 'code')
    return { code: typeof code === 'string' ? code : undefined, message: err }
  }
  const reason = pick(body, 'reason')
  if (typeof reason === 'string') {
    const rec = pick(body, 'recovery')
    return { code: reason, message: Array.isArray(rec) ? `${reason} (recovery: ${rec.join(', ')})` : reason }
  }
  const message = pick(body, 'message')
  if (typeof message === 'string') {
    const errors = pick(body, 'errors')
    return { message: Array.isArray(errors) && errors.length ? `${message}: ${JSON.stringify(errors)}` : message }
  }
  const code = pick(body, 'code')
  return { code: typeof code === 'string' ? code : undefined, message: fallback }
}

/** Map an HTTP failure to the SDK error class from the contract table. */
export function errorFromResponse(status: number, body: unknown, headers: Headers): SbBoxError {
  const { code, message } = describeBody(body, `HTTP ${status}`)
  const ra = headers.get('retry-after')
  const retryAfter = ra != null && ra !== '' && !Number.isNaN(Number(ra)) ? Number(ra) : undefined
  const init: SbBoxErrorInit = {
    status, code, message, detail: body, retryAfter,
    requestId: headers.get('x-request-id') ?? undefined,
  }
  switch (status) {
    case 400: case 422: return new ValidationError(init)
    case 401: return new AuthenticationError(init)
    case 402: return new InsufficientCreditsError(init)
    case 403: return code === 'sb_box_plan_required' ? new PlanRequiredError(init) : new ForbiddenError(init)
    case 404: return code === 'sb_box_region_retired' ? new RegionRetiredError(init) : new NotFoundError(init)
    case 408: return new CommandTimeoutError(init)
    case 409:
      if (code && code.endsWith('_limit')) return new LimitError(init)
      if (code === 'sb_box_interpreter_unavailable') return new InterpreterUnavailableError(init)
      return new ConflictError(init)
    case 410: return new DiskLostError(init)
    case 428: return new MemoryExpiredError(init)
    case 499: return new ClientDisconnectedError(init)
    case 502: return code === 'sb_box_interpreter_start_failed' ? new InterpreterStartFailedError(init) : new ServerError(init)
    default:
      return status >= 500 ? new ServerError(init) : new SbBoxError(init)
  }
}
