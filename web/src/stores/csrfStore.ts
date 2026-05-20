import { defineStore } from 'pinia'
import { joinBaseURL } from '@/lib/apiBase'

interface CSRFPrefetchResponse {
  csrf_token?: string
}

interface CSRFStoreState {
  tokenValue: string
  fetchInFlight: Promise<string> | null
}

/**
 * useCsrfStore caches the masked CSRF token used for unsafe API requests.
 */
export const useCsrfStore = defineStore('csrf', {
  state: (): CSRFStoreState => ({
    tokenValue: '',
    fetchInFlight: null,
  }),

  actions: {
    setToken(token: string): void {
      this.tokenValue = token
    },

    async ensureToken(): Promise<string> {
      if (this.tokenValue) {
        return this.tokenValue
      }
      if (this.fetchInFlight) {
        return this.fetchInFlight
      }

      const request = fetch(joinBaseURL('/auth/csrf'), {
        method: 'GET',
        credentials: 'include',
      })
        .then(async (response) => {
          if (!response.ok) {
            throw new Error(`CSRF prefetch failed: ${response.status}`)
          }
          const body = (await response.json()) as CSRFPrefetchResponse
          if (typeof body.csrf_token !== 'string' || body.csrf_token === '') {
            throw new Error('CSRF prefetch response did not include csrf_token')
          }
          this.setToken(body.csrf_token)
          return body.csrf_token
        })
        .finally(() => {
          if (this.fetchInFlight === request) {
            this.fetchInFlight = null
          }
        })

      this.fetchInFlight = request
      return request
    },
  },
})
