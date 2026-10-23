import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { contentWasStale, markContentFresh, markContentStale } from './stale'

const store = new Map<string, string>()
beforeEach(() => {
  store.clear()
  vi.stubGlobal('sessionStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => store.set(k, v),
    removeItem: (k: string) => store.delete(k),
  })
})
afterEach(() => vi.unstubAllGlobals())

// D40: after re-interpreting, the content step warns until the messages are
// saved or rewritten.
describe('stale content flag', () => {
  it('is set per campaign and cleared when the content is fresh again', () => {
    markContentStale(7)
    expect(contentWasStale(7)).toBe(true)
    expect(contentWasStale(8)).toBe(false)
    markContentFresh(7)
    expect(contentWasStale(7)).toBe(false)
  })

  it('works without storage', () => {
    vi.stubGlobal('sessionStorage', undefined)
    expect(() => markContentStale(7)).not.toThrow()
    expect(contentWasStale(7)).toBe(false)
  })
})
