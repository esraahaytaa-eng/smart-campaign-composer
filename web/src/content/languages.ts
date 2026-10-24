import type { Catalog, Channel, Counts, Lang } from '../campaign/types'
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
