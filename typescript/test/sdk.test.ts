import { afterAll, beforeAll, beforeEach, describe, expect, it } from 'vitest'
import {
  AuthenticationError, ClientDisconnectedError, CommandTimeoutError, ConflictError, DiskLostError, ForbiddenError,
  InsufficientCreditsError, InterpreterStartFailedError, InterpreterUnavailableError, LimitError, MemoryExpiredError,
  NotFoundError, PlanRequiredError, RegionRetiredError, SbBox, SbBoxConnectionError, SbBoxError, ServerError,
  TemplateBuildError, ValidationError, WaitTimeoutError, errorFromResponse, parseSse, redact,
} from '../src/index.js'
import { Http, SDK_VERSION } from '../src/http.js'
import { KEY, startMock } from './mock-server.js'

let mock: Awaited<ReturnType<typeof startMock>>
let sb: SbBox

beforeAll(async () => {
  mock = await startMock()
  sb = new SbBox({ apiKey: KEY, baseUrl: mock.url })
})
afterAll(() => mock.close())
beforeEach(() => {
  mock.state.requests.length = 0
  mock.state.freezeMode = '200'
  mock.state.freezePolls = 0
  mock.state.cloneRetries = 0
  mock.state.flakyGets = 0
  for (const h of mock.state.hangs) h.destroy()
  mock.state.hangs.length = 0
  mock.state.buildPolls = 2
  mock.state.buildOutcome = 'ready'
  mock.state.files.clear()
  mock.state.killed.clear()
})

const last = () => mock.state.requests.at(-1)!

describe('client construction', () => {
  it('requires a key and names the env vars', () => {
    const vars = ['SBBOX_API_KEY', 'GSK_API_KEY', 'SBBOX_BASE_URL'] as const
    const saved = Object.fromEntries(vars.map((v) => [v, process.env[v]]))
    for (const v of vars) delete process.env[v]
    try {
      expect(() => new SbBox()).toThrow(/SBBOX_API_KEY.*GSK_API_KEY/)
      process.env.GSK_API_KEY = KEY
      expect(new SbBox().baseUrl).toBe('https://www.genspark.ai/api/sb-box/v1')
      process.env.SBBOX_BASE_URL = 'https://example.test/api/sb-box/v1/'
      expect(new SbBox().baseUrl).toBe('https://example.test/api/sb-box/v1')
    } finally {
      for (const v of vars) {
        if (saved[v] === undefined) delete process.env[v]
        else process.env[v] = saved[v]
      }
    }
  })

  it('sends X-Api-Key and User-Agent', async () => {
    await sb.regions()
    expect(last().headers['x-api-key']).toBe(KEY)
    expect(last().headers['user-agent']).toMatch(new RegExp('^sbbox-typescript-sdk/' + SDK_VERSION.replace(/\./g, '\\.')))
  })

  it('rejects a bad key with AuthenticationError', async () => {
    const bad = new SbBox({ apiKey: 'gsk-wrong', baseUrl: mock.url })
    await expect(bad.regions()).rejects.toBeInstanceOf(AuthenticationError)
  })
})

describe('account', () => {
  it('regions / limits / usage', async () => {
    expect((await sb.regions()).default).toBe('westus2')
    const l = await sb.limits()
    expect(l.limits.maxConcurrentSandboxes).toBe(20)
    const u = await sb.usage(7)
    expect(u.days).toBe(7)
    expect(last().query.get('days')).toBe('7')
  })
})

