// The live status stream's snapshot (D16, D36) as the screens show it: seven
// groups that add up to the audience (D46), estimate vs actual, resend offers.
import type { Channel, Status } from '../campaign/types'

export const GROUPS = ['sent', 'uncertain', 'failed', 'skipped', 'cancelled', 'retrying', 'pending'] as const
export type Group = (typeof GROUPS)[number]

export type ChannelStatus = {
  audience: number
  groups: Record<Group, number>
  reasons: Partial<Record<Group, Record<string, number>>>
  retries: number
  rate_limited: number
  rate_limited_recent: number
  skip_reasons: Record<string, number>
  resendable: number
  active_resend_id: number | null
}

export type Snapshot = {
  status: Status
  send_at: string | null
  audience_built_at: string | null
  completed_at: string | null
  approval_estimate: { total: number; sms: number; email: number } | null
  channels: Partial<Record<Channel, ChannelStatus>>
}

export type Phase = 'scheduled' | 'sending' | 'completed' | 'cancelled' | 'cancelledBeforeSending'

export function deliveryPhase(s: Snapshot): Phase {
  if (s.status === 'cancelled') return s.audience_built_at ? 'cancelled' : 'cancelledBeforeSending'
  if (s.status === 'completed') return 'completed'
  return s.audience_built_at ? 'sending' : 'scheduled'
}

const CHANNELS: Channel[] = ['sms', 'email']
// Bar order: outcomes first, then what is still moving; pending is the empty rest.
const SEGMENTS: Group[] = ['sent', 'failed', 'skipped', 'uncertain', 'cancelled', 'retrying']
const FINAL: Group[] = ['sent', 'uncertain', 'failed', 'skipped', 'cancelled']

const sum = (g: Record<Group, number>, keys: Group[]) => keys.reduce((n, k) => n + g[k], 0)
const pct = (part: number, whole: number) => (whole > 0 ? (part / whole) * 100 : 0)

export function deliverySummary(s: Snapshot) {
  const started = s.audience_built_at !== null
  const channels = CHANNELS.flatMap((channel) => {
    const c = s.channels[channel]
    if (!c) return []
    const finished = sum(c.groups, FINAL)
    return [{
      channel,
      status: c,
      estimate: s.approval_estimate?.[channel] ?? null,
      actual: started ? c.audience : null,
      finished,
      finishedPct: pct(finished, c.audience),
      segments: SEGMENTS.filter((g) => c.groups[g] > 0).map((group) => ({ group, count: c.groups[group] })),
    }]
  })
  const total = (keys: Group[]) => channels.reduce((n, c) => n + sum(c.status.groups, keys), 0)
  const messages = channels.reduce((n, c) => n + c.status.audience, 0)
  const finished = total(FINAL)
  const est = s.approval_estimate
  return {
    channels,
    total: messages,
    sent: total(['sent']),
    notSent: total(['failed', 'skipped', 'cancelled']),
    uncertain: total(['uncertain']),
    inProgress: total(['retrying', 'pending']),
    pending: total(['pending']),
    retrying: total(['retrying']),
    finished,
    finishedPct: pct(finished, messages),
    // Per channel, as messages: the estimate's total counts people, not messages.
    estimate: est && { sms: est.sms, email: est.email, messages: channels.reduce((n, c) => n + (est[c.channel] ?? 0), 0) },
    actual: started ? { sms: s.channels.sms?.audience ?? 0, email: s.channels.email?.audience ?? 0, messages } : null,
  }
}

export type ResendOffer =
  | { state: 'none' }
  | { state: 'waiting'; skipped: number } // known once sending finishes
  | { state: 'available'; skipped: number; count: number }
  | { state: 'active'; skipped: number; resendId: number }
  | { state: 'nobodyLeft'; skipped: number } // every skipped recipient is excluded (consent, uncertain, sent)

// resendOffer: a resend reaches the recipients skipped for the character
// limit on one channel, once the campaign has finished (D31, D48).
export function resendOffer(s: Snapshot, channel: Channel): ResendOffer {
  const c = s.channels[channel]
  const skipped = c?.skip_reasons.character_limit_exceeded ?? 0
  if (!c || skipped === 0) return { state: 'none' }
  if (s.status !== 'completed' && s.status !== 'cancelled') return { state: 'waiting', skipped }
  if (c.active_resend_id !== null) return { state: 'active', skipped, resendId: c.active_resend_id }
  if (c.resendable > 0) return { state: 'available', skipped, count: c.resendable }
  return { state: 'nobodyLeft', skipped }
}

export type Section = 'sent' | 'inProgress' | 'notSent' | 'unknown'

// The design's rows per channel: sent, in progress, not sent, unknown.
const ROWS: [Section, Group][] = [
  ['sent', 'sent'],
  ['inProgress', 'pending'],
  ['inProgress', 'retrying'],
  ['notSent', 'failed'],
  ['notSent', 'skipped'],
  ['notSent', 'cancelled'],
  ['unknown', 'uncertain'],
]

export function channelRows(c: ChannelStatus) {
  return ROWS.map(([section, group]) => ({
    section,
    group,
    count: c.groups[group],
    reasons: Object.entries(c.reasons[group] ?? {})
      .map(([code, count]) => ({ code, count }))
      .sort((a, b) => b.count - a.count || a.code.localeCompare(b.code)),
  }))
}
