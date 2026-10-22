import { readonly, ref } from 'vue'
import { api, ApiError } from './api'

export type Me = { id: number; email: string; name: string; llm_mode: 'mock' | 'real' }

const me = ref<Me | null>(null)
let loaded = false

export const session = readonly(me)

// loadSession asks the api who is signed in, once; null means signed out.
export async function loadSession(): Promise<Me | null> {
  if (loaded) return me.value
  try {
    me.value = await api<Me>('/api/me')
  } catch (err) {
    // Signed out, or the api is unreachable: either way the login page is next,
    // and it reports a server problem when the marketer tries to sign in.
    if (!(err instanceof ApiError)) throw err
    me.value = null
  }
  loaded = true
  return me.value
}

export async function login(email: string, password: string): Promise<Me | null> {
  await api('/api/login', { method: 'POST', body: { email, password } })
  loaded = false // /api/me also carries the AI mode for the banner
  return loadSession()
}

export async function logout() {
  try {
    await api('/api/logout', { method: 'POST' })
  } finally {
    clearSession()
  }
}

export function clearSession() {
  me.value = null
  loaded = true
}
