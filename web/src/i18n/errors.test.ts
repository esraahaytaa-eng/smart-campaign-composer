import { afterEach, describe, expect, it } from 'vitest'
import { ApiError } from '../api'
import { setLocale } from './index'
import { aiReason, errorText, sessionLoadError } from './errors'

afterEach(() => setLocale('en'))

describe('errorText', () => {
  it.each([
    [new ApiError("Can't reach the server.", 0), "Can't reach the server. Check that it is running."],
    [new ApiError('campaign not found', 404), 'This campaign doesn’t exist, or it was deleted.'],
    [new ApiError('The AI returned an invalid answer twice.', 502), 'The AI couldn’t produce a usable answer. Try again, or rephrase the brief.'],
    [new ApiError('The AI did not answer in time.', 504), 'The AI didn’t answer in time. Try again.'],
    [new ApiError('internal error', 500), 'The server ran into a problem. Try again in a moment.'],
    [new ApiError('login required', 401), 'Your session has expired. Sign in again.'],
    [new Error('boom'), 'Something went wrong. Try again.'],
  ])('%s', (err, text) => {
    expect(errorText(err)).toBe(text)
  })

  // Validation the UI can't foresee: a localized heading, the server's own
  // words kept as details so nothing is lost.
  it('keeps the server details of a rejected change', () => {
    const err = new ApiError('send_at must be between now and 30 days from now', 422)
    expect(errorText(err)).toBe('The server didn’t accept this change. Details from the server (in English): send_at must be between now and 30 days from now')
    setLocale('tr')
    expect(errorText(err)).toBe('Sunucu bu değişikliği kabul etmedi. Sunucudan gelen ayrıntı (İngilizce): send_at must be between now and 30 days from now')
  })

  // 409 means many things (stale version, not a draft, no skipped recipients,
  // an active resend…): the server's reason is kept, never "changed elsewhere".
  it('keeps the reason of a conflict', () => {
    const err = new ApiError('no recipients were skipped for the character limit on this channel', 409)
    expect(errorText(err)).toBe(
      'The server didn’t accept this change. Details from the server (in English): no recipients were skipped for the character limit on this channel',
    )
  })

  it('explains a session that could not be loaded after signing in', () => {
    expect(errorText(sessionLoadError())).toBe('Signed in, but the session could not be loaded. Try again.')
  })

  it('speaks Turkish when the UI does', () => {
    setLocale('tr')
    expect(errorText(new ApiError('', 0))).toBe('Sunucuya ulaşılamıyor. Çalıştığından emin olun.')
  })
})

// The AI's reason is AI output: shown exactly as written, never translated.
describe('aiReason', () => {
  it('returns the reason of an unsupported brief unchanged', () => {
    const reason = 'Yaş bilgisi katalogda yok; "40 yaş üstü" bir kurala çevrilemez.'
    setLocale('en')
    expect(aiReason(new ApiError(reason, 422, { error: reason, unsupported: true }))).toBe(reason)
  })

  it('is null for every other error', () => {
    expect(aiReason(new ApiError('invalid', 422, { error: 'invalid' }))).toBeNull()
    expect(aiReason(new Error('x'))).toBeNull()
  })
})
