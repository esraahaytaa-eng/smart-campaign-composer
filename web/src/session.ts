import { readonly, ref } from 'vue'
import { api, ApiError } from './api'

export type Me = { id: number; email: string; name: string; llm_mode: 'mock' | 'real' }

const me = ref<Me | null>(null)
const problem = ref('')
let loaded = false

export const session = readonly(me)
// sessionProblem says why the session could not be checked (the api is down
// or failing); the login page shows it. Empty when signed in or signed out.
export const sessionProblem = readonly(problem)

// loadSession asks the api who is signed in; null means signed out. A clear
// "signed out" (401) is remembered; a server problem is not, so the next
// navigation asks again.
export async function loadSession(): Promise<Me | null> {
  if (loaded) return me.value
  try {
    me.value = await api<Me>('/api/me')
    problem.value = ''
    loaded = true
  } catch (err) {
    if (!(err instanceof ApiError)) throw err
    me.value = null
    loaded = err.status === 401
    problem.value = err.status === 401 ? '' : err.message
  }
  return me.value
}

export async function login(email: string, password: string): Promise<Me> {
  await api('/api/login', { method: 'POST', body: { email, password } })
  loaded = false // /api/me also carries the AI mode for the banner
  const signedIn = await loadSession()
  if (!signedIn) throw new ApiError(problem.value || 'Signed in, but the session could not be loaded. Try again.', 0)
  return signedIn
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
  problem.value = ''
  loaded = true
}
