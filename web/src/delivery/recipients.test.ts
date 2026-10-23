import { describe, expect, it } from 'vitest'
import type { LocationQuery } from 'vue-router'
import { pageInfo, readQuery, recipientsPath, tabCounts, writeQuery, type RecipientsQuery } from './recipients'

const base: RecipientsQuery = { group: 'failed', channel: 'all', q: '', page: 1 }

describe('readQuery / writeQuery', () => {
  it('round-trips through the URL query, leaving defaults out', () => {
    const q: RecipientsQuery = { group: 'skipped', channel: 'sms', q: 'ayşe', page: 3 }
    expect(writeQuery(q)).toEqual({ recipients: 'skipped', channel: 'sms', q: 'ayşe', page: '3' })
    expect(readQuery(writeQuery(q) as LocationQuery)).toEqual(q)
    expect(writeQuery(base)).toEqual({ recipients: 'failed' })
    expect(readQuery({ recipients: 'failed' })).toEqual(base)
  })

  it('has no drawer without a valid group', () => {
    expect(readQuery({})).toBeNull()
    expect(readQuery({ recipients: 'lost' })).toBeNull()
  })

  it('falls back to defaults for invalid values instead of failing', () => {
    expect(readQuery({ recipients: 'sent', channel: 'fax', page: '-2' })).toEqual({ ...base, group: 'sent' })
    expect(readQuery({ recipients: 'sent', page: 'x' })?.page).toBe(1)
    expect(readQuery({ recipients: 'sent', q: 'a'.repeat(80) })?.q).toHaveLength(50)
  })

  it('takes the first value of a repeated parameter', () => {
    expect(readQuery({ recipients: ['sent', 'failed'] })?.group).toBe('sent')
  })
})

describe('recipientsPath', () => {
  it('sends only the filters that are set, encoded', () => {
    expect(recipientsPath(7, base)).toBe('/api/campaigns/7/recipients?group=failed&page=1')
    expect(recipientsPath(7, { group: 'sent', channel: 'email', q: 'a&b ş', page: 2 })).toBe(
      '/api/campaigns/7/recipients?group=sent&channel=email&q=a%26b+%C5%9F&page=2',
    )
  })
})

describe('tabCounts', () => {
  const groups = {
    sms: { sent: 5, uncertain: 1, failed: 2, skipped: 3, cancelled: 0, retrying: 0, pending: 1 },
    email: { sent: 7, uncertain: 0, failed: 1, skipped: 0, cancelled: 0, retrying: 2, pending: 0 },
  }

  it('sums the channels for "all" and picks one otherwise, in the design order', () => {
    expect(tabCounts(groups, 'all')).toEqual([
      { group: 'failed', count: 3 },
      { group: 'skipped', count: 3 },
      { group: 'uncertain', count: 1 },
      { group: 'retrying', count: 2 },
      { group: 'pending', count: 1 },
      { group: 'sent', count: 12 },
      { group: 'cancelled', count: 0 },
    ])
    expect(tabCounts(groups, 'email').find((t) => t.group === 'sent')?.count).toBe(7)
  })

  it('counts zero for a channel the campaign does not use', () => {
    expect(tabCounts({ sms: groups.sms }, 'email').every((t) => t.count === 0)).toBe(true)
  })
})

describe('pageInfo', () => {
  it('describes the rows shown and the paging buttons', () => {
    expect(pageInfo({ total: 121, page: 1, page_size: 50 }, 50)).toEqual({ from: 1, to: 50, total: 121, hasPrev: false, hasNext: true })
    expect(pageInfo({ total: 121, page: 3, page_size: 50 }, 21)).toEqual({ from: 101, to: 121, total: 121, hasPrev: true, hasNext: false })
    expect(pageInfo({ total: 0, page: 1, page_size: 50 }, 0)).toEqual({ from: 0, to: 0, total: 0, hasPrev: false, hasNext: false })
  })

  it('offers a way back from a page past the end', () => {
    expect(pageInfo({ total: 10, page: 4, page_size: 50 }, 0)).toEqual({ from: 0, to: 0, total: 10, hasPrev: true, hasNext: false })
  })
})