describe('sandbox lifecycle', () => {
  it('create defaults to templateName base and exposes the record', async () => {
    const s = await sb.sandboxes.create()
    expect(last().json).toEqual({ templateName: 'base' })
    expect(s.id).toBe('sbx-1')
    expect(s.state).toBe('running')
    expect(s.portUrl(8080)).toBe('https://8080-sbx-1.sandbox.example.test')
    expect(s.trafficAccessToken).toBe('tok')
    expect(s.versions[0].version).toBe(1)
  })

  it('create forwards options and validates metadata values', async () => {
    await sb.sandboxes.create({ templateId: 'tpl-x', timeout: 300, onTimeout: 'kill', metadata: { a: 'b' }, envVars: { X: '1' }, network: { allowInternetAccess: false } })
    expect(last().json).toEqual({ templateId: 'tpl-x', timeout: 300, lifecycle: { onTimeout: 'kill' }, metadata: { a: 'b' }, envVars: { X: '1' }, network: { allowInternetAccess: false } })
    await expect(sb.sandboxes.create({ metadata: { n: 1 as unknown as string } })).rejects.toThrow(/metadata\.n must be a string/)
  })

  it('list / shared / get / kill → 404 afterwards', async () => {
    const rows = await sb.sandboxes.list(['running', 'frozen'])
    expect(last().query.get('state')).toBe('running,frozen')
    expect(rows).toHaveLength(2)
    await sb.sandboxes.list()
    expect(last().query.has('state')).toBe(false)
    expect((await sb.sandboxes.shared())[0].id).toBe('sbx-shared')
    const s = await sb.sandboxes.get('sbx-9')
    expect(s.id).toBe('sbx-9')
    await s.kill()
    await expect(s.refresh()).rejects.toBeInstanceOf(NotFoundError)
    await expect(sb.sandboxes.get('sbx-missing')).rejects.toBeInstanceOf(NotFoundError)
  })

  it('freeze 200 returns the frozen record', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    await s.freeze()
    expect(s.state).toBe('frozen')
    expect(mock.state.requests.filter((r) => r.method === 'POST')).toHaveLength(1)
  })

  it('freeze 202 polls until frozen', async () => {
    mock.state.freezeMode = '202'
    mock.state.freezePolls = 2
    const s = await sb.sandboxes.get('sbx-1')
    await s.freeze({ interval: 0.01 })
    expect(s.state).toBe('frozen')
    expect(mock.state.requests.filter((r) => r.method === 'GET' && r.path.endsWith('/sbx-1')).length).toBeGreaterThanOrEqual(3)
  })

  it('freeze 202 without wait returns freezing; wait timeout throws', async () => {
    mock.state.freezeMode = '202'
    const s = await sb.sandboxes.get('sbx-1')
    await s.freeze({ wait: false })
    expect(s.state).toBe('freezing')
    mock.state.freezePolls = 1000
    await expect(s.waitForState('frozen', { timeout: 0.05, interval: 0.02 })).rejects.toBeInstanceOf(WaitTimeoutError)
  })

  it('already-frozen shape is handled', async () => {
    mock.state.freezeMode = 'already'
    const s = await sb.sandboxes.get('sbx-1')
    await s.freeze()
    expect(s.state).toBe('frozen')
  })

  it('resume / rollback / setTimeout / reset / metrics', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    await s.resume({ timeout: 120 })
    expect(last().json).toEqual({ timeout: 120 })
    await s.rollback({ version: 1 })
    expect(last().json).toEqual({ version: 1 })
    expect(s.metadata.rolledBackTo).toBe('1')
    await s.setTimeout(0)
    expect(last().json).toEqual({ timeout: 0 })
    await s.reset()
    expect(last().path.endsWith('/reset')).toBe(true)
    const m = await s.metrics({ start: 1, end: 2 })
    expect(m[0].cpuUsedPct).toBe(2.5)
    expect(last().query.get('end')).toBe('2')
  })

  it('clone retries 503 with Retry-After', async () => {
    mock.state.cloneRetries = 2
    const s = await sb.sandboxes.get('sbx-1')
    const c = await s.clone({ metadata: { from: 'sbx-1' } })
    expect(c.id).toBe('sbx-clone')
    expect(c.metadata.from).toBe('sbx-1')
    expect(mock.state.requests.filter((r) => r.path.endsWith('/clone'))).toHaveLength(3)
  })

  it('grants on sandboxes', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const g = await s.grant('u2', { perm: 'write', expiresAt: '2027-01-01T00:00:00Z' })
    expect(g.permission).toBe('write')
    expect(last().json).toEqual({ perm: 'write', expires_at: '2027-01-01T00:00:00Z' })
    expect((await s.grants())[0].permission).toBe('write')
    await s.revoke('u2')
    expect(last().method).toBe('DELETE')
  })

  it('id-based shortcuts on sandboxes resource', async () => {
    const s = await sb.sandboxes.freeze('sbx-5')
    expect(s.state).toBe('frozen')
    await sb.sandboxes.kill('sbx-5')
    expect(mock.state.killed.has('sbx-5')).toBe(true)
  })
})

