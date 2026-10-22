import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

let session: typeof import('./session')
const me = { id: 1, email: 'm@example.com', name: 'Demo Marketer', llm_mode: 'mock' }

beforeEach(async () => {
  vi.resetModules()
  session = await import('./session')
})
afterEach(() => vi.unstubAllGlobals())

function replies(...responses: (() => Response)[]) {
  const fetch = vi.fn(async () => (responses.shift() ?? (() => new Response(null, { status: 500 })))())
  vi.stubGlobal('fetch', fetch)
  return fetch
}

describe('loadSession', () => {
  it('remembers that nobody is signed in', async () => {
    const fetch = replies(() => Response.json({ error: 'login required' }, { status: 401 }))

    expect(await session.loadSession()).toBeNull()
    expect(await session.loadSession()).toBeNull()
    expect(fetch).toHaveBeenCalledOnce()
    expect(session.sessionProblem.value).toBe('')
  })

  // A server error is not "signed out": say so, and ask again next time.
  it('reports a server problem and does not cache it', async () => {
    replies(
      () => new Response('down', { status: 502 }),
      () => Response.json(me),
    )

    expect(await session.loadSession()).toBeNull()
    expect(session.sessionProblem.value).toBe('The server answered with an error (502).')
    expect(await session.loadSession()).toEqual(me)
    expect(session.sessionProblem.value).toBe('')
  })
})

describe('login', () => {
  it('fails clearly if the session cannot be loaded after signing in', async () => {
    replies(
      () => Response.json({ id: 1, email: me.email, name: me.name }),
      () => new Response('down', { status: 500 }),
    )

    await expect(session.login(me.email, 'pw')).rejects.toThrow('The server answered with an error (500).')
  })
})
