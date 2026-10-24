// Decisions that protect the marketer's text on the content step (T17
// review): ask before the AI replaces anything, know what isn't saved yet,
// and never quietly adopt a newer version after an unfinished generation.
import type { Channel, Content, Lang, Variants } from '../campaign/types'

export type Draft = { sms: { body: string }; email: { subject: string; body: string } }

// The chosen channels' fields, as the API saves them.
export function chosenContent(d: Draft, channels: Channel[]): Content {
  return {
    ...(channels.includes('sms') ? { sms: { body: d.sms.body } } : {}),
    ...(channels.includes('email') ? { email: { subject: d.email.subject, body: d.email.body } } : {}),
  }
}

const hasText = (d: Draft, channels: Channel[]) =>
  (channels.includes('sms') && d.sms.body.trim() !== '') ||
  (channels.includes('email') && (d.email.subject.trim() !== '' || d.email.body.trim() !== ''))

// Every chosen channel filled in: the only text the API saves.
export const isComplete = (d: Draft, channels: Channel[]) =>
  (!channels.includes('sms') || d.sms.body.trim() !== '') &&
  (!channels.includes('email') || (d.email.subject.trim() !== '' && d.email.body.trim() !== ''))

// The ticked languages the AI would replace: saved text or text typed but not
// saved yet. The marketer confirms before those are written again (D66).
export function languagesToReplace(ticked: Lang[], drafts: Record<Lang, Draft>, saved: Variants | null, channels: Channel[]): Lang[] {
  return ticked.filter((l) => saved?.[l] !== undefined || (drafts[l] !== undefined && hasText(drafts[l], channels)))
}

function changed(l: Lang, d: Draft, saved: Variants | null, channels: Channel[]): boolean {
  const s = saved?.[l]
  if (s === undefined) return hasText(d, channels)
  return JSON.stringify(chosenContent(d, channels)) !== JSON.stringify(chosenContent(fromContent(s), channels))
}

export function hasUnsavedChanges(drafts: Record<Lang, Draft>, saved: Variants | null, channels: Channel[]): boolean {
  const removed = Object.keys(saved ?? {}).some((l) => drafts[l] === undefined)
  return removed || Object.entries(drafts).some(([l, d]) => changed(l, d, saved, channels))
}

// What a save sends: every saved language, plus added languages once they
// are complete. null when a saved language is incomplete: nothing can be saved.
// The default language must be among them: everyone else falls back to it (D64).
export function toSave(drafts: Record<Lang, Draft>, saved: Variants | null, channels: Channel[], def: Lang): Variants | null {
  const out: Variants = {}
  for (const [l, d] of Object.entries(drafts)) {
    if (isComplete(d, channels)) out[l] = chosenContent(d, channels)
    else if (saved?.[l] !== undefined) return null
  }
  return out[def] ? out : null
}

export function fromVariants(v: Variants | null): Record<Lang, Draft> {
  return Object.fromEntries(Object.entries(v ?? {}).map(([l, c]) => [l, fromContent(c)]))
}

export function fromContent(c: Content | null): Draft {
  return { sms: { body: c?.sms?.body ?? '' }, email: { subject: c?.email?.subject ?? '', body: c?.email?.body ?? '' } }
}

export type GenerationOutcome = 'done' | 'stopped' | 'failed' | 'conflict'

// After the stream ends, the refetched version says what happened. Unless the
// AI finished, a changed version means someone else saved meanwhile (or the
// AI's save raced Stop): the page must reload, not adopt it silently.
export function generationOutcome(r: { finished: boolean; aborted: boolean; sentVersion: number; version: number }): GenerationOutcome {
  if (r.finished) return 'done'
  if (r.version !== r.sentVersion) return 'conflict'
  return r.aborted ? 'stopped' : 'failed'
}