describe('commands', () => {
  it('exec: string → sh -c, non-zero exit is data, 408 throws', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const r = await s.exec('echo hi', { cwd: '/tmp', envVars: { A: 'b' }, user: 'user', timeout: 10 })
    expect(last().json).toEqual({ command: ['/bin/sh', '-c', 'echo hi'], cwd: '/tmp', envVars: { A: 'b' }, user: 'user', timeout: 10 })
    expect(r.exitCode).toBe(0)
    const f = await s.exec(['fail'])
    expect(f.exitCode).toBe(3)
    expect(f.stderr).toBe('boom\n')
    const err = await s.exec(['sleep', '999']).catch((e) => e)
    expect(err).toBeInstanceOf(CommandTimeoutError)
    expect(err.killed).toBe(true)
    await expect(s.exec('x', { timeout: 601 })).rejects.toThrow(RangeError)
  })

  it('execStream yields parsed SSE events', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const events = []
    for await (const e of s.execStream(['echo'])) events.push(e)
    expect(events).toEqual([
      { kind: 'stdout', data: 'hello\n', execId: 'e1', seq: 1 },
      { kind: 'stderr', data: 'warn\n', execId: 'e1', seq: 2 },
      { kind: 'stdout', data: 'multi\nline', execId: 'e1', seq: 3 },
      { kind: 'exit', exitCode: 2, execId: 'e1' },
    ])
    const errs = []
    for await (const e of s.execStream(['sleep'])) errs.push(e)
    expect(errs.at(-1)).toMatchObject({ kind: 'error', data: 'timed out', stage: 'timeout' })
  })

  it('runCode results and error', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const ok = await s.runCode('1+1', { contextId: 'ctx', images: 'omit' })
    expect(last().json).toEqual({ code: '1+1', language: 'python', timeout: 60, contextId: 'ctx', images: 'omit' })
    expect(ok.results[0].text).toBe('2')
    expect(ok.error).toBeNull()
    const bad = await s.runCode('raise ValueError("x")')
    expect(bad.error?.name).toBe('ValueError')
    await expect(s.runCode('x', { timeout: 541 })).rejects.toThrow(RangeError)
  })

  it('background commands', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const bg = await s.commands.start(['cat'])
    expect(last().json).toEqual({ command: ['cat'] })
    expect(bg.pid).toBe(4242)
    expect(await bg.input('abc')).toBe(3)
    const out = []
    for await (const e of bg.connect()) out.push(e)
    expect(out).toEqual([
      { kind: 'stdout', data: 'bg\n', seq: 1 }, { kind: 'stderr', data: 'warn', seq: 2 },
      { kind: 'gap', data: 'output gap — ring evicted unread bytes' }, { kind: 'done', data: 'disconnected' },
    ])
    expect(mock.state.connectFromSeq).toBeNull()
    for await (const _e of bg.connect({ fromSeq: 2 })) { /* drain */ }
    expect(mock.state.connectFromSeq).toBe('2')
    await bg.close()
    expect(await bg.wait()).toBe(7)
    await bg.kill()
    expect(last().json).toEqual({ pid: 4242, signal: 9 })
    expect((await s.commands.list())[0].execId).toBe('bg-1')
    await s.commands.sendStdin(4242, 'x')
    expect(last().json).toEqual({ pid: 4242, data: 'x' })
  })

  it('connect on a reaped command yields nothing', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const bg = await s.commands.start(['x'])
    const gone = Object.create(Object.getPrototypeOf(bg), Object.getOwnPropertyDescriptors(bg))
    Object.defineProperty(gone, 'execId', { value: 'bg-gone' })
    const out = []
    for await (const e of gone.connect()) out.push(e)
    expect(out).toEqual([])
  })
})

describe('files', () => {
  it('bytes round-trip, text helpers, list, missing file', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const bytes = new Uint8Array([0, 255, 10, 13, 200])
    expect(await s.files.write('/tmp/bin', bytes, { user: 'root' })).toBe(5)
    expect(last().headers['content-type']).toBe('application/octet-stream')
    expect(last().query.get('user')).toBe('root')
    expect(Array.from(await s.files.read('/tmp/bin'))).toEqual(Array.from(bytes))
    await s.files.write('/tmp/t.txt', 'héllo')
    expect(await s.files.readText('/tmp/t.txt')).toBe('héllo')
    const ls = await s.files.list('/tmp')
    expect(ls.map((e) => e.name).sort()).toEqual(['bin', 't.txt'])
    await expect(s.files.read('/nope')).rejects.toBeInstanceOf(NotFoundError)
  })
})

describe('pty', () => {
  it('open / input raw / resize / connect / kill', async () => {
    const s = await sb.sandboxes.get('sbx-1')
    const p = await s.pty.open({ rows: 40, cols: 120 })
    expect(last().json).toEqual({ command: ['/bin/bash'], rows: 40, cols: 120 })
    await p.input('ls\n')
    expect(last().body.toString()).toBe('ls\n')
    expect(last().headers['content-type']).toBe('application/octet-stream')
    await p.resize(50, 100)
    const ev = []
    for await (const e of p.connect()) ev.push(e)
    expect(ev).toEqual([{ kind: 'data', data: '$ ' }, { kind: 'exit', exitCode: 0, reason: 'eof' }])
    await p.kill()
  })
})

