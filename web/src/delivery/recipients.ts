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
    page: Number.isInteger(page) && page >= 1 ? page : 1,
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
  return { from, to: rows > 0 ? from + rows - 1 : 0, total: p.total, hasPrev: p.page > 1, hasNext: p.page * p.page_size < p.total }
}
