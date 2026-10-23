import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Snapshot } from './status'
import { watchStatus } from './stream'

// A stand-in for the browser's EventSource.
class FakeSource {
  static last: FakeSource
  listeners: Record<string, ((e: MessageEvent) => void)[]> = {}
  onerror: (() => void) | null = null
  readyState = 0
  closed = false
  constructor(public url: string) {
    FakeSource.last = this
  }
  addEventListener(name: string, fn: (e: MessageEvent) => void) {
    ;(this.listeners[name] ??= []).push(fn)
  }
  close() {
    this.closed = true
    this.readyState = 2
  }
  emit(name: string, data: unknown) {
    for (const fn of this.listeners[name] ?? []) fn(new MessageEvent(name, { data: JSON.stringify(data) }))
  }
}

describe('watchStatus', () => {
  const mocks = () => ({ onStatus: vi.fn<(s: Snapshot) => void>(), onEnd: vi.fn<() => void>(), onTrouble: vi.fn<(r: boolean) => void>() })
  let handlers: ReturnType<typeof mocks>
  beforeEach(() => {
    handlers = mocks()
  })
  const open = () => watchStatus(7, handlers, FakeSource as unknown as typeof EventSource)

  it("opens the campaign's stream and passes each snapshot on", () => {
    open()
    expect(FakeSource.last.url).toBe('/api/campaigns/7/events')
    FakeSource.last.emit('status', { status: 'sending' })
    expect(handlers.onStatus).toHaveBeenCalledWith({ status: 'sending' })
  })

  it('closes on end, so the browser does not reconnect (D36)', () => {
    open()
    FakeSource.last.emit('end', {})
    expect(FakeSource.last.closed).toBe(true)
    expect(handlers.onEnd).toHaveBeenCalledOnce()
  })

  it('reports trouble while the browser reconnects, and recovery with the next snapshot', () => {
    open()
    FakeSource.last.onerror!()
    expect(handlers.onTrouble).toHaveBeenCalledWith(true)
    FakeSource.last.emit('status', { status: 'sending' })
    expect(handlers.onTrouble).toHaveBeenLastCalledWith(false)
  })

  it('ignores a snapshot that is not JSON', () => {
    open()
    for (const fn of FakeSource.last.listeners.status!) fn(new MessageEvent('status', { data: 'nope' }))
    expect(handlers.onStatus).not.toHaveBeenCalled()
  })

  it('can be closed by the page', () => {
    const w = open()
    w.close()
    expect(FakeSource.last.closed).toBe(true)
  })
})
