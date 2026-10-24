// What still stops approval. Sending always needs an explicit choice of when,
// a confirmation that the marketer reviewed it, and a second confirm (D29).
import type { Channel, Lang, LanguageState, Variants } from '../campaign/types'
import type { ScheduleProblem } from './schedule'

export type ApprovalState = {
  channels: Channel[]
  content: Variants | null
  defaultLanguage: Lang | null
  languages: Record<Lang, LanguageState>
  audience: number
  when: 'now' | 'later' | null
  at: string | null
  reviewed: boolean
  scheduleProblem: ScheduleProblem | null
}

export type Blocker =
  | 'chooseWhen'
  | 'confirmReview'
  | 'noAudience'
  | 'missingSms'
  | 'missingEmail'
  | 'missingDefault'
  | 'unapprovedLanguage'
  | 'scheduleInvalid'
  | 'scheduleTooSoon'
  | 'scheduleTooLate'

export function approvalBlockers(s: ApprovalState): Blocker[] {
  const out: Blocker[] = []
  const blank = (v?: string) => !v || v.trim() === ''
  const all = Object.values(s.content ?? {})
  if (all.length === 0) all.push({})
  if (s.channels.includes('sms') && all.some((c) => blank(c.sms?.body))) out.push('missingSms')
  if (s.channels.includes('email') && all.some((c) => blank(c.email?.subject) || blank(c.email?.body))) out.push('missingEmail')
  if (s.content && Object.keys(s.content).length > 0) {
    if (s.defaultLanguage === null || s.content[s.defaultLanguage] === undefined) out.push('missingDefault')
    if (Object.keys(s.content).some((l) => !s.languages[l]?.approved_at)) out.push('unapprovedLanguage')
  }
  if (s.audience <= 0) out.push('noAudience')
  if (s.when === null) out.push('chooseWhen')
  if (s.when === 'later') {
    if (s.at === null || s.scheduleProblem === 'invalid') out.push('scheduleInvalid')
    else if (s.scheduleProblem === 'tooSoon') out.push('scheduleTooSoon')
    else if (s.scheduleProblem === 'tooLate') out.push('scheduleTooLate')
  }
  if (!s.reviewed) out.push('confirmReview')
  return out
}
