import { describe, expect, it } from 'vitest'
import { campaignActions, dashboardCampaigns, filterCampaigns, statusCounts } from './list'
import type { Campaign, Status } from './types'

let nextId = 1
const c = (status: Status, patch: Partial<Campaign> = {}): Campaign => ({
  id: nextId++,
  request_text: 'Brief',
  channels: ['sms'],
  rules: [],
  status,
  version: 1,
  send_at: null,
  schedule_note: null,
  time_zone: 'Europe/Istanbul',
  parent_campaign_id: null,
  content: null,
  created_at: '2026-10-20T10:00:00Z',
  updated_at: '2026-10-20T10:00:00Z',
  ...patch,
})

describe('statusCounts', () => {
  it('counts every status and the total', () => {
    expect(statusCounts([c('draft'), c('draft'), c('sending'), c('cancelled')])).toEqual({
      all: 4, draft: 2, approved: 0, sending: 1, completed: 0, cancelled: 1,
    })
  })
})

describe('filterCampaigns', () => {
  const list = [
    c('draft', { request_text: 'İstanbul sepet hatırlatması' }),
    c('completed', { request_text: 'Ankara kitap kampanyası' }),
    c('sending', { request_text: 'ISPARTA ŞUBE açılışı' }),
  ]

  it('filters by status and keeps the order', () => {
    expect(filterCampaigns(list, 'completed', '').map((x) => x.request_text)).toEqual(['Ankara kitap kampanyası'])
    expect(filterCampaigns(list, 'all', '')).toHaveLength(3)
  })

  it('searches the brief ignoring case and Turkish letters', () => {
    expect(filterCampaigns(list, 'all', 'istanbul').map((x) => x.status)).toEqual(['draft'])
    expect(filterCampaigns(list, 'all', 'SEPET HATIRLATMASI')).toHaveLength(1)
    expect(filterCampaigns(list, 'all', 'isparta sube').map((x) => x.status)).toEqual(['sending'])
    expect(filterCampaigns(list, 'all', '  kitap ')).toHaveLength(1)
    expect(filterCampaigns(list, 'draft', 'kitap')).toHaveLength(0)
  })
})

describe('dashboardCampaigns', () => {
  it("puts what's live and what's waiting first, then the most recent", () => {
    const done1 = c('completed', { updated_at: '2026-10-21T10:00:00Z' })
    const done2 = c('cancelled', { updated_at: '2026-10-22T10:00:00Z' })
    const later = c('approved', { send_at: '2026-10-30T10:00:00Z' })
    const sooner = c('approved', { send_at: '2026-10-25T10:00:00Z' })
    const draft = c('draft', { updated_at: '2026-10-19T10:00:00Z' })
    const live = c('sending')
    const { rows, more } = dashboardCampaigns([done1, done2, later, sooner, draft, live], 5)
    expect(rows.map((x) => x.id)).toEqual([live.id, draft.id, sooner.id, later.id, done2.id])
    expect(more).toBe(1)
  })

  it('has nothing more when everything fits', () => {
    expect(dashboardCampaigns([c('draft')], 6)).toEqual({ rows: [expect.any(Object)], more: 0 })
  })
})

describe('campaignActions', () => {
  it.each([
    ['draft', ['delete', 'edit']],
    ['approved', ['cancel', 'view']],
    ['sending', ['cancel', 'monitor']],
    ['completed', ['viewResults']],
    ['cancelled', ['viewResults']],
  ] as const)('%s offers %j', (status, actions) => {
    expect(campaignActions(c(status))).toEqual(actions)
  })
})
