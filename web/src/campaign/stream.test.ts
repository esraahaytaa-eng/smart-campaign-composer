import { afterEach, describe, expect, it, vi } from 'vitest'
import { setUnauthorizedHandler } from '../api'
import type { SSEEvent } from '../content/sse'
import { streamContent } from './client'

const enc = new TextEncoder()

function streamOf(parts: Uint8Array[]) {
  return new ReadableStream<Uint8Array>({
    start(c) {
      parts.forEach((p) => c.enqueue(p))
      c.close()
    },
  })
}

afterEach(() => {
  vi.unstubAllGlobals()
  setUnauthorizedHandler(() => {})
})

describe('streamContent', () => {
  it('posts the version and delivers events, even when a character is split across chunks', async () => {
    const body = enc.encode('event: delta\ndata: {"field":"sms.body","text":"İndirim"}\n\nevent: done\ndata: {"version":3}\n\n')
    const cut = body.indexOf(0xc4) + 1 // inside the two bytes of "İ"
    const fetch = vi.fn(async () => new Response(streamOf([body.slice(0, cut), body.slice(cut)]), { headers: { 'Content-Type': 'text/event-stream' } }))
    vi.stubGlobal('fetch', fetch)
    const events: SSEEvent[] = []

    await streamContent(7, 2, (e) => events.push(e), new AbortController().signal)

    expect(fetch).toHaveBeenCalledWith('/api/campaigns/7/content', expect.objectContaining({ method: 'POST', body: '{"version":2}' }))
    expect(events).toEqual([
      { event: 'delta', data: { field: 'sms.body', text: 'İndirim' } },
      { event: 'done', data: { version: 3 } },
    ])
  })

  it('turns an HTTP error into an ApiError', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ error: 'the campaign changed since you loaded it' }, { status: 409 })))
    await expect(streamContent(7, 1, () => {}, new AbortController().signal)).rejects.toMatchObject({ status: 409 })
  })

  it('reports an expired session', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Response.json({ error: 'login required' }, { status: 401 })))
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)
    await expect(streamContent(7, 1, () => {}, new AbortController().signal)).rejects.toMatchObject({ status: 401 })
    expect(unauthorized).toHaveBeenCalledOnce()
  })

  it('reports a network failure as status 0', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))))
    await expect(streamContent(7, 1, () => {}, new AbortController().signal)).rejects.toMatchObject({ status: 0 })
  })

  it('lets a stop (abort) through as an AbortError', async () => {
    const ctrl = new AbortController()
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new DOMException('aborted', 'AbortError'))))
    ctrl.abort()
    await expect(streamContent(7, 1, () => {}, ctrl.signal)).rejects.toMatchObject({ name: 'AbortError' })
  })
})
