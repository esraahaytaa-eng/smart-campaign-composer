import { describe, expect, it } from 'vitest'
import { decide, safeRedirect } from './guard'

const page = (fullPath: string, isPublic = false, redirect?: string) => ({
  fullPath,
  meta: { public: isPublic },
  query: redirect === undefined ? {} : { redirect },
})

describe('decide', () => {
  it('sends a signed-out visitor to the login page, remembering where they were going', () => {
    expect(decide(page('/campaigns/7?tab=content'), false)).toEqual({
      name: 'login',
      query: { redirect: '/campaigns/7?tab=content' },
    })
  })

  it('does not add a redirect for the start page', () => {
    expect(decide(page('/'), false)).toEqual({ name: 'login' })
  })

  it('lets a signed-in marketer through', () => {
    expect(decide(page('/campaigns'), true)).toBe(true)
  })

  it('shows the login page to a signed-out visitor', () => {
    expect(decide(page('/login', true), false)).toBe(true)
  })

  it('moves a signed-in marketer off the login page to where they were going', () => {
    expect(decide(page('/login', true, '/campaigns/3'), true)).toBe('/campaigns/3')
    expect(decide(page('/login', true), true)).toBe('/')
  })

  it('never redirects off the site after login', () => {
    expect(decide(page('/login', true, 'https://evil.example'), true)).toBe('/')
  })
})

describe('safeRedirect', () => {
  it('accepts paths on this site', () => {
    expect(safeRedirect('/campaigns/3?x=1')).toBe('/campaigns/3?x=1')
  })

  it.each(['//evil.example', '/\\evil.example', 'https://evil.example', 'javascript:alert(1)', 'campaigns', '', undefined, ['/a']])(
    'rejects %j',
    (value) => {
      expect(safeRedirect(value)).toBeNull()
    },
  )
})
