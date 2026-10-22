import { afterEach, describe, expect, it, vi } from 'vitest'
import { api, ApiError, setUnauthorizedHandler } from './api'

function reply(status: number, body?: unknown) {
  return vi.fn(async () =>
    body === undefined ? new Response(null, { status }) : Response.json(body, { status }),
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  setUnauthorizedHandler(() => {})
})

describe('api', () => {
  it('sends JSON and returns the decoded answer', async () => {
    const fetch = reply(200, { id: 1 })
    vi.stubGlobal('fetch', fetch)

    await expect(api('/api/campaigns', { method: 'POST', body: { request: 'x' } })).resolves.toEqual({ id: 1 })
    expect(fetch).toHaveBeenCalledWith('/api/campaigns', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{"request":"x"}',
      credentials: 'same-origin',
    })
  })

  it('returns nothing for 204', async () => {
    vi.stubGlobal('fetch', reply(204))
    await expect(api('/api/campaigns/1', { method: 'DELETE', body: { version: 1 } })).resolves.toBeUndefined()
  })

  it('reports an expired session and throws', async () => {
    vi.stubGlobal('fetch', reply(401, { error: 'login required' }))
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)

    await expect(api('/api/campaigns')).rejects.toMatchObject({ status: 401 })
    expect(unauthorized).toHaveBeenCalledOnce()
  })

  it('does not treat a wrong password as an expired session', async () => {
    vi.stubGlobal('fetch', reply(401, { error: 'invalid email or password' }))
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)

    await expect(api('/api/login', { method: 'POST', body: {} })).rejects.toMatchObject({ status: 401 })
    expect(unauthorized).not.toHaveBeenCalled()
  })

  // The login guard asks /api/me itself; a 401 there just means signed out.
  it('leaves the session check to the guard', async () => {
    vi.stubGlobal('fetch', reply(401, { error: 'login required' }))
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)

    await expect(api('/api/me')).rejects.toMatchObject({ status: 401 })
    expect(unauthorized).not.toHaveBeenCalled()
  })

  it('carries the server message and body of an error', async () => {
    const body = { error: 'invalid audience rules', field_errors: [{ rule: 0, message: 'unknown city' }] }
    vi.stubGlobal('fetch', reply(422, body))

    const err = await api('/api/campaigns/1/rules', { method: 'PUT', body: {} }).catch((e: unknown) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ status: 422, message: 'invalid audience rules', body })
  })

  it('turns a network failure into a readable error', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => Promise.reject(new TypeError('Failed to fetch'))))

    await expect(api('/api/me')).rejects.toMatchObject({ status: 0, message: "Can't reach the server. Check that it is running." })
  })

  it('does not fail on a non-JSON error page', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response('<html>Bad Gateway</html>', { status: 502 })))

    await expect(api('/api/me')).rejects.toMatchObject({ status: 502, message: 'The server answered with an error (502).' })
  })
})
