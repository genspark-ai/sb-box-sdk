export interface SseMessage {
  event: string
  data: string
  id?: string
}

/**
 * Parse a `text/event-stream` body into messages. `event:` defaults to `message`,
 * multi-line `data:` is joined with `\n`, a blank line ends the event.
 */
export async function* parseSse(body: ReadableStream<Uint8Array>): AsyncGenerator<SseMessage> {
  const reader = body.getReader()
  const decoder = new TextDecoder()
  let buf = ''
  let event = ''
  let data: string[] = []
  let id: string | undefined

  const flush = (): SseMessage | undefined => {
    if (!event && data.length === 0) return undefined
    const msg: SseMessage = { event: event || 'message', data: data.join('\n'), id }
    event = ''
    data = []
    id = undefined
    return msg
  }

  try {
    while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      let nl: number
      while ((nl = buf.indexOf('\n')) >= 0) {
        let line = buf.slice(0, nl)
        buf = buf.slice(nl + 1)
        if (line.endsWith('\r')) line = line.slice(0, -1)
        if (line === '') {
          const m = flush()
          if (m) yield m
          continue
        }
        if (line.startsWith(':')) continue
        const colon = line.indexOf(':')
        const field = colon < 0 ? line : line.slice(0, colon)
        let value = colon < 0 ? '' : line.slice(colon + 1)
        if (value.startsWith(' ')) value = value.slice(1)
        if (field === 'event') event = value
        else if (field === 'data') data.push(value)
        else if (field === 'id') id = value
      }
    }
    buf += decoder.decode()
    if (buf.trim()) {
      const colon = buf.indexOf(':')
      const field = colon < 0 ? buf : buf.slice(0, colon)
      const value = colon < 0 ? '' : buf.slice(colon + 1).replace(/^ /, '')
      if (field === 'data') data.push(value)
      else if (field === 'event') event = value
    }
    const m = flush()
    if (m) yield m
  } finally {
    reader.releaseLock()
  }
}

export function parseJsonData<T = unknown>(msg: SseMessage): T | undefined {
  if (!msg.data) return undefined
  try {
    return JSON.parse(msg.data) as T
  } catch {
    return undefined
  }
}
