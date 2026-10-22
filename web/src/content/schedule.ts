// Send-time choice (D29): at least 1 minute and at most 30 days ahead; the
// server checks again. Times are entered in the browser's zone, sent as UTC.
const MIN_LEAD = 60_000
const MAX_AHEAD = 30 * 24 * 3600_000

export type ScheduleProblem = 'invalid' | 'tooSoon' | 'tooLate'

export function scheduleProblem(at: string, now: Date): ScheduleProblem | null {
  const t = Date.parse(at)
  if (Number.isNaN(t)) return 'invalid'
  if (t < now.getTime() + MIN_LEAD) return 'tooSoon'
  if (t > now.getTime() + MAX_AHEAD) return 'tooLate'
  return null
}

// suggestion turns the brief's proposed time into the review's starting
// choice: preselected only if still usable; the note is shown either way.
export function suggestion(sendAt: string | null, note: string | null, now: Date) {
  const usable = sendAt !== null && scheduleProblem(sendAt, now) === null
  return { preselect: usable, at: usable ? sendAt : null, note }
}

const two = (n: number) => String(n).padStart(2, '0')

// ISO instant → "YYYY-MM-DDTHH:MM" in the browser's zone (datetime-local).
export function toLocalInput(iso: string): string {
  const d = new Date(iso)
  return `${d.getFullYear()}-${two(d.getMonth() + 1)}-${two(d.getDate())}T${two(d.getHours())}:${two(d.getMinutes())}`
}

// "YYYY-MM-DDTHH:MM" in the browser's zone → UTC ISO string.
export function fromLocalInput(local: string): string | null {
  if (!local) return null
  const d = new Date(local)
  return Number.isNaN(d.getTime()) ? null : d.toISOString()
}

export function browserZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}
