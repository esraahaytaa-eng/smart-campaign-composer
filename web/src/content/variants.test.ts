import { describe, expect, it } from 'vitest'
import { fromContent } from './editing'
import { copyAToB, missingB, toSaveB, variantRows } from './variants'

const both = ['sms', 'email'] as const
const full = { sms: { body: 'B' }, email: { subject: 'S', body: 'E' } }

// Version B is saved per language once complete; incomplete saved text blocks the save, as for A (D68).
describe('toSaveB', () => {
  it('saves the complete versions of the campaign languages', () => {
    expect(toSaveB({ tr: fromContent(full), en: fromContent(null) }, null, [...both], ['tr', 'en'])).toEqual({ tr: full })
  })

  it('leaves out languages the campaign no longer has', () => {
    expect(toSaveB({ tr: fromContent(full), de: fromContent(full) }, null, [...both], ['tr'])).toEqual({ tr: full })
  })

  it('saves nothing while saved text is incomplete', () => {
    expect(toSaveB({ tr: fromContent({ sms: { body: '' } }) }, { tr: full }, [...both], ['tr'])).toBeNull()
  })
})

describe('missingB', () => {
  it('lists languages without a saved version B', () => {
    expect(missingB(['tr', 'en'], { tr: full })).toEqual(['en'])
    expect(missingB(['tr'], null)).toEqual(['tr'])
  })
})

describe('copyAToB', () => {
  it('copies the text, not the object', () => {
    const a = fromContent(full)
    const b = copyAToB(a)
    b.sms.body = 'changed'
    expect(a.sms.body).toBe('B')
  })
})

// The report shows real delivery data only: no rates, no winner (D68).
describe('variantRows', () => {
  it('reads each version from the delivery groups and skip reasons', () => {
    const groups = { sent: 2, uncertain: 0, failed: 1, skipped: 1, cancelled: 0, retrying: 1, pending: 0 }
    const rows = variantRows({
      b: { audience: 3, groups: { ...groups, sent: 1, retrying: 0 }, skip_reasons: { character_limit_exceeded: 1 } },
      a: { audience: 5, groups, skip_reasons: { no_consent: 1 } },
    })
    expect(rows).toEqual([
      { variant: 'a', audience: 5, sent: 2, failed: 1, skipped: 1, tooLong: 0, waiting: 1 },
      { variant: 'b', audience: 3, sent: 1, failed: 1, skipped: 1, tooLong: 1, waiting: 0 },
    ])
  })

  it('is empty without an A/B test', () => {
    expect(variantRows(undefined)).toEqual([])
  })
})
