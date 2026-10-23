import { describe, expect, it } from 'vitest'
import { fromLocalInput, scheduleProblem, suggestion, toLocalInput } from './schedule'

const now = new Date('2026-10-22T12:00:00Z')

describe('suggestion', () => {
  it('preselects a usable time from the brief', () => {
    expect(suggestion('2026-10-23T07:00:00Z', 'tomorrow at 10:00', now)).toEqual({ preselect: true, at: '2026-10-23T07:00:00Z', note: 'tomorrow at 10:00' })
  })

  // Brief said "yesterday at 10" or the proposal went stale: show the note,
  // select nothing, so the marketer must choose (plan, Review Focus 7).
  it('selects nothing when the time is unusable, keeping the note', () => {
    expect(suggestion('2026-10-22T11:00:00Z', 'yesterday at 10', now)).toEqual({ preselect: false, at: null, note: 'yesterday at 10' })
    expect(suggestion('2026-12-01T10:00:00Z', 'next month', now)).toEqual({ preselect: false, at: null, note: 'next month' })
    expect(suggestion(null, 'next month', now)).toEqual({ preselect: false, at: null, note: 'next month' })
    expect(suggestion(null, null, now)).toEqual({ preselect: false, at: null, note: null })
  })
})

describe('scheduleProblem', () => {
  it.each([
    ['2026-10-22T12:00:30Z', 'tooSoon'],
    ['2026-11-21T12:00:01Z', 'tooLate'],
    ['not a date', 'invalid'],
    ['2026-10-22T12:01:00Z', null],
    ['2026-11-21T12:00:00Z', null],
  ])('%s → %s', (at, problem) => {
    expect(scheduleProblem(at, now)).toBe(problem)
  })
})

describe('local date-time input', () => {
  // The tests run in Europe/Istanbul (vite.config.ts), so the conversion is real.
  it('shows the instant in the browser zone and sends it as UTC', () => {
    const local = toLocalInput('2026-10-23T07:00:00Z')
    expect(local).toBe('2026-10-23T10:00')
    expect(fromLocalInput(local)).toBe('2026-10-23T07:00:00.000Z')
    expect(fromLocalInput('2026-10-24T00:30')).toBe('2026-10-23T21:30:00.000Z') // the day changes too
  })

  it('rejects an empty input', () => {
    expect(fromLocalInput('')).toBeNull()
  })
})
