/**
 * ProviderQuotaPanel.spec.ts — vitest spec for the Provider Quota
 * and Spend Visibility plan (May 2026) PR5a panel modal.
 *
 * Pins:
 *   - rate_limit variant renders the four windows + tightest summary.
 *   - token_spend variant renders native + USD + cap + period +
 *     thresholds + pricing source + observed-at + estimator-drift
 *     column.
 *   - token_spend uncapped (capMinor <= 0) disables the reset button.
 *   - not_configured variant renders the reason verbatim.
 *   - Reset button opens a confirmation modal.
 *   - Confirmed reset POSTs to /api/v1/providers/quota/reset with
 *     credentials: include AND emits 'reset' + 'close'.
 *   - 404 on reset surfaces a "Nothing to reset" toast.
 *   - 401 on reset (uniform B8 401) surfaces an error toast.
 *
 * Per memory feedback_response_ok_mock_gotcha — fetch mocks use real
 * Response objects so res.ok evaluates correctly.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { setActivePinia, createPinia } from 'pinia'
import ProviderQuotaPanel from './ProviderQuotaPanel.vue'
import type { ProviderQuotaEntry } from '@/api'

vi.mock('@/composables/useToast', () => ({
  showToast: vi.fn(),
}))

const rateLimitEntry: ProviderQuotaEntry = {
  provider: 'anthropic',
  accountHash: 'abc12345abc',
  model: 'claude-opus-4-7',
  observedAt: '2026-05-13T12:00:00Z',
  stale: false,
  storeBackend: 'memory',
  pricingSource: 'embedded',
  variant: 'rate_limit',
  rateLimit: {
    requests: { limit: 100, remaining: 42, reset: '2026-05-13T12:05:00Z' },
    tokens: { limit: 100000, remaining: 12000, reset: '2026-05-13T12:05:00Z' },
    input: { limit: 50000, remaining: 7000, reset: '2026-05-13T12:05:00Z' },
    output: { limit: 50000, remaining: 5000, reset: '2026-05-13T12:05:00Z' },
    tightestPercentRemaining: 12,
    tightestResetAt: '2026-05-13T12:05:00Z',
  },
  tokenSpend: null,
  notConfigured: null,
}

const tokenSpendEntry: ProviderQuotaEntry = {
  provider: 'openai',
  accountHash: 'def67890def',
  model: 'gpt-4o',
  observedAt: '2026-05-13T12:00:00Z',
  stale: false,
  storeBackend: 'memory',
  pricingSource: 'embedded',
  variant: 'token_spend',
  rateLimit: null,
  tokenSpend: {
    spentMinor: 1234,
    spentCurrency: 'USD',
    spentUsdMinor: 1234,
    capMinor: 10000,
    capCurrency: 'USD',
    period: 'month',
    periodStart: '2026-05-01T00:00:00Z',
    periodEnd: '2026-06-01T00:00:00Z',
    thresholdAmber: 80,
    thresholdRed: 95,
  },
  notConfigured: null,
}

const uncappedTokenSpendEntry: ProviderQuotaEntry = {
  ...tokenSpendEntry,
  tokenSpend: {
    ...tokenSpendEntry.tokenSpend!,
    capMinor: 0,
    capCurrency: '',
  },
}

const notConfiguredEntry: ProviderQuotaEntry = {
  provider: 'ollama',
  accountHash: '',
  model: 'llama3:8b',
  observedAt: '2026-05-13T12:00:00Z',
  stale: false,
  storeBackend: '',
  pricingSource: '',
  variant: 'not_configured',
  rateLimit: null,
  tokenSpend: null,
  notConfigured: { reason: 'local provider exposes no quota signal' },
}

describe('ProviderQuotaPanel', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  afterEach(() => {
    vi.unstubAllGlobals()
    vi.clearAllMocks()
  })

  describe('rate_limit branch', () => {
    it('renders the four windows + tightest summary', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: rateLimitEntry } })

      expect(wrapper.find('[data-testid="provider-quota-panel-rate-limit"]').exists()).toBe(true)
      expect(wrapper.find('[data-testid="provider-quota-panel-window-requests"]').text()).toContain(
        '100',
      )
      expect(wrapper.find('[data-testid="provider-quota-panel-window-tokens"]').text()).toContain(
        '100000',
      )
      expect(wrapper.find('[data-testid="provider-quota-panel-window-input"]').text()).toContain(
        '50000',
      )
      expect(wrapper.find('[data-testid="provider-quota-panel-window-output"]').text()).toContain(
        '50000',
      )
      expect(wrapper.find('[data-testid="provider-quota-panel-tightest"]').text()).toBe('12%')
    })

    it('does not render the token_spend reset button', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: rateLimitEntry } })
      expect(wrapper.find('[data-testid="provider-quota-panel-reset"]').exists()).toBe(false)
    })
  })

  describe('token_spend branch', () => {
    it('renders native + USD + cap + period + thresholds + pricing source + observed-at + estimator drift column', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })

      expect(
        wrapper.find('[data-testid="provider-quota-panel-token-spend"]').exists(),
      ).toBe(true)
      // Native = $12.34
      expect(wrapper.find('[data-testid="provider-quota-panel-spent-native"]').text()).toBe(
        '$12.34',
      )
      // USD equivalent (OD-6).
      expect(wrapper.find('[data-testid="provider-quota-panel-spent-usd"]').text()).toBe('$12.34')
      // Cap = $100.00
      expect(wrapper.find('[data-testid="provider-quota-panel-cap"]').text()).toBe('$100.00')
      expect(wrapper.find('[data-testid="provider-quota-panel-period"]').text()).toBe('month')
      expect(wrapper.find('[data-testid="provider-quota-panel-threshold-amber"]').text()).toBe(
        '80%',
      )
      expect(wrapper.find('[data-testid="provider-quota-panel-threshold-red"]').text()).toBe('95%')
      expect(
        wrapper.find('[data-testid="provider-quota-panel-pricing-source"]').text(),
      ).toBe('embedded')
      // OD-7 — estimator drift column is always present (the
      // visual contract is stable; populates when the wire shape
      // carries the measured estimator).
      expect(
        wrapper.find('[data-testid="provider-quota-panel-estimator-drift"]').exists(),
      ).toBe(true)
    })

    it('enables the reset button when capped', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      const btn = wrapper.find('[data-testid="provider-quota-panel-reset"]')
      expect(btn.exists()).toBe(true)
      expect((btn.element as HTMLButtonElement).disabled).toBe(false)
    })

    it('disables the reset button when uncapped', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: uncappedTokenSpendEntry } })
      const btn = wrapper.find('[data-testid="provider-quota-panel-reset"]')
      expect(btn.exists()).toBe(true)
      expect((btn.element as HTMLButtonElement).disabled).toBe(true)
      expect(wrapper.find('[data-testid="provider-quota-panel-cap"]').text()).toContain(
        'uncapped',
      )
    })
  })

  describe('not_configured branch', () => {
    it('renders the reason verbatim', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: notConfiguredEntry } })
      expect(
        wrapper.find('[data-testid="provider-quota-panel-not-configured"]').exists(),
      ).toBe(true)
      expect(wrapper.find('[data-testid="provider-quota-panel-reason"]').text()).toBe(
        'local provider exposes no quota signal',
      )
    })

    it('does not render the reset button', () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: notConfiguredEntry } })
      expect(wrapper.find('[data-testid="provider-quota-panel-reset"]').exists()).toBe(false)
    })
  })

  describe('reset flow', () => {
    let fetchMock: ReturnType<typeof vi.fn>

    beforeEach(() => {
      fetchMock = vi.fn()
      vi.stubGlobal('fetch', fetchMock)
    })

    it('opens a confirm modal when reset is clicked', async () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      expect(
        wrapper.find('[data-testid="provider-quota-panel-reset-confirm"]').exists(),
      ).toBe(false)

      await wrapper.find('[data-testid="provider-quota-panel-reset"]').trigger('click')
      await flushPromises()

      expect(
        wrapper.find('[data-testid="provider-quota-panel-reset-confirm"]').exists(),
      ).toBe(true)
    })

    it('cancel button dismisses the confirm modal without firing fetch', async () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      await wrapper.find('[data-testid="provider-quota-panel-reset"]').trigger('click')
      await wrapper
        .find('[data-testid="provider-quota-panel-reset-cancel"]')
        .trigger('click')
      await flushPromises()

      expect(
        wrapper.find('[data-testid="provider-quota-panel-reset-confirm"]').exists(),
      ).toBe(false)
      expect(fetchMock).not.toHaveBeenCalled()
    })

    it('confirmed reset POSTs to /api/v1/providers/quota/reset with credentials: include AND CSRF header', async () => {
      // Seed the _csrf cookie so withCsrfHeader injects the
      // X-CSRF-Token header on the unsafe POST request. PR3
      // discipline: GET passes through gorilla/csrf; POST
      // requires the header.
      Object.defineProperty(document, 'cookie', {
        writable: true,
        value: '_csrf=csrf-token-value',
      })

      fetchMock.mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'reset' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )

      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      await wrapper.find('[data-testid="provider-quota-panel-reset"]').trigger('click')
      await wrapper
        .find('[data-testid="provider-quota-panel-reset-confirm-button"]')
        .trigger('click')
      await flushPromises()

      expect(fetchMock).toHaveBeenCalledTimes(1)
      const [url, init] = fetchMock.mock.calls[0]
      expect(String(url)).toContain('/api/v1/providers/quota/reset')
      expect((init as RequestInit).method).toBe('POST')
      expect((init as RequestInit).credentials).toBe('include')
      const headers = (init as RequestInit).headers as Record<string, string>
      expect(headers['X-CSRF-Token']).toBe('csrf-token-value')
      expect(headers['Content-Type']).toBe('application/json')
      const body = JSON.parse(String((init as RequestInit).body))
      expect(body.provider).toBe('openai')
      expect(body.account_hash).toBe('def67890def')
      expect(body.model).toBe('gpt-4o')
    })

    it('emits reset + close events after successful reset', async () => {
      fetchMock.mockResolvedValueOnce(
        new Response(JSON.stringify({ status: 'reset' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
      )

      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      await wrapper.find('[data-testid="provider-quota-panel-reset"]').trigger('click')
      await wrapper
        .find('[data-testid="provider-quota-panel-reset-confirm-button"]')
        .trigger('click')
      await flushPromises()

      expect(wrapper.emitted('reset')).toHaveLength(1)
      expect(wrapper.emitted('close')).toHaveLength(1)
    })

    it('surfaces 404 as a default-variant toast and does not throw', async () => {
      const { showToast } = await import('@/composables/useToast')
      fetchMock.mockResolvedValueOnce(
        new Response('not_found\n', {
          status: 404,
          headers: { 'Content-Type': 'text/plain' },
        }),
      )

      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      await wrapper.find('[data-testid="provider-quota-panel-reset"]').trigger('click')
      await wrapper
        .find('[data-testid="provider-quota-panel-reset-confirm-button"]')
        .trigger('click')
      await flushPromises()

      expect(showToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringContaining('Nothing to reset'),
        }),
      )
    })

    it('surfaces 401 (uniform B8 401) as an error toast', async () => {
      const { showToast } = await import('@/composables/useToast')
      fetchMock.mockResolvedValueOnce(
        new Response('unauthenticated\n', {
          status: 401,
          headers: { 'Content-Type': 'text/plain' },
        }),
      )

      const wrapper = mount(ProviderQuotaPanel, { props: { entry: tokenSpendEntry } })
      await wrapper.find('[data-testid="provider-quota-panel-reset"]').trigger('click')
      await wrapper
        .find('[data-testid="provider-quota-panel-reset-confirm-button"]')
        .trigger('click')
      await flushPromises()

      expect(showToast).toHaveBeenCalledWith(
        expect.objectContaining({
          message: expect.stringContaining('401'),
          variant: 'error',
        }),
      )
    })
  })

  describe('close behaviour', () => {
    it('emits close when the close button is clicked', async () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: rateLimitEntry } })
      await wrapper.find('[data-testid="provider-quota-panel-close"]').trigger('click')
      expect(wrapper.emitted('close')).toHaveLength(1)
    })

    it('emits close when the backdrop is clicked', async () => {
      const wrapper = mount(ProviderQuotaPanel, { props: { entry: rateLimitEntry } })
      // Use .self trigger via .trigger on the backdrop element.
      await wrapper.find('[data-testid="provider-quota-panel-backdrop"]').trigger('click')
      expect(wrapper.emitted('close')).toHaveLength(1)
    })
  })
})
