// The UI's languages (D44): a small typed translator, English by default,
// with the choice kept in localStorage. Only interface text is translated;
// data and AI output reach the page through placeholders, unchanged.
import { readonly, ref } from 'vue'
import { en, type Key, type Message } from './en'

export type { Key } from './en'
export type Locale = 'en' | 'tr'

// Language names are shown in their own language, whatever the UI language.
export const locales: { code: Locale; name: string }[] = [
  { code: 'en', name: 'English' },
  { code: 'tr', name: 'Türkçe' },
]

const STORAGE_KEY = 'scc.locale'
// Only English is part of the app; Turkish is fetched when chosen (D58).
const dictionaries: Partial<Record<Locale, Record<Key, Message>>> = { en }
const formats: Record<Locale, string> = { en: 'en-GB', tr: 'tr-TR' } // "1 Oct 2026", "1 Eki 2026"

function stored(): Locale {
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    return value === 'tr' || value === 'en' ? value : 'en'
  } catch {
    return 'en' // storage blocked (private mode, tests): fall back to English
  }
}

const current = ref<Locale>('en')
export const locale = readonly(current)

function markDocument() {
  if (typeof document !== 'undefined') document.documentElement.lang = current.value
}
markDocument()

let requested: Locale = 'en'

// setLocale switches once the language's dictionary is there. If it can't be
// fetched, the page stays in the current language; of two quick choices, the
// last one wins.
export async function setLocale(next: Locale) {
  requested = next
  if (!dictionaries[next]) {
    try {
      dictionaries[next] = (await import('./tr')).tr
    } catch {
      return
    }
  }
  if (requested !== next) return
  current.value = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // not kept across visits, but the page still switches
  }
  markDocument()
}

// The remembered language, loaded before the app first renders (main.ts).
export const localeReady = setLocale(stored())

export type Params = Record<string, string | number>

// isKey tells whether text from the server, such as a reason code, has a
// translation, so unknown codes can be shown as they are.
export const isKey = (k: string): k is Key => k in en

// t returns the text for key in the current language, with {placeholders}
// filled in once (a value containing "{x}" is left as it is).
export function t(key: Key, params: Params = {}): string {
  const message = (dictionaries[current.value] ?? en)[key]
  const text =
    typeof message === 'string'
      ? message
      : message[new Intl.PluralRules(formats[current.value]).select(Number(params.count)) === 'one' ? 'one' : 'other']
  return text.replace(/\{(\w+)\}/g, (whole, name: string) => (name in params ? String(params[name]) : whole))
}

export const formatNumber = (n: number) => new Intl.NumberFormat(formats[current.value]).format(n)

// Amounts show cents only when there are any: "10,000" but "499.50".
export const formatAmount = (n: number) =>
  new Intl.NumberFormat(formats[current.value], { minimumFractionDigits: Number.isInteger(n) ? 0 : 2, maximumFractionDigits: 2 }).format(n)

// "2026-10-01" → "1 Oct 2026" / "1 Eki 2026". Read as UTC so the day never shifts.
export function formatDate(iso: string): string {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(iso)) return '—' // a rule without its date: show a gap, not 1900
  const [y, m, d] = iso.split('-').map(Number)
  return new Intl.DateTimeFormat(formats[current.value], { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' }).format(
    Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1),
  )
}

// An instant in the browser's zone, with the zone: "23 Oct 2026, 10:00 GMT+3".
export const formatDateTime = (iso: string) =>
  new Intl.DateTimeFormat(formats[current.value], {
    day: 'numeric',
    month: 'short',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    timeZoneName: 'short',
  }).format(new Date(iso))

// "Thursday, 22 October 2026" / "22 Ekim 2026 Perşembe".
export const formatLongDate = (d: Date) =>
  new Intl.DateTimeFormat(formats[current.value], { weekday: 'long', day: 'numeric', month: 'long', year: 'numeric' }).format(d)

// An instant's day in the browser's zone: "22 Oct 2026".
export const formatDay = (iso: string) =>
  new Intl.DateTimeFormat(formats[current.value], { day: 'numeric', month: 'short', year: 'numeric' }).format(new Date(iso))

// A clock time with seconds: "19:58:41".
export const formatTime = (d: Date) =>
  new Intl.DateTimeFormat(formats[current.value], { hour: '2-digit', minute: '2-digit', second: '2-digit' }).format(d)

// 82.1 → "82.1%" / "%82,1".
export const formatPercent = (n: number) =>
  new Intl.NumberFormat(formats[current.value], { style: 'percent', maximumFractionDigits: 1 }).format(n / 100)

export const formatOr = (items: string[]) => new Intl.ListFormat(formats[current.value], { type: 'disjunction' }).format(items)
export const formatAnd = (items: string[]) => new Intl.ListFormat(formats[current.value], { type: 'conjunction' }).format(items)

// Upper-cases the first letter by the language's rules (Turkish "i" → "İ").
export const capitalize = (s: string) => s.charAt(0).toLocaleUpperCase(formats[current.value]) + s.slice(1)
