// Recipient details (D47): the drawer's filters live in the URL query, so
// Back closes it and a link reopens it.
import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import type { Channel } from '../campaign/types'
import { GROUPS, type Group } from './status'

export type RecipientsQuery = { group: Group; channel: Channel | 'all'; q: string; page: number }

export type Attempt = { attempt_no: number; started_at: string; finished_at: string | null; outcome: string | null; http_status: number | null }

export type Recipient = {
  id: number
  name: string
  contact: string // masked by the server
  channel: Channel
  group: Group
  reason: string | null
  detail: { part: 'sms' | 'subject' | 'body'; length: number; limit: number } | null
  next_attempt_at: string | null
  attempts: Attempt[]
}

export type RecipientsPage = {
  as_of: string
  groups: Partial<Record<Channel, Record<Group, number>>>
  reasons: Record<string, number>
  total: number
  page: number
  page_size: number
  recipients: Recipient[]
}

export const MAX_QUERY = 50 // characters, as the API allows
const MAX_PAGE = 1000 // as the API allows

const first = (v: LocationQuery[string] | undefined) => (Array.isArray(v) ? v[0] : v) ?? ''

export function readQuery(query: LocationQuery): RecipientsQuery | null {
  const group = first(query.recipients) as Group
  if (!GROUPS.includes(group)) return null
  const channel = first(query.channel)
  const page = Number(first(query.page))
  return {
    group,
    channel: channel === 'sms' || channel === 'email' ? channel : 'all',
    q: [...first(query.q)].slice(0, MAX_QUERY).join(''),
    page: Number.isInteger(page) && page >= 1 ? Math.min(page, MAX_PAGE) : 1,
  }
}

export function writeQuery(r: RecipientsQuery): LocationQueryRaw {
  const out: LocationQueryRaw = { recipients: r.group }
  if (r.channel !== 'all') out.channel = r.channel
  if (r.q) out.q = r.q
  if (r.page > 1) out.page = String(r.page)
  return out
}

export function recipientsPath(id: number, r: RecipientsQuery): string {
  const p = new URLSearchParams({ group: r.group })
  if (r.channel !== 'all') p.set('channel', r.channel)
  if (r.q.trim()) p.set('q', r.q.trim())
  p.set('page', String(r.page))
  return `/api/campaigns/${id}/recipients?${p}`
}

// The design's tab order: what needs attention first.
const TAB_ORDER: Group[] = ['failed', 'skipped', 'uncertain', 'retrying', 'pending', 'sent', 'cancelled']

export function tabCounts(groups: RecipientsPage['groups'], channel: Channel | 'all') {
  const channels: Channel[] = channel === 'all' ? ['sms', 'email'] : [channel]
  return TAB_ORDER.map((group) => ({ group, count: channels.reduce((n, ch) => n + (groups[ch]?.[group] ?? 0), 0) }))
}

export function pageInfo(p: Pick<RecipientsPage, 'total' | 'page' | 'page_size'>, rows: number) {
  const from = rows > 0 ? (p.page - 1) * p.page_size + 1 : 0
  const last = Math.max(1, Math.ceil(p.total / p.page_size))
  return {
    from,
    to: rows > 0 ? from + rows - 1 : 0,
    total: p.total,
    hasPrev: p.page > 1,
    hasNext: p.page * p.page_size < p.total,
    prevPage: Math.max(1, Math.min(p.page - 1, last)), // past the end: straight to the last page
  }
}

// rowDetail is the line under a row's reason (board 5): a dictionary key
// with its values.
export type RowDetail =
  | { key: 'detail.sent' | 'detail.pending' | 'detail.cancelled' | 'detail.uncertain' | 'detail.provider_rejected' | 'detail.no_consent' | 'detail.lengthUnknown' }
  | { key: 'detail.retries_exhausted'; count: number }
  | { key: 'detail.length.sms' | 'detail.length.subject' | 'detail.length.body'; length: number; limit: number }
  | { key: 'detail.nextAttempt'; at: string }

export function rowDetail(r: Recipient): RowDetail {
  switch (r.group) {
    case 'failed':
      return r.reason === 'provider_rejected' ? { key: 'detail.provider_rejected' } : { key: 'detail.retries_exhausted', count: r.attempts.length }
    case 'skipped':
      if (r.reason === 'no_consent') return { key: 'detail.no_consent' }
      // The length is worked out with today's names; null if they changed since.
      return r.detail ? { key: `detail.length.${r.detail.part}`, length: r.detail.length, limit: r.detail.limit } : { key: 'detail.lengthUnknown' }
    case 'retrying':
      return r.next_attempt_at ? { key: 'detail.nextAttempt', at: r.next_attempt_at } : { key: 'detail.pending' }
    case 'uncertain':
      return { key: 'detail.uncertain' }
    default:
      return { key: `detail.${r.group}` }
  }
}

export type AttemptView = {
  code: string | null // "HTTP 429"; null when the answer is better said in words
  outcome: string // accepted, rate_limited, …, or none when no answer was recorded
  next: 'retry' | 'sent' | 'failed' | 'skipped' | 'cancelled' | 'uncertain'
  tone: 'ok' | 'retry' | 'fail' | 'unknown'
}

// attemptView: one line of the attempt history, ending in what it led to.
export function attemptView(a: Attempt, last: boolean, r: Recipient): AttemptView {
  const outcome = a.outcome ?? 'none'
  const next: AttemptView['next'] = !last || r.group === 'retrying' || r.group === 'pending' ? 'retry' : r.group
  const tone = outcome === 'accepted' ? 'ok' : next === 'retry' ? 'retry' : next === 'uncertain' ? 'unknown' : 'fail'
  return { code: outcome !== 'accepted' && a.http_status ? `HTTP ${a.http_status}` : null, outcome, next, tone }
}
