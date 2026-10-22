import { describe, expect, it } from 'vitest'
import { approvalBlockers, type ApprovalState } from './approval'

const ready: ApprovalState = {
  channels: ['sms', 'email'],
  content: { sms: { body: 'Merhaba {{first_name}}' }, email: { subject: 'Konu', body: 'Merhaba' } },
  audience: 12,
  when: 'now',
  at: null,
  reviewed: true,
  scheduleProblem: null,
}

// Sending always needs an explicit choice and a confirmation (D9, D29).
describe('approvalBlockers', () => {
  it('is empty when everything is in place', () => {
    expect(approvalBlockers(ready)).toEqual([])
  })

  it.each([
    [{ when: null }, 'chooseWhen'],
    [{ reviewed: false }, 'confirmReview'],
    [{ audience: 0 }, 'noAudience'],
    [{ content: { sms: { body: 'x' } } }, 'missingEmail'],
    [{ content: { email: { subject: 's', body: 'b' } } }, 'missingSms'],
    [{ content: { sms: { body: '  ' }, email: { subject: 's', body: 'b' } } }, 'missingSms'],
    [{ when: 'later', at: null, scheduleProblem: 'invalid' }, 'scheduleInvalid'],
    [{ when: 'later', at: '2026-10-22T12:00:00Z', scheduleProblem: 'tooSoon' }, 'scheduleTooSoon'],
  ])('%j blocks with %s', (patch, blocker) => {
    expect(approvalBlockers({ ...ready, ...patch } as ApprovalState)).toContain(blocker)
  })

  it('needs only the chosen channel', () => {
    expect(approvalBlockers({ ...ready, channels: ['sms'], content: { sms: { body: 'x' } } })).toEqual([])
  })
})
