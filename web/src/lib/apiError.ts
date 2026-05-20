/**
 * ApiError carries the HTTP status alongside the user-facing API error
 * message.
 */
export class ApiError extends Error {
  status: number
  statusText: string

  constructor(message: string, status: number, statusText: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.statusText = statusText
  }
}

/**
 * apiErrorFromResponse builds a status-aware API error while preserving
 * the existing `<context>: <statusText>` message shape.
 */
export function apiErrorFromResponse(context: string, response: Response): ApiError {
  const statusText = response.statusText || `HTTP ${response.status}`
  return new ApiError(`${context}: ${statusText}`, response.status, response.statusText)
}

/**
 * isUnauthorizedError reports whether an error represents an auth/session
 * failure that should send the user to the login view.
 */
export function isUnauthorizedError(error: unknown): boolean {
  if (error instanceof ApiError) {
    return error.status === 401
  }
  if (!(error instanceof Error)) {
    return false
  }
  return /\b(?:401|Unauthorized|unauthenticated)\b/i.test(error.message)
}
