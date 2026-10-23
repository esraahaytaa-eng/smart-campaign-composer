// Errors in the UI's language (D44, approach A): each situation the UI knows
// gets its own text; validation it can't foresee keeps the server's English
// words as details. 409 is not mapped: the backend uses it for a stale
// version, a non-draft, no skipped recipients, an active resend…, so its
// reason is kept (a screen that knows it means "changed elsewhere" says so).
// AI output (an unsupported brief's reason) is never translated.

const SESSION_LOAD = 'session_load'

// sessionLoadError: signed in, but /api/me then failed (e.g. cookies blocked).
export const sessionLoadError = () => new ApiError('session could not be loaded', 0, { code: SESSION_LOAD }) // i18n-ignore: mapped by code
import { ApiError } from '../api'
import { t } from './index'

export function errorText(err: unknown): string {
  if (!(err instanceof ApiError)) return t('error.unknown')
  if ((err.body as { code?: string } | undefined)?.code === SESSION_LOAD) return t('error.sessionLoad')
  switch (err.status) {
    case 0:
      return t('error.network')
    case 401:
      return t('error.sessionExpired')
    case 404:
      return t('error.notFound')
    case 429: // only sign-in is limited (D55)
      return t('error.tooManySignIns')
    case 502:
      return t('error.aiFailed')
    case 504:
      return t('error.aiTimeout')
  }
  if (err.status >= 500) return t('error.server')
  return `${t('error.invalid')} ${t('error.details', { message: err.message })}`
}

export function aiReason(err: unknown): string | null {
  const body = err instanceof ApiError ? (err.body as { unsupported?: boolean; error?: string } | undefined) : undefined
  return body?.unsupported === true && typeof body.error === 'string' ? body.error : null
}