describe('templates', () => {
  it('build requires image or dockerfile and waitReady polls to ready', async () => {
    await expect(sb.templates.build({ name: 'x' })).rejects.toThrow(TypeError)
    const b = await sb.templates.build({ name: 'n', image: 'ubuntu:24.04', cpuCount: 2 })
    expect(last().json).toEqual({ name: 'n', image: 'ubuntu:24.04', cpuCount: 2 })
    expect(b.templateId).toBe('tpl-new')
    const t = await sb.templates.waitReady(b.templateId, { interval: 0.01 })
    expect(t.status).toBe('ready')
  })

  it('waitReady throws TemplateBuildError on error, WaitTimeoutError on deadline', async () => {
    mock.state.buildOutcome = 'error'
    const e = await sb.templates.waitReady('tpl-new', { interval: 0.01 }).catch((x) => x)
    expect(e).toBeInstanceOf(TemplateBuildError)
    expect(e.buildError).toBe('docker pull failed')
    mock.state.buildPolls = 1000
    await expect(sb.templates.waitReady('tpl-new', { timeout: 0.03, interval: 0.02 })).rejects.toBeInstanceOf(WaitTimeoutError)
  })

  it('list / shared / get / delete / grants', async () => {
    expect((await sb.templates.list())[0].namespace).toBe('public')
    expect((await sb.templates.shared())[0].id).toBe('tpl-shared')
    expect((await sb.templates.get('tpl-base')).cpuCount).toBe(1)
    await sb.templates.delete('tpl-base')
    const g = await sb.templates.grant('tpl-base', 'u2')
    expect(g.permission).toBe('read')
    expect((await sb.templates.grants('tpl-base'))).toHaveLength(1)
    await sb.templates.revoke('tpl-base', 'u2')
  })
})

