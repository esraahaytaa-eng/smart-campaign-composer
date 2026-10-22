import type { RouteLocationRaw } from 'vue-router'

type Target = { fullPath: string; meta: { public?: boolean }; query: Record<string, unknown> }

// decide is the login guard: signed-out visitors go to the login page with
// the page they wanted; signed-in marketers never see the login page.
export function decide(to: Target, signedIn: boolean): RouteLocationRaw | true {
  if (to.meta.public) return signedIn ? (safeRedirect(to.query.redirect) ?? '/') : true
  if (signedIn) return true
  return to.fullPath === '/' ? { name: 'login' } : { name: 'login', query: { redirect: to.fullPath } }
}

// safeRedirect allows only paths on this site, so a crafted login link can't
// send the marketer elsewhere after signing in.
export function safeRedirect(value: unknown): string | null {
  if (typeof value !== 'string' || !value.startsWith('/') || value.startsWith('//') || value.startsWith('/\\')) return null
  return value
}
