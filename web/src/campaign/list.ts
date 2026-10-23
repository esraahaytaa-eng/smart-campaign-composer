// The campaign list (D54): filtered, searched, paged and counted by the
// server; the filters live in the address, so a reload or Back keeps them.
import type { LocationQuery, LocationQueryRaw } from 'vue-router'
import type { Campaign, Status } from './types'

const STATUSES: Status[] = ['draft', 'approved', 'sending', 'completed', 'cancelled']
const MAX_QUERY = 50 // characters, as the API allows
const MAX_PAGE = 10000 // as the API allows

export type StatusFilter = Status | 'all'
export type ListQuery = { status: StatusFilter; q: string; page: number }

export type ListPage = {
  campaigns: Campaign[]
  total: number
  page?: number
  page_size?: number
  counts: Record<StatusFilter, number> // over all campaigns
}

const first = (v: LocationQuery[string] | undefined) => (Array.isArray(v) ? v[0] : v) ?? ''

export function readListQuery(query: LocationQuery): ListQuery {
  const status = first(query.status)
  const page = Number(first(query.page))
  return {
    status: (STATUSES as string[]).includes(status) ? (status as Status) : 'all',
    q: [...first(query.q)].slice(0, MAX_QUERY).join(''),
    page: Number.isInteger(page) && page >= 1 ? Math.min(page, MAX_PAGE) : 1,
  }
}

export function writeListQuery(l: ListQuery): LocationQueryRaw {
  return { ...(l.status !== 'all' && { status: l.status }), ...(l.q && { q: l.q }), ...(l.page > 1 && { page: String(l.page) }) }
}

export function listPath(l: ListQuery): string {
  const p = new URLSearchParams()
  if (l.status !== 'all') p.set('status', l.status)
  if (l.q.trim()) p.set('q', l.q.trim())
  p.set('page', String(l.page))
  return `/api/campaigns?${p}`
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
