// Shapes of the campaign API (README, "Campaign draft API").

export type Channel = 'sms' | 'email'
export type Status = 'draft' | 'approved' | 'sending' | 'completed' | 'cancelled'
export type Rule = { field: string; op: string; value: unknown }
export type Counts = { total: number; sms: number; email: number }
export type Content = { sms?: { body: string }; email?: { subject: string; body: string } }
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
  counts?: Counts // drafts only
  content: Content | null
  created_at: string
  updated_at: string
  approved_at?: string | null
  audience_built_at?: string | null // when sending started
  cancelled_at?: string | null
  completed_at?: string | null
}

export type ValueType = 'cities' | 'window' | 'categories_window' | 'count' | 'amount'

export type Catalog = {
  fields: { field: string; ops: string[]; value: ValueType }[]
  cities: string[]
  categories: string[]
  limits: { max_rules: number; max_days: number; max_count: number; max_amount: number; min_date: string }
}
