import { describe, expect, it } from 'vitest'
import { createSSEParser, type SSEEvent } from './sse'

function parse(chunks: string[]): SSEEvent[] {
  const events: SSEEvent[] = []
  const p = createSSEParser((e) => events.push(e))
  chunks.forEach((c) => p.push(c))
  p.end()
  return events
}

const stream = 'event: delta\ndata: {"field":"sms.body","text":"Merhaba"}\n\nevent: done\ndata: {"content":{},"version":3}\n\n'

describe('createSSEParser', () => {
  it('reads named events with JSON data', () => {
    expect(parse([stream])).toEqual([
      { event: 'delta', data: { field: 'sms.body', text: 'Merhaba' } },
      { event: 'done', data: { content: {}, version: 3 } },
    ])
  })

  it('gives the same events however the stream is split', () => {
    const whole = parse([stream])
    for (let i = 1; i < stream.length; i++) expect(parse([stream.slice(0, i), stream.slice(i)]), `split at ${i}`).toEqual(whole)
    expect(parse(stream.split(''))).toEqual(whole)
  })

  it('accepts CRLF line endings and joins multi-line data', () => {
    expect(parse(['event: retry\r\ndata: {"errors":\r\ndata: ["a"]}\r\n\r\n'])).toEqual([{ event: 'retry', data: { errors: ['a'] } }])
  })

  it('ignores comments and events without data', () => {
    expect(parse([': keep-alive\n\nevent: ping\n\n'])).toEqual([])
  })

  it('reports data that is not JSON as an error event', () => {
    expect(parse(['event: delta\ndata: {oops\n\n'])).toEqual([{ event: 'error', data: { message: 'malformed event data' } }])
  })

  it('drops an unfinished event at the end', () => {
    expect(parse(['event: delta\ndata: {"text":"x"}'])).toEqual([])
  })
})
