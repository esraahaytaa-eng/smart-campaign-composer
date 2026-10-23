// Decisions that protect the marketer's text on the content step (T17
// review): ask before the AI replaces anything, know what isn't saved yet,
// and never quietly adopt a newer version after an unfinished generation.
import type { Channel, Content } from '../campaign/types'

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

// The AI's text replaces both saved content and text typed but not saved yet.
export function needsOverwriteConfirm(d: Draft, saved: Content | null, channels: Channel[]): boolean {
  return saved !== null || hasText(d, channels)
}

export function hasUnsavedChanges(d: Draft, saved: Content | null, channels: Channel[]): boolean {
  if (saved === null) return hasText(d, channels)
  return JSON.stringify(chosenContent(d, channels)) !== JSON.stringify(chosenContent(fromContent(saved), channels))
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
