import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, type Router } from 'vue-router'

// The router module keeps the session in module state, so every test loads
// fresh copies of the modules.
let router: Router
let api: typeof import('./api')
let session: typeof import('./session')

type Route = Record<string, (init?: RequestInit) => Response>
let routes: Route
const calls: string[] = []

beforeEach(async () => {
  vi.resetModules()
  calls.length = 0
  routes = {}
  vi.stubGlobal(
    'fetch',
    vi.fn(async (path: string, init?: RequestInit) => {
      calls.push(`${init?.method ?? 'GET'} ${path}`)
      const handle = routes[`${init?.method ?? 'GET'} ${path}`]
      return handle ? handle(init) : Response.json({ error: 'login required' }, { status: 401 })
    }),
  )
  api = await import('./api')
  session = await import('./session')
  router = (await import('./router')).createAppRouter(createMemoryHistory())
})

afterEach(() => vi.unstubAllGlobals())

const me = { id: 1, email: 'm@example.com', name: 'Demo Marketer', llm_mode: 'mock' }
const signedIn = () => {
  routes['GET /api/me'] = () => Response.json(me)
}

describe('router', () => {
  it('sends a signed-out deep link to the login page with the return address', async () => {
    await router.push('/campaigns?status=draft')

    expect(router.currentRoute.value.name).toBe('login')
    expect(router.currentRoute.value.query.redirect).toBe('/campaigns?status=draft')
  })

  it('returns to the requested page after signing in', async () => {
    await router.push('/campaigns')
    routes['POST /api/login'] = () => Response.json({ id: 1, email: me.email, name: me.name })
    signedIn()

    await session.login(me.email, 'pw')
    await router.replace(String(router.currentRoute.value.query.redirect)) // what the login page does

    expect(router.currentRoute.value.fullPath).toBe('/campaigns')
    expect(session.session.value?.llm_mode).toBe('mock')
  })

  it('checks the session once, not on every navigation', async () => {
    signedIn()
    await router.push('/')
    await router.push('/campaigns')
    await router.push('/')

    expect(calls.filter((c) => c === 'GET /api/me')).toHaveLength(1)
  })

  it('sends the marketer to sign in when a call finds the session expired', async () => {
    signedIn()
    await router.push('/campaigns')
    routes['GET /api/me'] = () => Response.json({ error: 'login required' }, { status: 401 })

    await expect(api.api('/api/campaigns')).rejects.toMatchObject({ status: 401 })
    await vi.waitFor(() => expect(router.currentRoute.value.name).toBe('login'))

    expect(router.currentRoute.value.query.redirect).toBe('/campaigns')
    expect(session.session.value).toBeNull()
  })

  it('needs a new sign-in after logging out', async () => {
    signedIn()
    await router.push('/')
    routes['POST /api/logout'] = () => new Response(null, { status: 204 })

    await session.logout()
    await router.push('/campaigns')

    expect(router.currentRoute.value.name).toBe('login')
  })
})
