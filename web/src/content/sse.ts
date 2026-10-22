// A text/event-stream parser for fetch() streams. The content endpoint is a
// POST (it needs the draft's version), which EventSource can't send.
export type SSEEvent = { event: string; data: unknown }

export function createSSEParser(emit: (e: SSEEvent) => void) {
  let buffer = ''
  let name = ''
  let data: string[] = []

  function line(raw: string) {
    const l = raw.endsWith('\r') ? raw.slice(0, -1) : raw
    if (l === '') {
      if (data.length) {
        try {
          emit({ event: name || 'message', data: JSON.parse(data.join('\n')) })
        } catch {
          emit({ event: 'error', data: { message: 'malformed event data' } }) // i18n-ignore: internal; the UI shows its own text
        }
      }
      name = ''
      data = []
    } else if (l.startsWith('event:')) name = l.slice(6).trim()
    else if (l.startsWith('data:')) data.push(l.slice(5).replace(/^ /, ''))
    // ":" comments and unknown fields are ignored
  }

  return {
    push(chunk: string) {
      buffer += chunk
      let i: number
      while ((i = buffer.indexOf('\n')) >= 0) {
        line(buffer.slice(0, i))
        buffer = buffer.slice(i + 1)
      }
    },
    end() {
      buffer = '' // an event without its closing blank line never completed
      name = ''
      data = []
    },
  }
}