describe('errors', () => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const hit = (p: string, retry = false): Promise<any> =>
    (sb as unknown as { http: { request: (p: string, o?: { retry?: boolean }) => Promise<unknown> } }).http.request(p, { retry }).catch((e: unknown) => e)

  it('maps every documented status/code', async () => {
    const plan = await hit('/errors/plan')
    expect(plan).toBeInstanceOf(PlanRequiredError)
    expect(plan).toBeInstanceOf(ForbiddenError)
    expect(plan.plan).toBe('free')
    expect(plan.upgrade).toBe('https://x/plans')
    expect(plan.requestId).toBe('req-1')
    const credits = await hit('/errors/credits')
    expect(credits).toBeInstanceOf(InsufficientCreditsError)
    expect(credits.balance).toBe(-3)
    const limit = await hit('/errors/limit')
    expect(limit).toBeInstanceOf(LimitError)
    expect(limit).toBeInstanceOf(ConflictError)
    expect(limit.limit).toBe(20)
    expect(limit.code).toBe('sb_box_concurrency_limit')
    const retired = await hit('/errors/retired')
    expect(retired).toBeInstanceOf(RegionRetiredError)
    expect(retired).toBeInstanceOf(NotFoundError)
    expect(retired.region).toBe('eastus')
    const mem = await hit('/errors/memory')
    expect(mem).toBeInstanceOf(MemoryExpiredError)
    expect(mem.recovery).toEqual(['reset'])
    const disk = await hit('/errors/disk')
    expect(disk).toBeInstanceOf(DiskLostError)
    expect((disk as DiskLostError).recovery).toEqual(['rollback'])
    expect((disk as DiskLostError).resumable).toBe(false)
    const v = await hit('/errors/422')
    expect(v).toBeInstanceOf(ValidationError)
    expect(v.message).toMatch(/validation failed.*timeout/)
    expect(await hit('/errors/interp')).toBeInstanceOf(InterpreterUnavailableError)
    const i502 = await hit('/errors/interp502')
    expect(i502).toBeInstanceOf(InterpreterStartFailedError)
    expect(i502).toBeInstanceOf(ServerError)
  })

  it('redacts keys in messages', async () => {
    const e = await hit('/errors/leak')
    expect(e.message).toBe('bad key gsk-REDACTED rejected')
    expect(redact(`a ${KEY} b`)).toBe('a gsk-REDACTED b')
  })

  it('status-only mapping for codes the mock does not serve', () => {
    const h = new Headers({ 'retry-after': '3' })
    expect(errorFromResponse(499, { detail: 'x' }, h)).toBeInstanceOf(ClientDisconnectedError)
    expect(errorFromResponse(504, 'gateway', h)).toBeInstanceOf(ServerError)
    expect(errorFromResponse(503, {}, h).retryAfter).toBe(3)
    expect(errorFromResponse(418, {}, new Headers())).toBeInstanceOf(SbBoxError)
  })

  it('retries idempotent reads on 503 but not non-idempotent calls', async () => {
    mock.state.flakyGets = 2
    expect(await hit('/errors/flaky', true)).toEqual({ ok: true })
    mock.state.flakyGets = 5
    const e = await hit('/errors/flaky', true)
    expect(e).toBeInstanceOf(ServerError)
    expect(e.status).toBe(503)
  }, 15000)

  it('retries means retries after the first attempt (3 → four tries)', async () => {
    const three = new SbBox({ apiKey: KEY, baseUrl: mock.url, retries: 3 })
    const two = new SbBox({ apiKey: KEY, baseUrl: mock.url, retries: 2 })
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const via = (c: SbBox): Promise<any> =>
      (c as unknown as { http: { request: (p: string, o?: { retry?: boolean }) => Promise<unknown> } }).http.request('/errors/flaky', { retry: true }).catch((e: unknown) => e)
    mock.state.flakyGets = 3
    expect(await via(three)).toEqual({ ok: true })
    mock.state.flakyGets = 3
    const e = await via(two)
    expect(e).toBeInstanceOf(ServerError)
    expect(e.status).toBe(503)
  }, 15000)

  it('a raw stream that never ends is aborted at the deadline', async () => {
    const http = (sb as unknown as { http: Http }).http
    const res = await http.raw('/errors/hang', { headers: { Accept: 'text/event-stream' }, timeout: 0.2 })
    expect(res.status).toBe(200)
    const events: string[] = []
    const err = await (async () => {
      for await (const m of parseSse(res.body!)) events.push(m.event)
    })().catch((x: unknown) => x)
    expect(events).toEqual(['execId'])
    expect(err).toBeInstanceOf(SbBoxConnectionError)
    expect((err as Error).message).toMatch(/stream timed out/)
    mock.state.hangs.forEach((r) => r.end())
    mock.state.hangs.length = 0
  })

  it('bodyDeadline: false leaves an unbounded stream alone past the timeout', async () => {
    const http = (sb as unknown as { http: Http }).http
    const res = await http.raw('/errors/hang', { headers: { Accept: 'text/event-stream' }, timeout: 0.1, bodyDeadline: false })
    const reader = res.body!.getReader()
    await reader.read()
    await new Promise((r) => setTimeout(r, 250))
    const hung = mock.state.hangs.pop()!
    hung.write('event: exit\ndata: {"exitCode":0}\n\n')
    hung.end()
    const rest = await reader.read()
    expect(rest.done).toBe(false)
    expect(new TextDecoder().decode(rest.value)).toContain('exit')
  })

  it('connection errors become SbBoxConnectionError', async () => {
    const dead = new SbBox({ apiKey: KEY, baseUrl: 'http://127.0.0.1:1', retries: 0 })
    await expect(dead.regions()).rejects.toBeInstanceOf(SbBoxConnectionError)
  })

  it('request timeout surfaces as a connection error', async () => {
    const slow = new SbBox({ apiKey: KEY, baseUrl: mock.url, timeout: 0.01, retries: 0, fetch: (u, i) => new Promise((_, rej) => i?.signal?.addEventListener('abort', () => rej(i.signal!.reason))) })
    const e = await slow.regions().catch((x) => x)
    expect(e).toBeInstanceOf(SbBoxConnectionError)
    expect(e.message).toMatch(/timed out/)
  })
})

describe('sse parser', () => {
  it('handles CRLF, comments, multi-line data and a trailing event without blank line', async () => {
    const text = ': ping\r\nevent: stdout\r\nid: 1\r\ndata: a\r\ndata: b\r\n\r\ndata: {"x":1}\nevent: exit'
    const stream = new ReadableStream<Uint8Array>({
      start(c) { c.enqueue(new TextEncoder().encode(text.slice(0, 7))); c.enqueue(new TextEncoder().encode(text.slice(7))); c.close() },
    })
    const out = []
    for await (const m of parseSse(stream)) out.push(m)
    expect(out).toEqual([
      { event: 'stdout', data: 'a\nb', id: '1' },
      { event: 'exit', data: '{"x":1}', id: undefined },
    ])
  })
})
