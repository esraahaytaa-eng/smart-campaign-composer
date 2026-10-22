import { describe, expect, it } from 'vitest'
import { generationOutcome, hasUnsavedChanges, needsOverwriteConfirm, type Draft } from './editing'

const empty: Draft = { sms: { body: '' }, email: { subject: '', body: '' } }
const typed: Draft = { sms: { body: 'Merhaba {{first_name}}' }, email: { subject: '', body: '' } }

// T17 review 1: the AI must never replace text without asking, saved or not.
describe('needsOverwriteConfirm', () => {
  it('asks when saved content exists', () => {
    expect(needsOverwriteConfirm(empty, { sms: { body: 'x' } }, ['sms'])).toBe(true)
  })

  it('asks when the marketer has typed text that is not saved yet', () => {
    expect(needsOverwriteConfirm(typed, null, ['sms', 'email'])).toBe(true)
  })

  it('does not ask when every chosen field is empty', () => {
    expect(needsOverwriteConfirm(empty, null, ['sms', 'email'])).toBe(false)
  })

  it('ignores text in a channel that is not chosen', () => {
    expect(needsOverwriteConfirm(typed, null, ['email'])).toBe(false)
  })
})

// T17 review 2: what differs from the saved content must be saved, or the
// marketer warned, before leaving.
describe('hasUnsavedChanges', () => {
  const saved = { sms: { body: 'Merhaba {{first_name}}' } }

  it('is false when the editors match what is saved', () => {
    expect(hasUnsavedChanges(typed, saved, ['sms'])).toBe(false)
  })

  it('is true after an edit', () => {
    expect(hasUnsavedChanges({ ...typed, sms: { body: 'Merhaba!' } }, saved, ['sms'])).toBe(true)
  })

  it('is true for text typed before anything was saved', () => {
    expect(hasUnsavedChanges(typed, null, ['sms'])).toBe(true)
  })

  it('is false for empty editors and nothing saved', () => {
    expect(hasUnsavedChanges(empty, null, ['sms', 'email'])).toBe(false)
  })
})

// T17 review 3: a generation that did not finish must not quietly adopt a
// newer version: that is another edit (or a save we can't tell apart from one).
describe('generationOutcome', () => {
  it.each([
    [{ finished: true, aborted: false, sentVersion: 3, version: 4 }, 'done'],
    [{ finished: false, aborted: true, sentVersion: 3, version: 3 }, 'stopped'],
    [{ finished: false, aborted: false, sentVersion: 3, version: 3 }, 'failed'],
    [{ finished: false, aborted: true, sentVersion: 3, version: 4 }, 'conflict'],
    [{ finished: false, aborted: false, sentVersion: 3, version: 5 }, 'conflict'],
  ] as const)('%j → %s', (input, outcome) => {
    expect(generationOutcome(input)).toBe(outcome)
  })
})
