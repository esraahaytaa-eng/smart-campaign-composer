// The campaign list and dashboard (D49): filtered and searched in the
// browser over the newest campaigns the API returns.
import type { Campaign, Status } from './types'

const STATUSES: Status[] = ['draft', 'approved', 'sending', 'completed', 'cancelled']

export type StatusFilter = Status | 'all'

export function statusCounts(list: Campaign[]): Record<StatusFilter, number> {
  const out = { all: list.length } as Record<StatusFilter, number>
  for (const s of STATUSES) out[s] = list.filter((c) => c.status === s).length
  return out
}

const turkish: Record<string, string> = { ç: 'c', ğ: 'g', ı: 'i', i̇: 'i', ö: 'o', ş: 's', ü: 'u' }

// fold makes "İstanbul", "ISTANBUL" and "istanbul" match, like the backend.
export function fold(s: string): string {
  return s
    .replace(/I/g, 'i')
    .toLocaleLowerCase('tr')
    .replace(/i̇|[çğıöşü]/g, (ch) => turkish[ch]!)
    .replace(/\s+/g, ' ')
    .trim()
}

export function filterCampaigns(list: Campaign[], status: StatusFilter, query: string): Campaign[] {
  const q = fold(query)
  return list.filter((c) => (status === 'all' || c.status === status) && (!q || fold(c.request_text).includes(q)))
}

// What's live and what's waiting comes first: sending, drafts, then
// scheduled by send time, then finished ones most recent first.
const RANK: Record<Status, number> = { sending: 0, draft: 1, approved: 2, completed: 3, cancelled: 3 }

export function dashboardCampaigns(list: Campaign[], n: number): { rows: Campaign[]; more: number } {
  const key = (c: Campaign) => (c.status === 'approved' ? (c.send_at ?? '') : c.updated_at)
  const sorted = [...list].sort((a, b) => {
    if (RANK[a.status] !== RANK[b.status]) return RANK[a.status] - RANK[b.status]
    return a.status === 'approved' ? key(a).localeCompare(key(b)) : key(b).localeCompare(key(a))
  })
  return { rows: sorted.slice(0, n), more: Math.max(0, list.length - n) }
}

export type Action = 'delete' | 'edit' | 'cancel' | 'view' | 'monitor' | 'viewResults'

export function campaignActions(c: Campaign): Action[] {
  switch (c.status) {
    case 'draft':
      return ['delete', 'edit']
    case 'approved':
      return ['cancel', 'view']
    case 'sending':
      return ['cancel', 'monitor']
    default:
      return ['viewResults']
  }
}

// The dashboard row's one button and its line under the title (board B).
export function dashboardAction(c: Campaign): 'edit' | 'monitor' | 'view' {
  return c.status === 'draft' ? 'edit' : c.status === 'sending' ? 'monitor' : 'view'
}

export type DashboardHint =
  | { key: 'sending' | 'completed' | 'cancelled' }
  | { key: 'draft' | 'scheduled'; at: string }

export function dashboardHint(c: Campaign): DashboardHint {
  if (c.status === 'draft') return { key: 'draft', at: c.updated_at }
  if (c.status === 'approved') return { key: 'scheduled', at: c.send_at ?? c.updated_at }
  return { key: c.status }
}
