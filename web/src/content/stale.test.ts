import { describe, expect, it } from 'vitest'
import { needsReview, outdatedLanguages } from './stale'

const ok = { approved_at: '2026-10-24T10:00:00Z', outdated: false }

// D66: the server keeps each language's review state, so the warning survives
// reloads and other tabs.
describe('outdatedLanguages', () => {
  it('lists the languages written for an earlier brief', () => {
    expect(outdatedLanguages({ tr: { approved_at: null, outdated: true }, en: ok })).toEqual(['tr'])
  })

  it('is empty without languages', () => {
    expect(outdatedLanguages(undefined)).toEqual([])
  })
})

describe('needsReview', () => {
  it('lists the languages not approved yet', () => {
    expect(needsReview({ tr: ok, en: { approved_at: null, outdated: false } })).toEqual(['en'])
  })
})
