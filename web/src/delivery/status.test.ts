import { describe, expect, it } from 'vitest'
import { deliveryPhase, deliverySummary, resendOffer, type ChannelStatus, type Snapshot } from './status'

type ChannelSummary = ReturnType<typeof deliverySummary>['channels'][number]

const groups = (g: Partial<ChannelStatus['groups']> = {}): ChannelStatus['groups'] => ({
  sent: 0, uncertain: 0, failed: 0, skipped: 0, cancelled: 0, retrying: 0, pending: 0, ...g,
})

const channel = (g: Partial<ChannelStatus['groups']>, extra: Partial<ChannelStatus> = {}): ChannelStatus => {
  const gs = groups(g)
  return {
    audience: Object.values(gs).reduce((a, b) => a + b, 0),
    groups: gs,
    reasons: {},
    retries: 0,
    rate_limited: 0,
    rate_limited_recent: 0,
    skip_reasons: {},
    resendable: 0,
    active_resend_id: null,
    ...extra,
  }
}

const snapshot = (s: Partial<Snapshot> = {}): Snapshot => ({
  status: 'sending',
  send_at: '2026-10-23T10:00:00Z',
  audience_built_at: '2026-10-23T10:00:01Z',
  completed_at: null,
  approval_estimate: { total: 90, sms: 50, email: 45 },
  channels: {
    sms: channel({ sent: 30, failed: 2, skipped: 5, uncertain: 1, retrying: 3, pending: 10 }, { skip_reasons: { character_limit_exceeded: 4, no_consent: 1 } }),
    email: channel({ sent: 40, pending: 4 }),
  },
  ...s,
})

describe('deliveryPhase', () => {
  it.each([
    [{ status: 'approved', audience_built_at: null }, 'scheduled'],
    [{ status: 'sending' }, 'sending'],
    [{ status: 'completed' }, 'completed'],
    [{ status: 'cancelled' }, 'cancelled'],
    [{ status: 'cancelled', audience_built_at: null }, 'cancelledBeforeSending'],
  ] as const)('%j is %s', (patch, phase) => {
    expect(deliveryPhase(snapshot(patch as Partial<Snapshot>))).toBe(phase)
  })
})

describe('deliverySummary', () => {
  it('adds up the groups across channels', () => {
    const s = deliverySummary(snapshot())
    expect(s.total).toBe(95)
    expect(s.sent).toBe(70)
    expect(s.notSent).toBe(7) // failed + skipped + cancelled
    expect(s.uncertain).toBe(1)
    expect(s.inProgress).toBe(17) // retrying + pending
    expect(s.finished).toBe(78)
    expect(s.finishedPct).toBeCloseTo(82.1, 1)
  })

  it('compares the approval estimate with the actual messages per channel', () => {
    const s = deliverySummary(snapshot())
    expect(s.estimate).toEqual({ sms: 50, email: 45, messages: 95 })
    expect(s.actual).toEqual({ sms: 51, email: 44, messages: 95 })
    expect(s.channels.map((c) => [c.channel, c.estimate, c.actual])).toEqual([['sms', 50, 51], ['email', 45, 44]])
  })

  it('lists channels in a fixed order with processed share and bar segments', () => {
    const [sms, email] = deliverySummary(snapshot()).channels as [ChannelSummary, ChannelSummary]
    expect(sms.channel).toBe('sms')
    expect(sms.finished).toBe(38)
    expect(sms.finishedPct).toBeCloseTo(74.5, 1)
    expect(sms.segments).toEqual([
      { group: 'sent', count: 30 },
      { group: 'failed', count: 2 },
      { group: 'skipped', count: 5 },
      { group: 'uncertain', count: 1 },
      { group: 'retrying', count: 3 },
    ])
    expect(email.segments).toEqual([{ group: 'sent', count: 40 }])
  })

  it('has no actual numbers before sending starts', () => {
    const s = deliverySummary(snapshot({ status: 'approved', audience_built_at: null, channels: { sms: channel({}), email: channel({}) } }))
    expect(s.actual).toBeNull()
    expect(s.total).toBe(0)
    expect(s.finishedPct).toBe(0)
    expect(s.channels[0]!.finishedPct).toBe(0)
  })

  it('works without an approval estimate', () => {
    expect(deliverySummary(snapshot({ approval_estimate: null })).estimate).toBeNull()
  })
})

describe('resendOffer', () => {
  it('waits while the campaign is still sending', () => {
    expect(resendOffer(snapshot(), 'sms')).toEqual({ state: 'waiting', skipped: 4 })
  })

  it('offers the resendable count once finished', () => {
    const s = snapshot({ status: 'completed' })
    s.channels.sms!.resendable = 3
    expect(resendOffer(s, 'sms')).toEqual({ state: 'available', skipped: 4, count: 3 })
  })

  it('also after a cancel', () => {
    const s = snapshot({ status: 'cancelled' })
    s.channels.sms!.resendable = 3
    expect(resendOffer(s, 'sms').state).toBe('available')
  })

  it('points to an active resend instead of offering a second one', () => {
    const s = snapshot({ status: 'completed' })
    s.channels.sms!.resendable = 3
    s.channels.sms!.active_resend_id = 42
    expect(resendOffer(s, 'sms')).toEqual({ state: 'active', skipped: 4, resendId: 42 })
  })

  it('says nobody is left when every skipped recipient is excluded', () => {
    expect(resendOffer(snapshot({ status: 'completed' }), 'sms')).toEqual({ state: 'nobodyLeft', skipped: 4 })
  })

  it('is not offered without length skips or for a channel not in the campaign', () => {
    expect(resendOffer(snapshot({ status: 'completed' }), 'email')).toEqual({ state: 'none' })
    const s = snapshot({ status: 'completed' })
    delete s.channels.email
    expect(resendOffer(s, 'email')).toEqual({ state: 'none' })
  })
})
