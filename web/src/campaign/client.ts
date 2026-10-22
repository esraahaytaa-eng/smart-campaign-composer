import { api } from '../api'
import type { Campaign, Catalog, Channel, Rule } from './types'

const path = (id: number, rest = '') => `/api/campaigns/${id}${rest}`

export const campaigns = {
  create: (request: string, channels: Channel[], timeZone: string) =>
    api<Campaign>('/api/campaigns', { method: 'POST', body: { request, channels, time_zone: timeZone } }),
  get: (id: number) => api<Campaign>(path(id)),
  setRules: (id: number, version: number, rules: Rule[]) => api<Campaign>(path(id, '/rules'), { method: 'PUT', body: { version, rules } }),
  setChannels: (id: number, version: number, channels: Channel[]) =>
    api<Campaign>(path(id, '/channels'), { method: 'PUT', body: { version, channels } }),
  setRequest: (id: number, version: number, request: string) =>
    api<Campaign>(path(id, '/request'), { method: 'PUT', body: { version, request } }),
  setName: (id: number, version: number, name: string) => api<Campaign>(path(id, '/name'), { method: 'PUT', body: { version, name } }),
  remove: (id: number, version: number) => api<void>(path(id), { method: 'DELETE', body: { version } }),
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
