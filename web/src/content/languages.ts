import type { Campaign, Catalog, Channel, Counts, Lang } from '../campaign/types'
import { LEGACY } from '../campaign/types'
import { t } from '../i18n'

// Language names come from the catalog, in their own language (D64).
export function languageName(code: Lang, catalog: Catalog['languages']): string {
  if (code === LEGACY) return t('language.legacy')
  return catalog.find((l) => l.code === code)?.name ?? code
}

// Tabs and lists: the default first, then catalog order, legacy text last.
export function orderLanguages(langs: Lang[], def: Lang | null, catalog: Catalog['languages']): Lang[] {
  const rank = (l: Lang) => {
    const i = catalog.findIndex((c) => c.code === l)
    return l === def ? -1 : i === -1 ? catalog.length : i
  }
  return [...langs].sort((a, b) => rank(a) - rank(b))
}

// The audience step's rows: each language its audience speaks, in catalog
// order, counted on the chosen channels (null when a channel isn't chosen),
// and whether it is suggested. A suggestion only, never a restriction (D64).
export function audienceByLanguage(counts: Counts | undefined, channels: Channel[], recommended: Lang[], catalog: Catalog['languages']) {
  const by = counts?.by_language ?? {}
  return orderLanguages(Object.keys(by), null, catalog)
    .filter((l) => by[l]!.total > 0)
    .map((l) => ({
      code: l,
      name: languageName(l, catalog),
      total: by[l]!.total,
      share: Math.round((by[l]!.total / counts!.total) * 100),
      sms: channels.includes('sms') ? by[l]!.sms : null,
      email: channels.includes('email') ? by[l]!.email : null,
      recommended: recommended.includes(l),
    }))
}

// The default before one is saved: the saved one, the audience's suggestion,
// then the first catalog language. An empty audience suggests '' (D64).
export function startingDefault(saved: Lang | null | undefined, suggested: Lang | undefined, catalog: Catalog['languages']): Lang {
  return saved || suggested || catalog[0]?.code || 'tr'
}

// What "Write with AI" writes: ticked languages without saved text. Legacy
// text gets its language set first; the AI can't write next to it (D64).
export function toWrite(tabs: Lang[], saved: Partial<Record<Lang, unknown>> | null | undefined): Lang[] {
  if (tabs.includes(LEGACY)) return []
  return tabs.filter((l) => !saved?.[l])
}

// One-step creation (D71): the languages chosen on the audience step, from
// the URL: catalog codes only, once each, in catalog order.
export function chosenLanguages(raw: unknown, catalog: Catalog['languages']): Lang[] {
  if (typeof raw !== 'string') return []
  const codes = raw.split(',')
  return catalog.map((l) => l.code).filter((l) => codes.includes(l))
}

// Only a new draft with no text at all may have the AI write it first.
export function writesFirst(c: Pick<Campaign, 'status' | 'content' | 'parent_campaign_id' | 'content_b'>): boolean {
  return c.status === 'draft' && !c.parent_campaign_id && !Object.keys(c.content ?? {}).length && !Object.keys(c.content_b ?? {}).length
}

// What the content step writes by itself on arrival: nothing unless asked
// with write=ai, for such a draft, with at least one chosen language.
export function autoWrite(query: Record<string, unknown>, c: Parameters<typeof writesFirst>[0], catalog: Catalog['languages']): Lang[] {
  if (query.write !== 'ai' || !writesFirst(c)) return []
  return chosenLanguages(query.languages, catalog)
}

// The default among the chosen languages: the suggested one if chosen.
export function chosenDefault(chosen: Lang[], suggested: Lang | undefined): Lang | undefined {
  return !chosen.length || (suggested && chosen.includes(suggested)) ? suggested : chosen[0]
}
