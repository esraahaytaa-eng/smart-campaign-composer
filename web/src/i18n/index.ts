// The UI's languages (D44): a small typed translator, English by default,
// with the choice kept in localStorage. Only interface text is translated;
// data and AI output reach the page through placeholders, unchanged.
import { readonly, ref } from 'vue'
import { en, type Key } from './en'
import { tr } from './tr'

export type { Key } from './en'
export type Locale = 'en' | 'tr'

// Language names are shown in their own language, whatever the UI language.
export const locales: { code: Locale; name: string }[] = [
  { code: 'en', name: 'English' },
  { code: 'tr', name: 'Türkçe' },
]

const STORAGE_KEY = 'scc.locale'
const dictionaries = { en, tr }
const formats: Record<Locale, string> = { en: 'en-GB', tr: 'tr-TR' } // "1 Oct 2026", "1 Eki 2026"

function stored(): Locale {
  try {
    const value = localStorage.getItem(STORAGE_KEY)
    return value === 'tr' || value === 'en' ? value : 'en'
  } catch {
    return 'en' // storage blocked (private mode, tests): fall back to English
  }
}

const current = ref<Locale>(stored())
export const locale = readonly(current)

function markDocument() {
  if (typeof document !== 'undefined') document.documentElement.lang = current.value
}
markDocument()

export function setLocale(next: Locale) {
  current.value = next
  try {
    localStorage.setItem(STORAGE_KEY, next)
  } catch {
    // not kept across visits, but the page still switches
  }
  markDocument()
}

export type Params = Record<string, string | number>

// t returns the text for key in the current language, with {placeholders}
// filled in once (a value containing "{x}" is left as it is).
export function t(key: Key, params: Params = {}): string {
  const message = dictionaries[current.value][key]
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
  const [y, m, d] = iso.split('-').map(Number)
  return new Intl.DateTimeFormat(formats[current.value], { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' }).format(
    Date.UTC(y ?? 1970, (m ?? 1) - 1, d ?? 1),
  )
}

export const formatOr = (items: string[]) => new Intl.ListFormat(formats[current.value], { type: 'disjunction' }).format(items)
export const formatAnd = (items: string[]) => new Intl.ListFormat(formats[current.value], { type: 'conjunction' }).format(items)

// Upper-cases the first letter by the language's rules (Turkish "i" → "İ").
export const capitalize = (s: string) => s.charAt(0).toLocaleUpperCase(formats[current.value]) + s.slice(1)
