import type { Catalog, Lang } from '../campaign/types'
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
