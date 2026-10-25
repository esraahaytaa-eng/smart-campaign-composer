// Shapes of the campaign API (the OpenAPI spec: internal/apidocs/openapi.json, served at /docs).

export type Channel = 'sms' | 'email'
export type Status = 'draft' | 'approved' | 'sending' | 'completed' | 'cancelled'
export type Rule = { field: string; op: string; value: unknown }
export type Lang = string // a catalog language, or 'und' on content from before T20 (D64)
export const LEGACY: Lang = 'und'
export type ChannelCounts = { total: number; sms: number; email: number }
export type Counts = ChannelCounts & { by_language?: Record<Lang, ChannelCounts> }
// One language's messages; text of a channel that isn't chosen is kept, not sent (D67).
export type Content = { sms?: { body: string }; email?: { subject: string; body: string } }
export type Variants = Record<Lang, Content>
export type LanguageState = { approved_at: string | null; outdated: boolean } // D66
export type DeliveryCount = { own: number; fallback: number }
export type FieldError = { rule: number; message: string }

export type Campaign = {
  id: number
  request_text: string
  channels: Channel[]
  rules: Rule[] | null // null on a resend: its audience comes from the parent (D31)
  status: Status
  version: number
  send_at: string | null
  schedule_note: string | null
  time_zone: string
  parent_campaign_id: number | null
  parent_request_text?: string | null // in lists: the original's brief, for a resend (D54)
  counts?: Counts // drafts only
  content: Variants | null
  default_language: Lang | null
  languages?: Record<Lang, LanguageState>
  // an A/B test's version B per language (D68); content is version A
  ab_test?: boolean
  content_b?: Variants
  // drafts only: a suggestion, never a restriction (D64), and who gets which text
  recommendation?: { languages: Lang[]; default: Lang }
  delivery?: Partial<Record<Channel, Record<Lang, DeliveryCount>>>
  approval_estimate?: { delivery?: Partial<Record<Channel, Record<Lang, DeliveryCount>>> } | null
  created_at: string
  updated_at: string
  approved_at?: string | null
  audience_built_at?: string | null // when sending started
  cancelled_at?: string | null
  completed_at?: string | null
}

export type ValueType = 'cities' | 'window' | 'categories_window' | 'count' | 'amount' | 'amount_window' | 'count_window'

export type Catalog = {
  fields: { field: string; ops: string[]; value: ValueType }[]
  cities: string[]
  categories: string[]
  limits: { max_rules: number; max_days: number; max_months: number; max_count: number; max_amount: number; min_date: string }
  languages: { code: Lang; name: string }[]
}
