import { api, ApiError, responseError } from '../api'
import { createSSEParser, type SSEEvent } from '../content/sse'
import { recipientsPath, type RecipientsPage, type RecipientsQuery } from '../delivery/recipients'
import { listPath, type ListPage, type ListQuery } from './list'
import type { Campaign, Catalog, Channel, ChannelCounts, DeliveryCount, Lang, Rule, Variants } from './types'

type OverLimit = { sms: number; email: number }
export type Estimate = {
  audience: ChannelCounts & { by_language?: Record<Lang, ChannelCounts> }
  over_limit: (OverLimit & { by_language?: Record<Lang, OverLimit> }) | null
  calculated_at: string
  delivery?: Partial<Record<Channel, Record<Lang, DeliveryCount>>>
  recommendation?: { languages: Lang[]; default: Lang }
}
// What the AI writes (D64): replace rewrites languages that already have text (D66).
export type WriteRequest = { languages: Lang[]; default_language?: Lang; replace: boolean }

const path = (id: number, rest = '') => `/api/campaigns/${id}${rest}`

export const campaigns = {
  create: (request: string, channels: Channel[], timeZone: string) =>
    api<Campaign>('/api/campaigns', { method: 'POST', body: { request, channels, time_zone: timeZone } }),
  list: (q: ListQuery) => api<ListPage>(listPath(q)), // D54
  dashboard: () => api<ListPage>('/api/dashboard'),
  get: (id: number) => api<Campaign>(path(id)),
  cancel: (id: number) => api<Campaign>(path(id, '/cancel'), { method: 'POST' }),
  // A draft follow-up for the recipients skipped for length on one channel (D31).
  resend: (id: number, channel: Channel) => api<Campaign>(path(id, '/resend'), { method: 'POST', body: { channel } }),
  setRules: (id: number, version: number, rules: Rule[]) => api<Campaign>(path(id, '/rules'), { method: 'PUT', body: { version, rules } }),
  setChannels: (id: number, version: number, channels: Channel[]) =>
    api<Campaign>(path(id, '/channels'), { method: 'PUT', body: { version, channels } }),
  setRequest: (id: number, version: number, request: string) =>
    api<Campaign>(path(id, '/request'), { method: 'PUT', body: { version, request } }),
  remove: (id: number, version: number) => api<void>(path(id), { method: 'DELETE', body: { version } }),
  setContent: (id: number, version: number, content: Variants, defaultLanguage: Lang) =>
    api<Campaign>(path(id, '/content'), { method: 'PUT', body: { version, default_language: defaultLanguage, content } }),
  // A language's text approved as it is: no new version, no AI call (D66).
  approveLanguage: (id: number, lang: Lang, version: number) =>
    api<Campaign>(path(id, `/languages/${lang}/approve`), { method: 'POST', body: { version } }),
  estimate: (id: number) => api<Estimate>(path(id, '/estimate')),
  recipients: (id: number, q: RecipientsQuery) => api<RecipientsPage>(recipientsPath(id, q)),
  // send is required: "now" or { at } (D29). Nothing is sent without it.
  approve: (id: number, version: number, send: 'now' | { at: string }) =>
    api<Campaign>(path(id, '/approve'), { method: 'POST', body: { version, send } }),
}

let catalog: Promise<Catalog> | undefined

// The rule catalog changes only with the seed data, so it is loaded once.
export function loadCatalog(): Promise<Catalog> {
  catalog ??= api<Catalog>('/api/catalog').catch((err) => {
    catalog = undefined // try again next time
    throw err
  })
  return catalog
}

// The browser's IANA zone, which the backend needs for dates in briefs and rules.
export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}

// streamContent asks the AI for the messages and delivers the stream's events
// (delta, retry, done, error) as they arrive (D34). It is a POST, so it uses
// fetch, not EventSource; abort the signal to stop (nothing is saved then).
export async function streamContent(id: number, version: number, req: WriteRequest, onEvent: (e: SSEEvent) => void, signal: AbortSignal): Promise<void> {
  const url = path(id, '/content')
  let res: Response
  try {
    res = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'text/event-stream' },
      body: JSON.stringify({ version, ...req }),
      credentials: 'same-origin',
      signal,
    })
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') throw err
    throw new ApiError("Can't reach the server.", 0) // i18n-ignore: the UI shows errorText()
  }
  if (!res.ok || !res.body) throw await responseError(res, url)
  const parser = createSSEParser(onEvent)
  const reader = res.body.getReader()
  const decoder = new TextDecoder() // stream: true keeps characters split across chunks
  for (;;) {
    let chunk: ReadableStreamReadResult<Uint8Array>
    try {
      chunk = await reader.read()
    } catch (err) {
      // The connection dropped mid-stream: a network problem, said as one.
      if (err instanceof DOMException && err.name === 'AbortError') throw err
      throw new ApiError("Can't reach the server.", 0) // i18n-ignore: the UI shows errorText()
    }
    if (chunk.done) break
    parser.push(decoder.decode(chunk.value, { stream: true }))
  }
  parser.push(decoder.decode())
  parser.end()
}
