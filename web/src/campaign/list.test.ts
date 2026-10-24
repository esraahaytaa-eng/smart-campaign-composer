import { describe, expect, it } from 'vitest'
import type { LocationQuery } from 'vue-router'
import { campaignActions, dashboardAction, dashboardHint, listPath, readListQuery, writeListQuery } from './list'
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
  default_language: null,
  time_zone: 'Europe/Istanbul',
  parent_campaign_id: null,
  content: null,
  created_at: '2026-10-20T10:00:00Z',
  updated_at: '2026-10-20T10:00:00Z',
  ...patch,
})

describe('readListQuery / writeListQuery', () => {
  it('round-trips the filters through the address, leaving defaults out', () => {
    const q = { status: 'completed' as const, q: 'istanbul', page: 3 }
    expect(writeListQuery(q)).toEqual({ status: 'completed', q: 'istanbul', page: '3' })
    expect(readListQuery(writeListQuery(q) as LocationQuery)).toEqual(q)
    expect(writeListQuery({ status: 'all', q: '', page: 1 })).toEqual({})
    expect(readListQuery({})).toEqual({ status: 'all', q: '', page: 1 })
  })

  it('falls back to defaults for invalid values, as the API would refuse them', () => {
    expect(readListQuery({ status: 'lost', page: '0' })).toEqual({ status: 'all', q: '', page: 1 })
    expect(readListQuery({ page: 'x' }).page).toBe(1)
    expect(readListQuery({ page: '20000' }).page).toBe(10000)
    expect(readListQuery({ q: 'a'.repeat(80) }).q).toHaveLength(50)
    expect(readListQuery({ status: ['draft', 'sending'] }).status).toBe('draft')
  })
})

describe('listPath', () => {
  it('sends only what is set, encoded', () => {
    expect(listPath({ status: 'all', q: '', page: 1 })).toBe('/api/campaigns?page=1')
    expect(listPath({ status: 'draft', q: 'a&b ş', page: 2 })).toBe('/api/campaigns?status=draft&q=a%26b+%C5%9F&page=2')
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

describe('dashboard rows', () => {
  it.each([
    ['sending', 'monitor', { key: 'sending' }],
    ['draft', 'edit', { key: 'draft', at: '2026-10-21T08:00:00Z' }],
    ['approved', 'view', { key: 'scheduled', at: '2026-10-25T10:00:00Z' }],
    ['completed', 'view', { key: 'completed' }],
    ['cancelled', 'view', { key: 'cancelled' }],
  ] as const)('a %s campaign opens with %s and says %j', (status, action, hint) => {
    const x = c(status, { updated_at: '2026-10-21T08:00:00Z', send_at: '2026-10-25T10:00:00Z' })
    expect(dashboardAction(x)).toBe(action)
    expect(dashboardHint(x)).toEqual(hint)
  })
})
