// npx tsx examples/quickstart.ts   (SBBOX_API_KEY must be set)
import { SbBox } from '@genspark/sb-box'

const sbbox = new SbBox()

const sandbox = await sbbox.sandboxes.create({ timeout: 600, metadata: { purpose: 'quickstart' } })
console.log('created', sandbox.id, 'in', sandbox.region)

const r = await sandbox.exec('uname -a && python3 --version')
console.log(r.stdout.trim(), '(exit', r.exitCode + ')')

await sandbox.files.write('/home/user/notes.txt', 'written from the SDK\n')
console.log(await sandbox.files.readText('/home/user/notes.txt'))

for await (const ev of sandbox.execStream(['sh', '-c', 'for i in 1 2 3; do echo tick $i; sleep 0.2; done'])) {
  if (ev.kind === 'stdout') process.stdout.write(ev.data ?? '')
}

const code = await sandbox.runCode('x = 21\nx * 2')
console.log('run-code →', code.results[0])

console.log('web server would be reachable at', sandbox.portUrl(8080))

await sandbox.freeze()
console.log('frozen at', sandbox.frozenAt)
await sandbox.resume()
console.log('resumed, state =', sandbox.state)

await sandbox.kill()
console.log('killed')
