import { describe, expect, it } from 'vitest'
import { focusTarget } from './login'

// After a failed sign-in, focus goes where the marketer must act, so keyboard
// and screen-reader users hear the error.
describe('focusTarget', () => {
  it('picks the first invalid field', () => {
    expect(focusTarget({ email: 'Enter your work email.', password: 'Enter your password.' }, false)).toBe('email')
    expect(focusTarget({ email: '', password: 'Enter your password.' }, false)).toBe('password')
  })

  it('picks the email field after a server error', () => {
    expect(focusTarget({ email: '', password: '' }, true)).toBe('email')
  })

  it('leaves focus alone when nothing failed', () => {
    expect(focusTarget({ email: '', password: '' }, false)).toBeNull()
  })
})
