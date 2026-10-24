import { describe, expect, it } from 'vitest'
import { generationOutcome, hasUnsavedChanges, languagesToReplace, toSave, type Draft } from './editing'

const empty: Draft = { sms: { body: '' }, email: { subject: '', body: '' } }
const typed: Draft = { sms: { body: 'Merhaba {{first_name}}' }, email: { subject: '', body: '' } }
const hello: Draft = { sms: { body: 'Hello {{first_name}}' }, email: { subject: '', body: '' } }

// T17 review 1 and D66: the AI never replaces text without asking, saved or
// not; languages without text are written without a question.
describe('languagesToReplace', () => {
  it('names ticked languages that have saved text', () => {
    expect(languagesToReplace(['tr', 'en'], { tr: empty, en: empty }, { tr: { sms: { body: 'x' } } }, ['sms'])).toEqual(['tr'])
  })

  it('names ticked languages with text typed but not saved yet', () => {
    expect(languagesToReplace(['tr', 'en'], { tr: typed, en: empty }, null, ['sms', 'email'])).toEqual(['tr'])
  })

  it('is empty when every ticked language is empty', () => {
    expect(languagesToReplace(['en'], { tr: typed, en: empty }, { tr: { sms: { body: 'x' } } }, ['sms'])).toEqual([])
  })

  it('ignores text in a channel that is not chosen', () => {
    expect(languagesToReplace(['tr'], { tr: typed }, null, ['email'])).toEqual([])
  })
})

// T17 review 2: what differs from the saved content must be saved, or the
// marketer warned, before leaving.
describe('hasUnsavedChanges', () => {
  const saved = { tr: { sms: { body: 'Merhaba {{first_name}}' } } }

  it('is false when the editors match what is saved', () => {
    expect(hasUnsavedChanges({ tr: typed }, saved, ['sms'])).toBe(false)
  })

  it('is true after an edit in any language', () => {
    expect(hasUnsavedChanges({ tr: { ...typed, sms: { body: 'Merhaba!' } } }, saved, ['sms'])).toBe(true)
  })

  it('is true for a language written by hand and not saved yet', () => {
    expect(hasUnsavedChanges({ tr: typed, en: hello }, saved, ['sms'])).toBe(true)
  })

  it('is false for an added language still empty', () => {
    expect(hasUnsavedChanges({ tr: typed, en: empty }, saved, ['sms'])).toBe(false)
  })

  it('is true after removing a saved language', () => {
    expect(hasUnsavedChanges({}, saved, ['sms'])).toBe(true)
  })

  it('ignores kept text of a channel that is not chosen (D67)', () => {
    const kept = { tr: { sms: { body: 'Merhaba {{first_name}}' }, email: { subject: 'Konu', body: 'Gövde' } } }
    expect(hasUnsavedChanges({ tr: typed }, kept, ['sms'])).toBe(false)
  })
})

// What a save sends: every saved language, plus added languages once they
// are complete, so an empty new tab never blocks saving the others.
describe('toSave', () => {
  const saved = { tr: { sms: { body: 'x' } } }

  it('sends the saved languages and complete new ones', () => {
    expect(toSave({ tr: typed, en: hello }, saved, ['sms'], 'tr')).toEqual({ tr: { sms: { body: typed.sms.body } }, en: { sms: { body: hello.sms.body } } })
  })

  it('leaves out an added language that is not complete yet', () => {
    expect(toSave({ tr: typed, en: empty }, saved, ['sms'], 'tr')).toEqual({ tr: { sms: { body: typed.sms.body } } })
  })

  it('is null when a saved language is incomplete: nothing can be saved', () => {
    expect(toSave({ tr: empty }, saved, ['sms'], 'tr')).toBeNull()
  })

  it('is null until the default language is complete: every message has a fallback (D64)', () => {
    expect(toSave({ tr: empty, en: hello }, null, ['sms'], 'tr')).toBeNull()
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
