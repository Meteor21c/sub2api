import type { Router } from 'vue-router'

export const DEFAULT_AUTH_REDIRECT = '/dashboard'

const AUTH_ENTRY_PATHS = new Set(['/login', '/register'])

function containsControlCharacter(value: string): boolean {
  return Array.from(value).some((character) => {
    const code = character.charCodeAt(0)
    return code < 0x20 || code === 0x7f
  })
}

/**
 * Return a safe, existing frontend route for a post-authentication redirect.
 *
 * Redirect values can survive in old bookmarks, browser history, OAuth state,
 * or a stale login URL. Only same-origin application routes that are known to
 * this router are allowed to continue. This also prevents an open redirect.
 */
export function sanitizeRedirectPath(
  router: Pick<Router, 'resolve'>,
  path: unknown,
  fallback = DEFAULT_AUTH_REDIRECT
): string {
  const value = typeof path === 'string' ? path.trim() : ''

  if (
    !value ||
    !value.startsWith('/') ||
    value.startsWith('//') ||
    value.includes('://') ||
    value.includes('\\') ||
    containsControlCharacter(value)
  ) {
    return fallback
  }

  // Vue Router always provides resolve at runtime. Keeping this graceful
  // fallback makes lightweight component-test router stubs behave as before.
  if (typeof router.resolve !== 'function') {
    return value
  }

  try {
    const resolved = router.resolve(value)
    const isUnknownRoute =
      resolved.matched.length === 0 ||
      resolved.matched.some((record) => record.name === 'NotFound')

    if (isUnknownRoute || AUTH_ENTRY_PATHS.has(resolved.path)) {
      return fallback
    }
  } catch {
    return fallback
  }

  return value
}
