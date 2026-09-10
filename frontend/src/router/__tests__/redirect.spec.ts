import { createMemoryHistory, createRouter } from 'vue-router'
import { describe, expect, it } from 'vitest'
import { sanitizeRedirectPath } from '@/router/redirect'

const component = { template: '<div />' }

const router = createRouter({
  history: createMemoryHistory(),
  routes: [
    { path: '/dashboard', name: 'Dashboard', component },
    { path: '/keys', name: 'Keys', component },
    { path: '/login', name: 'Login', component },
    { path: '/register', name: 'Register', component },
    { path: '/:pathMatch(.*)*', name: 'NotFound', component }
  ]
})

describe('sanitizeRedirectPath', () => {
  it('keeps an existing route and its query/hash', () => {
    expect(sanitizeRedirectPath(router, '/keys?tab=active#recent')).toBe(
      '/keys?tab=active#recent'
    )
  })

  it.each([
    '/usage-logs/common?start_timestamp=2026-01-01',
    '/dashboard/overview',
    'https://old-newapi.example.com/dashboard',
    '//old-newapi.example.com/dashboard',
    '/dashboard\\\\external'
  ])('falls back for an invalid redirect: %s', (path) => {
    expect(sanitizeRedirectPath(router, path)).toBe('/dashboard')
  })

  it.each(['/login', '/register', '', null, [' /keys']])(
    'falls back for an auth entry or invalid value: %s',
    (path) => {
      expect(sanitizeRedirectPath(router, path)).toBe('/dashboard')
    }
  )
})
