import { describe, expect, it } from 'vitest'
import type { LocationQuery } from 'vue-router'
import { attemptView, pageInfo, readQuery, recipientsPath, rowDetail, tabCounts, writeQuery, type Attempt, type Recipient, type RecipientsQuery } from './recipients'

const base: RecipientsQuery = { group: 'failed', channel: 'all', language: '', q: '', page: 1 }

describe('readQuery / writeQuery', () => {
  it('round-trips through the URL query, leaving defaults out', () => {
    const q: RecipientsQuery = { group: 'skipped', channel: 'sms', language: 'en', q: 'ayşe', page: 3 }
    expect(writeQuery(q)).toEqual({ recipients: 'skipped', channel: 'sms', language: 'en', q: 'ayşe', page: '3' })
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
    expect(readQuery({ recipients: 'sent', language: 'Türkçe!' })?.language).toBe('') // not a language code
    expect(readQuery({ recipients: 'sent', page: 'x' })?.page).toBe(1)
    expect(readQuery({ recipients: 'sent', q: 'a'.repeat(80) })?.q).toHaveLength(50)
    expect(readQuery({ recipients: 'sent', page: '5000' })?.page).toBe(1000) // the API's limit
  })

  it('takes the first value of a repeated parameter', () => {
    expect(readQuery({ recipients: ['sent', 'failed'] })?.group).toBe('sent')
  })
})

describe('recipientsPath', () => {
  it('sends only the filters that are set, encoded', () => {
    expect(recipientsPath(7, base)).toBe('/api/campaigns/7/recipients?group=failed&page=1')
    expect(recipientsPath(7, { group: 'sent', channel: 'email', language: 'en', q: 'a&b ş', page: 2 })).toBe(
      '/api/campaigns/7/recipients?group=sent&channel=email&language=en&q=a%26b+%C5%9F&page=2',
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
    expect(pageInfo({ total: 121, page: 1, page_size: 50 }, 50)).toEqual({ from: 1, to: 50, total: 121, hasPrev: false, hasNext: true, prevPage: 1 })
    expect(pageInfo({ total: 121, page: 3, page_size: 50 }, 21)).toEqual({ from: 101, to: 121, total: 121, hasPrev: true, hasNext: false, prevPage: 2 })
    expect(pageInfo({ total: 0, page: 1, page_size: 50 }, 0)).toEqual({ from: 0, to: 0, total: 0, hasPrev: false, hasNext: false, prevPage: 1 })
  })

  it('offers a way back from a page past the end, straight to the last page', () => {
    expect(pageInfo({ total: 10, page: 4, page_size: 50 }, 0)).toEqual({ from: 0, to: 0, total: 10, hasPrev: true, hasNext: false, prevPage: 1 })
    expect(pageInfo({ total: 120, page: 9, page_size: 50 }, 0).prevPage).toBe(3)
    expect(pageInfo({ total: 120, page: 3, page_size: 50 }, 20).prevPage).toBe(2)
  })
})

const attempt = (a: Partial<Attempt>): Attempt => ({ attempt_no: 1, started_at: '2026-10-23T10:00:00Z', finished_at: null, outcome: 'accepted', http_status: 200, ...a })
const recipient = (r: Partial<Recipient>): Recipient => ({
  id: 1, name: 'Ayşe Demir', contact: '+90 555 ••• •• 17', channel: 'sms', language: 'tr', group: 'sent', reason: null, detail: null, next_attempt_at: null, attempts: [], ...r,
})

describe('rowDetail', () => {
  it.each([
    [{ group: 'sent' }, { key: 'detail.sent' }],
    [{ group: 'pending' }, { key: 'detail.pending' }],
    [{ group: 'cancelled' }, { key: 'detail.cancelled' }],
    [{ group: 'failed', reason: 'provider_rejected' }, { key: 'detail.provider_rejected' }],
    [{ group: 'failed', reason: 'retries_exhausted', attempts: [attempt({}), attempt({ attempt_no: 2 })] }, { key: 'detail.retries_exhausted', count: 2 }],
    [{ group: 'skipped', reason: 'no_consent' }, { key: 'detail.no_consent' }],
    [{ group: 'skipped', reason: 'character_limit_exceeded', detail: { part: 'sms', length: 165, limit: 160 } }, { key: 'detail.length.sms', length: 165, limit: 160 }],
    [{ group: 'skipped', reason: 'character_limit_exceeded', detail: { part: 'subject', length: 85, limit: 80 } }, { key: 'detail.length.subject', length: 85, limit: 80 }],
    [{ group: 'skipped', reason: 'character_limit_exceeded' }, { key: 'detail.lengthUnknown' }], // names changed since the skip
    [{ group: 'uncertain', reason: 'timeout' }, { key: 'detail.uncertain' }],
    [{ group: 'retrying', reason: 'rate_limited', next_attempt_at: '2026-10-23T10:00:05Z' }, { key: 'detail.nextAttempt', at: '2026-10-23T10:00:05Z' }],
  ] as const)('%j → %j', (r, want) => {
    expect(rowDetail(recipient(r as Partial<Recipient>))).toEqual(want)
  })
})

describe('attemptView', () => {
  it('names the answer and what followed', () => {
    const r = recipient({ group: 'failed', reason: 'retries_exhausted' })
    expect(attemptView(attempt({ outcome: 'rate_limited', http_status: 429 }), false, r)).toEqual({ code: 'HTTP 429', outcome: 'rate_limited', next: 'retry', tone: 'retry' })
    expect(attemptView(attempt({ outcome: 'server_error', http_status: 503 }), true, r)).toEqual({ code: 'HTTP 503', outcome: 'server_error', next: 'failed', tone: 'fail' })
  })

  it('shows acceptance and missing answers in words', () => {
    expect(attemptView(attempt({}), true, recipient({}))).toEqual({ code: null, outcome: 'accepted', next: 'sent', tone: 'ok' })
    expect(attemptView(attempt({ outcome: 'timeout', http_status: null }), true, recipient({ group: 'uncertain' }))).toEqual({ code: null, outcome: 'timeout', next: 'uncertain', tone: 'unknown' })
    expect(attemptView(attempt({ outcome: null, http_status: null }), true, recipient({ group: 'uncertain' }))).toEqual({ code: null, outcome: 'none', next: 'uncertain', tone: 'unknown' })
  })

  it('a retrying message ends its history with a scheduled retry', () => {
    expect(attemptView(attempt({ outcome: 'server_error', http_status: 500 }), true, recipient({ group: 'retrying' })).next).toBe('retry')
  })
})

// D68: the version filter of an A/B test lives in the URL like the others.
describe('the version filter', () => {
  it('round-trips through the URL and the API path', () => {
    const q: RecipientsQuery = { ...base, variant: 'b' }
    expect(writeQuery(q)).toEqual({ recipients: 'failed', variant: 'b' })
    expect(readQuery({ recipients: 'failed', variant: 'b' })?.variant).toBe('b')
    expect(recipientsPath(7, q)).toBe('/api/campaigns/7/recipients?group=failed&variant=b&page=1')
  })

  it('ignores anything but a or b', () => {
    expect(readQuery({ recipients: 'failed', variant: 'c' })?.variant).toBeUndefined()
  })
})
