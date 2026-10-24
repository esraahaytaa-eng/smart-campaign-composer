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
