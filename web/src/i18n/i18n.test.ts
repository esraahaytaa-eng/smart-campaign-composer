import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { en } from './en'
import { tr } from './tr'

let i18n: typeof import('./index')
const store = new Map<string, string>()

beforeEach(async () => {
  vi.resetModules()
  store.clear()
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => store.set(k, v),
  })
  i18n = await import('./index')
})
afterEach(() => vi.unstubAllGlobals())

const placeholders = (s: unknown) => (JSON.stringify(s).match(/\{\w+\}/g) ?? []).sort().join(' ')

describe('dictionaries', () => {
  it('give every English text a Turkish one with the same placeholders', () => {
    expect(Object.keys(tr).sort()).toEqual(Object.keys(en).sort())
    for (const key of Object.keys(en) as (keyof typeof en)[]) {
      expect(placeholders(tr[key]), key).toBe(placeholders(en[key]))
      expect(JSON.stringify(tr[key]).length, `${key} is empty`).toBeGreaterThan(2)
    }
  })
})

// "Taslak" is the draft (a status). The brief needs its own word, or "delete
// the draft" and "re-interpret the brief" read the same (T16 i18n review).
describe('Turkish terminology', () => {
  it('never calls the brief "taslak"', () => {
    for (const key of Object.keys(en) as (keyof typeof en)[]) {
      const english = JSON.stringify(en[key])
      const turkish = JSON.stringify(tr[key]).toLocaleLowerCase('tr')
      if (!/\bbrief\b/i.test(english)) continue
      if (/\bdraft\b/i.test(english)) expect(turkish, key).toMatch(/açıklama/) // mentions both: the brief has its own word
      else expect(turkish, key).not.toMatch(/taslak|taslağ/)
    }
  })

  it('still calls the draft "taslak"', () => {
    expect(tr['status.draft']).toBe('Taslak')
    expect(tr['confirm.delete']).toMatch(/^Taslak/)
  })
})

describe('t', () => {
  it('is English by default', () => {
    expect(i18n.locale.value).toBe('en')
    expect(i18n.t('auth.signIn')).toBe('Sign in')
  })

  it('switches language and remembers it', async () => {
    i18n.setLocale('tr')
    expect(i18n.t('auth.signIn')).toBe('Giriş yap')
    expect(store.get('scc.locale')).toBe('tr')

    vi.resetModules()
    const again = await import('./index')
    expect(again.locale.value).toBe('tr')
  })

  it('ignores a stored value it does not know', async () => {
    store.set('scc.locale', 'de')
    vi.resetModules()
    expect((await import('./index')).locale.value).toBe('en')
  })

  it('works when storage is unavailable', async () => {
    vi.stubGlobal('localStorage', {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    })
    vi.resetModules()
    const blocked = await import('./index')
    expect(blocked.locale.value).toBe('en')
    expect(() => blocked.setLocale('tr')).not.toThrow()
    expect(blocked.locale.value).toBe('tr')
  })

  it('fills in placeholders, leaving the values as they are', () => {
    expect(i18n.t('composer.count', { count: 12, max: 1000 })).toBe('12 / 1000')
  })

  it('picks the plural form', () => {
    expect(i18n.t('rules.orders', { count: 1 })).toBe('1 order')
    expect(i18n.t('rules.orders', { count: 3 })).toBe('3 orders')
    i18n.setLocale('tr')
    expect(i18n.t('rules.orders', { count: 3 })).toBe('3 sipariş')
  })
})

describe('formatting', () => {
  it('formats numbers, amounts and dates for the language', () => {
    expect(i18n.formatNumber(10000)).toBe('10,000')
    expect(i18n.formatAmount(499.5)).toBe('499.50')
    expect(i18n.formatDate('2026-10-01')).toBe('1 Oct 2026')
    i18n.setLocale('tr')
    expect(i18n.formatNumber(10000)).toBe('10.000')
    expect(i18n.formatAmount(499.5)).toBe('499,50')
    expect(i18n.formatDate('2026-10-01')).toBe('1 Eki 2026')
  })

  it('shows a gap for a missing date, not 1900', () => {
    expect(i18n.formatDate('')).toBe('—')
  })

  it('never shifts a date across time zones', () => {
    expect(i18n.formatDate('2026-01-01')).toBe('1 Jan 2026')
  })

  it('joins choices with "or" in each language', () => {
    expect(i18n.formatOr(['Ankara', 'İstanbul', 'İzmir'])).toBe('Ankara, İstanbul or İzmir')
    i18n.setLocale('tr')
    expect(i18n.formatOr(['Ankara', 'İstanbul', 'İzmir'])).toBe('Ankara, İstanbul veya İzmir')
  })
})
