// End-to-end smoke against a real environment:
//   SBBOX_API_KEY=… [SBBOX_BASE_URL=…] npx tsx examples/smoke.ts
// Creates one sandbox from the public base, exercises the main surface, kills it.
import { SbBox, SbBoxError } from '@genspark/sb-box'

const redact = (s: string) => s.replace(/gsk-[A-Za-z0-9._-]+/g, 'gsk-REDACTED')
const log = (...a: unknown[]) => console.log(redact(a.map((x) => (typeof x === 'string' ? x : JSON.stringify(x))).join(' ')))
const t0 = Date.now()
const step = (name: string) => log(`[${((Date.now() - t0) / 1000).toFixed(1)}s] ${name}`)

const sbbox = new SbBox()
step(`base ${sbbox.baseUrl}`)
log('regions', await sbbox.regions())
const lim = await sbbox.limits()
log('plan', lim.plan, 'running', lim.usage.runningSandboxes, '/', lim.limits.maxConcurrentSandboxes)

const sandbox = await sbbox.sandboxes.create({ timeout: 900, metadata: { smoke: 'typescript' } })
step(`created ${sandbox.id} state=${sandbox.state}`)
let failed = false
try {
  const echo = await sandbox.exec('echo smoke-ok')
  if (echo.stdout.trim() !== 'smoke-ok' || echo.exitCode !== 0) throw new Error(`exec mismatch: ${JSON.stringify(echo)}`)
  step('exec ok')

  const bytes = new Uint8Array(1024).map((_, i) => i % 251)
  await sandbox.files.write('/tmp/smoke.bin', bytes)
  const back = await sandbox.files.read('/tmp/smoke.bin')
  if (back.length !== 1024 || back.some((b, i) => b !== bytes[i])) throw new Error('file round-trip mismatch')
  const ls = await sandbox.files.list('/tmp')
  if (!ls.some((e) => e.name === 'smoke.bin')) throw new Error('file not listed')
  step('files ok')

  const stream: string[] = []
  let exit = -1
  for await (const ev of sandbox.execStream(['sh', '-c', 'echo a; echo b >&2; exit 5'])) {
    if (ev.kind === 'stdout' || ev.kind === 'stderr') stream.push(`${ev.kind}:${ev.data?.trim()}`)
    if (ev.kind === 'exit') exit = ev.exitCode ?? -1
  }
  if (exit !== 5) throw new Error(`sse exit ${exit}, events ${stream}`)
  step(`sse ok ${stream.join(',')}`)

  const code = await sandbox.runCode('1+1')
  if (code.error) throw new Error(`run-code error ${JSON.stringify(code.error)}`)
  step(`run-code ok ${JSON.stringify(code.results[0])}`)

  const bg = await sandbox.commands.start(['sh', '-c', 'sleep 1; echo done'])
  const code2 = await bg.wait()
  step(`background command exit ${code2}`)

  await sandbox.freeze()
  step(`frozen state=${sandbox.state}`)
  await sandbox.resume()
  step(`resumed state=${sandbox.state}`)
  const again = await sandbox.exec('cat /tmp/smoke.bin | wc -c')
  if (again.stdout.trim() !== '1024') throw new Error(`file lost across freeze: ${again.stdout}`)
  step('post-resume exec ok')

  const metrics = await sandbox.metrics()
  step(`metrics points=${metrics.length}`)
} catch (e) {
  failed = true
  if (e instanceof SbBoxError) log('FAILED', e.name, e.status, e.code ?? '', e.message)
  else log('FAILED', String(e))
} finally {
  await sandbox.kill()
  step('killed')
  try {
    await sandbox.refresh()
    log('FAILED: killed sandbox still visible')
    failed = true
  } catch (e) {
    if (e instanceof SbBoxError && e.status === 404) step('killed sandbox is 404 ✓')
    else { log('FAILED unexpected after kill', String(e)); failed = true }
  }
}
process.exit(failed ? 1 : 0)
