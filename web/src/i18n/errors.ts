// Errors in the UI's language (D44, approach A): each situation the UI knows
// gets its own text; validation it can't foresee keeps the server's English
// words as details. AI output (an unsupported brief's reason) is never
// translated: aiReason returns it as written.
import { ApiError } from '../api'
import { t } from './index'

export function errorText(err: unknown): string {
  if (!(err instanceof ApiError)) return t('error.unknown')
  switch (err.status) {
    case 0:
      return t('error.network')
    case 401:
      return t('error.sessionExpired')
    case 404:
      return t('error.notFound')
    case 409:
      return t('error.changed')
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
