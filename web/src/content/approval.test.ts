import { describe, expect, it } from 'vitest'
import { approvalBlockers, type ApprovalState } from './approval'

const both = { sms: { body: 'Merhaba {{first_name}}' }, email: { subject: 'Konu', body: 'Merhaba' } }
const ok = { approved_at: '2026-10-24T10:00:00Z', outdated: false }
const ready: ApprovalState = {
  channels: ['sms', 'email'],
  content: { tr: both, en: both },
  defaultLanguage: 'tr',
  languages: { tr: ok, en: ok },
  audience: 12,
  when: 'now',
  at: null,
  reviewed: true,
  scheduleProblem: null,
}

// Sending always needs an explicit choice and a confirmation (D9, D29), and
// every language approved by the marketer (D66).
describe('approvalBlockers', () => {
  it('is empty when everything is in place', () => {
    expect(approvalBlockers(ready)).toEqual([])
  })

  it.each([
    [{ when: null }, 'chooseWhen'],
    [{ reviewed: false }, 'confirmReview'],
    [{ audience: 0 }, 'noAudience'],
    [{ content: { tr: both, en: { sms: { body: 'x' } } } }, 'missingEmail'],
    [{ content: { tr: { email: { subject: 's', body: 'b' } }, en: both } }, 'missingSms'],
    [{ content: { tr: { sms: { body: '  ' }, email: { subject: 's', body: 'b' } }, en: both } }, 'missingSms'],
    [{ content: null, defaultLanguage: null, languages: {} }, 'missingSms'],
    [{ defaultLanguage: null }, 'missingDefault'],
    [{ defaultLanguage: 'de' }, 'missingDefault'],
    [{ languages: { tr: ok, en: { approved_at: null, outdated: false } } }, 'unapprovedLanguage'],
    [{ languages: { tr: ok, en: { approved_at: null, outdated: true } } }, 'unapprovedLanguage'],
    [{ when: 'later', at: null, scheduleProblem: 'invalid' }, 'scheduleInvalid'],
    [{ when: 'later', at: '2026-10-22T12:00:00Z', scheduleProblem: 'tooSoon' }, 'scheduleTooSoon'],
  ])('%j blocks with %s', (patch, blocker) => {
    expect(approvalBlockers({ ...ready, ...patch } as ApprovalState)).toContain(blocker)
  })

  it('needs only the chosen channel; kept text of another channel is ignored (D67)', () => {
    expect(approvalBlockers({ ...ready, channels: ['sms'], content: { tr: { sms: { body: 'x' }, email: { subject: '', body: '' } } }, languages: { tr: ok } })).toEqual([])
  })
})

// D68: an A/B test needs version B in every language; without a test it is ignored.
describe('approvalBlockers with an A/B test', () => {
  it('waits for version B in every language', () => {
    expect(approvalBlockers({ ...ready, abTest: true, contentB: { tr: both } })).toEqual(['missingVersionB'])
    expect(approvalBlockers({ ...ready, abTest: true, contentB: { tr: both, en: both } })).toEqual([])
  })

  it('ignores kept version B text while the test is off', () => {
    expect(approvalBlockers({ ...ready, abTest: false, contentB: {} })).toEqual([])
  })
})
