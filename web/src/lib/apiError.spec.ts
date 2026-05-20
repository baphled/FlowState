import { describe, expect, it } from 'vitest'
import { ApiError, apiErrorFromResponse, isUnauthorizedError } from './apiError'

describe('apiErrorFromResponse', () => {
  it('preserves the existing message shape while attaching the HTTP status', () => {
    const response = new Response('', { status: 401, statusText: 'Unauthorized' })
    const error = apiErrorFromResponse('Failed to fetch sessions', response)

    expect(error.message).toBe('Failed to fetch sessions: Unauthorized')
    expect(error.status).toBe(401)
    expect(error.statusText).toBe('Unauthorized')
  })
})

describe('isUnauthorizedError', () => {
  it('matches ApiError instances with HTTP 401 status', () => {
    expect(isUnauthorizedError(new ApiError('anything', 401, 'Unauthorized'))).toBe(true)
  })

  it('matches legacy plain Error messages that mention Unauthorized', () => {
    expect(isUnauthorizedError(new Error('Failed to fetch sessions: Unauthorized'))).toBe(true)
  })

  it('does not match unrelated network errors', () => {
    expect(isUnauthorizedError(new Error('network down'))).toBe(false)
  })
})
