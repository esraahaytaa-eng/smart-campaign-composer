// An A/B test's version B on the content step and its delivery report (D68).
import type { Channel, Lang, Variants } from '../campaign/types'
import { chosenContent, isComplete, type Draft } from './editing'

// What a save sends as version B: the complete versions of the campaign's
// languages. null when a saved version B is incomplete: nothing can be saved.
export function toSaveB(drafts: Record<Lang, Draft>, saved: Variants | null, channels: Channel[], langs: Lang[]): Variants | null {
  const out: Variants = {}
  for (const l of langs) {
    const d = drafts[l]
    if (d && isComplete(d, channels)) out[l] = chosenContent(d, channels)
    else if (saved?.[l] !== undefined) return null
  }
  return out
}

// The languages that still need a saved version B before approval.
export const missingB = (langs: Lang[], saved: Variants | null): Lang[] => langs.filter((l) => saved?.[l] === undefined)

export const copyAToB = (a: Draft): Draft => ({ sms: { ...a.sms }, email: { ...a.email } })

export type VariantStatus = { audience: number; groups: Record<string, number>; skip_reasons: Record<string, number> }
export type VariantRow = { variant: 'a' | 'b'; audience: number; sent: number; failed: number; skipped: number; tooLong: number; waiting: number }

// One row per version from the real delivery counts; nothing is derived
// beyond sums of them (no rates, no winner).
export function variantRows(by: Partial<Record<'a' | 'b', VariantStatus>> | undefined): VariantRow[] {
  return (['a', 'b'] as const)
    .filter((v) => by?.[v])
    .map((v) => {
      const s = by![v]!
      return {
        variant: v,
        audience: s.audience,
        sent: s.groups.sent ?? 0,
        failed: s.groups.failed ?? 0,
        skipped: s.groups.skipped ?? 0,
        tooLong: s.skip_reasons.character_limit_exceeded ?? 0,
        waiting: (s.groups.pending ?? 0) + (s.groups.retrying ?? 0),
      }
    })
}
