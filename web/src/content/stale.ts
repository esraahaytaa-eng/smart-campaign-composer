// D40, D66: after the brief is re-interpreted, the existing messages may no
// longer match it. The server keeps each language's review state, so the
// warning survives reloads and shows in every tab.
import type { Lang, LanguageState } from '../campaign/types'

export function outdatedLanguages(languages: Record<Lang, LanguageState> | undefined): Lang[] {
  return Object.entries(languages ?? {})
    .filter(([, s]) => s.outdated)
    .map(([l]) => l)
}

export function needsReview(languages: Record<Lang, LanguageState> | undefined): Lang[] {
  return Object.entries(languages ?? {})
    .filter(([, s]) => !s.approved_at)
    .map(([l]) => l)
}
