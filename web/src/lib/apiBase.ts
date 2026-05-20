import { isAllowedApiHost } from '@/lib/apiHostAllowlist'

const BASE = '/api'
const API_HOST_STORAGE_KEY = 'flowstate-api-host'

/**
 * getBaseURL returns the API base URL, validated against the host
 * allowlist. A localStorage value that fails the allowlist is removed
 * and the safe BASE default is returned.
 */
function getBaseURL(): string {
  let stored: string | null = null
  try {
    stored = localStorage.getItem(API_HOST_STORAGE_KEY)
  } catch {
    return BASE
  }
  if (!stored) return BASE
  if (!isAllowedApiHost(stored)) {
    try {
      localStorage.removeItem(API_HOST_STORAGE_KEY)
    } catch {
      return BASE
    }
    console.warn(
      '[flowstate] cleared API host override that failed allowlist policy:',
      stored,
    )
    return BASE
  }
  return stored
}

/**
 * joinBaseURL prefixes an API path with the configured API base.
 */
export function joinBaseURL(path: string): string {
  const base = getBaseURL().replace(/\/$/, '')
  return `${base}${path}`
}
