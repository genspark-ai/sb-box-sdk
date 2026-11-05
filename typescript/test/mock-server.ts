import http from 'node:http'
import type { AddressInfo } from 'node:net'

export const KEY = 'gsk-test-0123456789abcdef'

export interface Recorded {
  method: string
  path: string
  query: URLSearchParams
  headers: http.IncomingHttpHeaders
  body: Buffer
  json?: unknown
}

export interface MockState {
  requests: Recorded[]
  freezeMode: '200' | '202' | 'already'
  freezePolls: number
  cloneRetries: number
  flakyGets: number
  /** Open SSE responses of `/errors/hang` (headers sent, never ended). */
  hangs: http.ServerResponse[]
  buildPolls: number
  buildOutcome: 'ready' | 'error'
  files: Map<string, Buffer>
  killed: Set<string>
  /** `from_seq` query of the last background-command connect (null = none). */
  connectFromSeq: string | null
}

function sandboxRecord(id: string, state = 'running') {
  return {
    id, templateId: 'tpl-base', state, region: 'westus2',
    portUrl: `https://{port}-${id}.sandbox.example.test`, domain: 'sandbox.example.test',
    createdAt: '2026-11-04T00:00:00Z', cpuCount: 1, memoryMB: 2048, diskSizeGB: 20, idleTimeout: 600,
    versions: [{ version: 1, frozenAt: '2026-11-04T01:00:00Z', label: 'v1' }],
    metadata: { team: 'sdk' },
  }
}

function send(res: http.ServerResponse, status: number, body: unknown, headers: Record<string, string> = {}) {
  const text = typeof body === 'string' ? body : JSON.stringify(body)
  res.writeHead(status, { 'content-type': typeof body === 'string' ? 'text/plain' : 'application/json', 'x-request-id': 'req-1', ...headers })
  res.end(text)
}

function sse(res: http.ServerResponse, events: Array<[string, unknown, string?]>) {
  res.writeHead(200, { 'content-type': 'text/event-stream' })
  for (const [ev, data, id] of events) {
    if (id) res.write(`id: ${id}\n`)
    res.write(`event: ${ev}\ndata: ${JSON.stringify(data)}\n\n`)
  }
  res.end()
}

export async function startMock(): Promise<{ url: string; state: MockState; close: () => Promise<void> }> {
  const state: MockState = {
    requests: [], freezeMode: '200', freezePolls: 0, cloneRetries: 0, flakyGets: 0, hangs: [], connectFromSeq: null,
    buildPolls: 2, buildOutcome: 'ready', files: new Map(), killed: new Set(),
  }
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c) => chunks.push(c))
    req.on('end', () => {
      const body = Buffer.concat(chunks)
      const u = new URL(req.url ?? '/', 'http://x')
      const rec: Recorded = { method: req.method ?? '', path: u.pathname, query: u.searchParams, headers: req.headers, body }
      if ((req.headers['content-type'] ?? '').includes('json') && body.length) {
        try { rec.json = JSON.parse(body.toString()) } catch { /* ignore */ }
      }
      state.requests.push(rec)
      try {
        route(rec, res, state)
      } catch (e) {
        send(res, 500, { detail: String(e) })
      }
    })
  })
  await new Promise<void>((r) => server.listen(0, '127.0.0.1', r))
  const port = (server.address() as AddressInfo).port
  return {
    url: `http://127.0.0.1:${port}/api/sb-box/v1`,
    state,
    close: () => { for (const h of state.hangs) h.destroy(); state.hangs.length = 0; return new Promise((r) => server.close(() => r())) },
  }
}

