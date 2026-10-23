// What still stops approval. Sending always needs an explicit choice of when,
// a confirmation that the marketer reviewed it, and a second confirm (D29).
import type { Channel, Content } from '../campaign/types'
import type { ScheduleProblem } from './schedule'

export type ApprovalState = {
  channels: Channel[]
  content: Content | null
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
  | 'scheduleInvalid'
  | 'scheduleTooSoon'
  | 'scheduleTooLate'

export function approvalBlockers(s: ApprovalState): Blocker[] {
  const out: Blocker[] = []
  const blank = (v?: string) => !v || v.trim() === ''
  if (s.channels.includes('sms') && blank(s.content?.sms?.body)) out.push('missingSms')
  if (s.channels.includes('email') && (blank(s.content?.email?.subject) || blank(s.content?.email?.body))) out.push('missingEmail')
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
