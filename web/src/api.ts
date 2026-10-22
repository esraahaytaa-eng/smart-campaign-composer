// Thin JSON client for the Go api. The session lives in an HttpOnly cookie,
// so requests only need to stay same-origin (the Vite proxy in development).

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number, // 0 when the server could not be reached
    readonly body: unknown = undefined,
  ) {
    super(message)
  }
}

let onUnauthorized: () => void = () => {}

// The router registers how to react when the session has expired.
export function setUnauthorizedHandler(fn: () => void) {
  onUnauthorized = fn
}

type Options = { method?: 'GET' | 'POST' | 'PUT' | 'DELETE'; body?: unknown }

export async function api<T = unknown>(path: string, { method = 'GET', body }: Options = {}): Promise<T> {
  let res: Response
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
    })
  } catch {
    throw new ApiError("Can't reach the server. Check that it is running.", 0)
  }
  if (res.status === 204) return undefined as T
  const data: unknown = await res.json().catch(() => undefined)
  if (res.ok) return data as T
  // A 401 from login is a wrong password, and from /api/me "signed out",
  // which the login guard handles; elsewhere the session has expired.
  if (res.status === 401 && path !== '/api/login' && path !== '/api/me') onUnauthorized()
  const message =
    typeof data === 'object' && data !== null && typeof (data as { error?: unknown }).error === 'string'
      ? (data as { error: string }).error
      : `The server answered with an error (${res.status}).`
  throw new ApiError(message, res.status, data)
}