function route(req: Recorded, res: http.ServerResponse, st: MockState) {
  const key = req.headers['x-api-key'] ?? (req.headers.authorization ?? '').replace(/^Bearer /, '')
  if (key !== KEY) return send(res, 401, { detail: 'invalid API key' })
  const p = req.path.replace(/^\/api\/sb-box\/v1/, '')
  const m = req.method
  const j = (req.json ?? {}) as Record<string, unknown>

  if (m === 'GET' && p === '/regions') return send(res, 200, { regions: ['westus2'], default: 'westus2' })
  if (m === 'GET' && p === '/limits') {
    return send(res, 200, { plan: 'plus', planRequired: false, limits: { maxConcurrentSandboxes: 20, maxCpu: 4, maxMemoryMB: 8192, maxDiskGB: 50, maxConcurrentBuilds: 2 }, usage: { runningSandboxes: 1, buildingTemplates: 0 } })
  }
  if (m === 'GET' && p === '/usage') return send(res, 200, { days: Number(req.query.get('days')), from: '20261001', to: '20261104', direct: { credits: 12.5 }, daily: [] })

  // Error zoo
  if (p === '/errors/plan') return send(res, 403, { detail: { code: 'sb_box_plan_required', plan: 'free', message: 'upgrade', upgrade: 'https://x/plans' } })
  if (p === '/errors/credits') return send(res, 402, { detail: { code: 'sb_box_insufficient_credits', balance: -3, message: 'no credits' } })
  if (p === '/errors/limit') return send(res, 409, { detail: { code: 'sb_box_concurrency_limit', plan: 'plus', limit: 20, current: 20, message: 'too many' } })
  if (p === '/errors/retired') return send(res, 404, { detail: { code: 'sb_box_region_retired', region: 'eastus', message: 'gone' } })
  if (p === '/errors/memory') return send(res, 428, { reason: 'memory_expired', recovery: ['reset'] })
  if (p === '/errors/disk') return send(res, 410, { error: 'disk lost', reason: 'disk_lost', recovery: ['rollback'], resumable: false })
  if (p === '/errors/422') return send(res, 422, { status: -2, message: 'validation failed', errors: [{ loc: ['body', 'timeout'], msg: 'must be int' }] })
  if (p === '/errors/leak') return send(res, 400, { detail: `bad key ${KEY} rejected` })
  if (p === '/errors/interp') return send(res, 409, { error: 'no interpreter', code: 'sb_box_interpreter_unavailable' })
  if (p === '/errors/interp502') return send(res, 502, { error: 'start failed', code: 'sb_box_interpreter_start_failed' })
  if (p === '/errors/hang') {
    res.writeHead(200, { 'content-type': 'text/event-stream' })
    res.write('event: execId\ndata: {"execId":"e-hang"}\n\n')
    st.hangs.push(res)
    return
  }
  if (p === '/errors/flaky') {
    if (st.flakyGets > 0) { st.flakyGets--; return send(res, 503, { detail: 'busy' }) }
    return send(res, 200, { ok: true })
  }

  // Templates
  if (m === 'POST' && p === '/templates/build') return send(res, 200, { templateId: 'tpl-new', status: 'building', region: 'westus2', owner: 'me' })
  if (m === 'GET' && p === '/templates') return send(res, 200, { templates: [{ id: 'tpl-base', name: 'base', namespace: 'public', status: 'ready', can_create: true }] })
  if (m === 'GET' && p === '/templates/shared') return send(res, 200, [{ id: 'tpl-shared', name: 's', namespace: 'private', status: 'ready' }])
  if (m === 'GET' && p === '/templates/tpl-new') {
    if (st.buildPolls > 0) { st.buildPolls--; return send(res, 200, { id: 'tpl-new', name: 'n', namespace: 'private', status: 'building' }) }
    return st.buildOutcome === 'ready'
      ? send(res, 200, { id: 'tpl-new', name: 'n', namespace: 'private', status: 'ready' })
      : send(res, 200, { id: 'tpl-new', name: 'n', namespace: 'private', status: 'error', buildError: 'docker pull failed' })
  }
  if (m === 'GET' && p === '/templates/tpl-base') return send(res, 200, { id: 'tpl-base', name: 'base', namespace: 'public', status: 'ready', cpuCount: 1 })
  if (m === 'DELETE' && p === '/templates/tpl-base') return send(res, 200, { status: 'deleted' })
  const tg = p.match(/^\/templates\/([^/]+)\/grants(?:\/([^/]+))?$/)
  if (tg) {
    if (m === 'PUT') return send(res, 200, { template_id: tg[1], grantee_id: tg[2], permission: j.perm, granted_at: 'now', expires_at: j.expires_at ?? null })
    if (m === 'GET') return send(res, 200, [{ grantee_id: 'u2', permission: 'read' }])
    if (m === 'DELETE') return send(res, 200, { status: 'revoked' })
  }

  // Sandboxes
  if (m === 'POST' && p === '/sandboxes') {
    if (j.templateName === 'nope') return send(res, 404, { detail: 'template not found' })
    return send(res, 200, { ...sandboxRecord('sbx-1'), trafficAccessToken: 'tok' })
  }
  if (m === 'GET' && p === '/sandboxes') {
    return send(res, 200, { sandboxes: [{ id: 'sbx-1', template_id: 'tpl-base', state: 'running' }, { id: 'sbx-2', template_id: 'tpl-base', state: 'frozen' }], hasMore: false })
  }
  if (m === 'GET' && p === '/sandboxes/shared') return send(res, 200, [sandboxRecord('sbx-shared')])

  const sm = p.match(/^\/sandboxes\/([^/]+)(\/.*)?$/)
  if (!sm) return send(res, 404, { detail: 'Not Found' })
  const id = sm[1]
  const rest = sm[2] ?? ''
  if (st.killed.has(id) || id === 'sbx-missing') return send(res, 404, { detail: 'sandbox not found' })

  if (rest === '') {
    if (m === 'GET') {
      if (st.freezePolls > 0) { st.freezePolls--; return send(res, 200, sandboxRecord(id, 'freezing')) }
      return send(res, 200, sandboxRecord(id, st.freezeMode === '202' ? 'frozen' : 'running'))
    }
    if (m === 'DELETE') { st.killed.add(id); return send(res, 200, { status: 'killed' }) }
  }
  if (rest === '/freeze') {
    if (st.freezeMode === '200') return send(res, 200, sandboxRecord(id, 'frozen'))
    if (st.freezeMode === 'already') return send(res, 200, { state: 'frozen', status: 'already frozen' })
    return send(res, 202, { state: 'freezing', id })
  }
  if (rest === '/resume') return send(res, 200, { ...sandboxRecord(id, 'running'), timeoutAt: j.timeout ? 'later' : undefined })
  if (rest === '/rollback') return send(res, 200, { ...sandboxRecord(id), metadata: { rolledBackTo: String(j.version) } })
  if (rest === '/clone') {
    if (st.cloneRetries > 0) { st.cloneRetries--; return send(res, 503, { detail: 'clone still starting' }, { 'retry-after': '0' }) }
    return send(res, 200, { ...sandboxRecord('sbx-clone'), metadata: (j.metadata as Record<string, string>) ?? {} })
  }
  if (rest === '/timeout') return send(res, 200, { status: 'ok' })
  if (rest === '/reset') return send(res, 200, sandboxRecord(id))
  if (rest === '/metrics') return send(res, 200, [{ timestamp: 't', timestampUnix: 1, cpuCount: 1, cpuUsedPct: 2.5, memUsed: 1, memTotal: 2, diskUsed: 3, diskTotal: 4 }])
  const sg = rest.match(/^\/grants(?:\/([^/]+))?$/)
  if (sg) {
    if (m === 'PUT') return send(res, 200, { sandbox_id: id, grantee_id: sg[1], permission: j.perm })
    if (m === 'GET') return send(res, 200, [{ grantee_id: 'u2', permission: 'write' }])
    if (m === 'DELETE') return send(res, 200, { status: 'revoked' })
  }

  if (rest === '/exec') {
    const cmd = j.command as string[]
    if (cmd.includes('sleep')) return send(res, 408, { error: 'command timed out', stage: 'timeout', killed: true, sandbox_id: id })
    if (cmd.includes('fail')) return send(res, 200, { stdout: '', stderr: 'boom\n', exitCode: 3 })
    return send(res, 200, { stdout: cmd.join(' ') + '\n', stderr: '', exitCode: 0 })
  }
  if (rest === '/sse/exec') {
    const cmd = j.command as string[]
    if (cmd.includes('sleep')) return sse(res, [['execId', { execId: 'e1' }], ['error', { error: 'timed out', stage: 'timeout' }]])
    return sse(res, [['execId', { execId: 'e1' }], ['stdout', { data: 'hello\n' }, '1'], ['stderr', { data: 'warn\n' }, '2'], ['stdout', { data: 'multi\nline' }, '3'], ['exit', { exitCode: 2 }]])
  }
  if (rest === '/run-code') {
    if ((j.code as string).includes('raise')) {
      return send(res, 200, { stdout: '', stderr: '', executionCount: 2, complete: true, error: { name: 'ValueError', value: 'x', traceback: ['tb'] }, results: [] })
    }
    return send(res, 200, { stdout: 'out\n', stderr: '', executionCount: 1, complete: true, error: null, results: [{ text: '2', isMainResult: true }] })
  }
  if (rest === '/commands/start') return send(res, 200, { execId: 'bg-1', pid: 4242 })
  if (rest === '/commands' && m === 'GET') return send(res, 200, [{ execId: 'bg-1', pid: 4242, sandboxId: id }])
  if (rest === '/commands/bg-1/input') return send(res, 200, { written: (j.data as string).length })
  if (rest === '/commands/bg-1/close') return send(res, 200, { status: 'ok' })
  if (rest === '/commands/bg-1/wait') return send(res, 200, { exitCode: 7 })
  if (rest === '/commands/bg-1/connect') {
    st.connectFromSeq = req.query.get('from_seq')
    return sse(res, [['stdout', { data: 'bg\n' }, '1'], ['stderr', { data: 'warn' }, '2'], ['gap', { error: 'output gap — ring evicted unread bytes' }], ['done', { status: 'disconnected' }]])
  }
  if (rest === '/commands/bg-gone/connect') return send(res, 404, { detail: 'command not found' })
  if (rest === '/commands/kill') return send(res, 200, { status: 'ok' })
  if (rest === '/commands/send-stdin') return send(res, 200, { status: 'ok' })

  if (rest === '/files/stream') {
    const path = req.query.get('path') ?? ''
    if (m === 'POST') { st.files.set(path, req.body); return send(res, 200, { written: req.body.length }) }
    const f = st.files.get(path)
    if (!f) return send(res, 404, { detail: 'file not found' })
    res.writeHead(200, { 'content-type': 'application/octet-stream' })
    return res.end(f)
  }
  if (rest === '/files/list') {
    return send(res, 200, [...st.files.keys()].map((k) => ({ name: k.split('/').pop(), path: k, isDir: false, size: st.files.get(k)!.length })))
  }

  if (rest === '/pty' && m === 'POST') return send(res, 200, { execId: 'pty-1', pid: 99 })
  if (rest === '/pty/pty-1/input') return send(res, 200, { status: 'ok' })
  if (rest === '/pty/pty-1/resize') return send(res, 200, { status: 'ok' })
  if (rest === '/pty/pty-1/kill') return send(res, 200, { status: 'ok' })
  if (rest === '/pty/pty-1/connect') return sse(res, [['data', { data: '$ ' }], ['exit', { reason: 'eof', exitCode: 0 }]])

  return send(res, 404, { detail: 'Not Found' })
}
