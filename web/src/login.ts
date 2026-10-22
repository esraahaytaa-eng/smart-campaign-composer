export type LoginField = 'email' | 'password'

// focusTarget is where focus goes after a failed sign-in: the first invalid
// field, or the email field after a server error, so the error is announced.
export function focusTarget(errors: Record<LoginField, string>, serverError: boolean): LoginField | null {
  if (errors.email) return 'email'
  if (errors.password) return 'password'
  return serverError ? 'email' : null
}
