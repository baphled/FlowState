import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useQuotaStore, snapshotKey, type ProviderQuotaSnapshot } from './quotaStore'
import type { SSEProviderQuotaEvent } from '@/lib/sseEvent'

/**
 * quotaStore behaviour specs — pin the Pinia store's contract for
 * the Provider Quota and Spend Visibility feature PR4a.
 *
 * Contract:
 *   - applyProviderQuotaEvent ingests an SSE event and stores a
 *     ProviderQuotaSnapshot under the `<provider>:<account>:<model>`
 *     key.
 *   - currentQuotaFor reads back by exact (provider, account, model)
 *     tuple.
 *   - anyQuotaFor returns the first match for (provider, model)
 *     regardless of account — the chip's fallback when accountHash
 *     is unknown.
 *   - reset clears all snapshots.
 *
 * The discriminator-union invariant is enforced by the typed event
 * coming in from sseEvent.ts; the store does not re-validate so a
 * malformed event passes through verbatim.
 */

function buildRateLimitEvent(): SSEProviderQuotaEvent {
  return {
    kind: 'provider_quota',
    provider: 'anthropic',
    accountHash: 'deadbeef',
    model: 'claude-opus-4-7',
    observedAt: '2026-05-13T12:00:00Z',
    stale: false,
    storeBackend: 'memory',
    pricingSource: '',
    variant: 'rate_limit',
    rateLimit: {
      requests: { limit: 1000, remaining: 750, reset: '2026-05-13T12:01:00Z' },
      tokens: { limit: 100000, remaining: 75000, reset: '2026-05-13T12:01:00Z' },
      input: { limit: -1, remaining: -1, reset: '' },
      output: { limit: -1, remaining: -1, reset: '' },
      tightestPercentRemaining: 75,
      tightestResetAt: '2026-05-13T12:01:00Z',
    },
    tokenSpend: null,
    notConfigured: null,
  }
}

function buildTokenSpendEvent(): SSEProviderQuotaEvent {
  return {
    kind: 'provider_quota',
    provider: 'anthropic',
    accountHash: 'deadbeef',
    model: 'claude-opus-4-7',
    observedAt: '2026-05-13T12:00:00Z',
    stale: false,
    storeBackend: 'memory',
    pricingSource: 'flowstate-default-v1',
    variant: 'token_spend',
    rateLimit: null,
    tokenSpend: {
      spentMinor: 241,
      spentCurrency: 'USD',
      spentUsdMinor: 241,
      capMinor: 5000,
      capCurrency: 'USD',
      period: 'monthly',
      periodStart: '2026-05-01T00:00:00Z',
      periodEnd: '2026-06-01T00:00:00Z',
      thresholdAmber: 80,
      thresholdRed: 95,
    },
    notConfigured: null,
  }
}

function buildNotConfiguredEvent(): SSEProviderQuotaEvent {
  return {
    kind: 'provider_quota',
    provider: 'ollama',
    accountHash: '',
    model: 'llama3',
    observedAt: '2026-05-13T12:00:00Z',
    stale: false,
    storeBackend: 'memory',
    pricingSource: '',
    variant: 'not_configured',
    rateLimit: null,
    tokenSpend: null,
    notConfigured: { reason: 'local-model' },
  }
}

describe('quotaStore', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  describe('snapshotKey helper', () => {
    it('builds the canonical "<provider>:<account>:<model>" key form', () => {
      expect(snapshotKey('anthropic', 'deadbeef', 'claude-opus-4-7'))
        .toBe('anthropic:deadbeef:claude-opus-4-7')
    })

    it('tolerates empty accountHash (ollama-style no-key providers)', () => {
      expect(snapshotKey('ollama', '', 'llama3')).toBe('ollama::llama3')
    })
  })

  describe('applyProviderQuotaEvent — RateLimit branch', () => {
    it('stores a RateLimit snapshot under (provider, account, model)', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent(buildRateLimitEvent())
      const snap = store.currentQuotaFor('anthropic', 'deadbeef', 'claude-opus-4-7')
      expect(snap).not.toBeNull()
      expect(snap?.variant).toBe('rate_limit')
      expect(snap?.rateLimit?.tightestPercentRemaining).toBe(75)
      expect(snap?.tokenSpend).toBeNull()
      expect(snap?.notConfigured).toBeNull()
    })
  })

  describe('applyProviderQuotaEvent — TokenSpend branch', () => {
    it('stores a TokenSpend snapshot with figures preserved', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent(buildTokenSpendEvent())
      const snap = store.currentQuotaFor('anthropic', 'deadbeef', 'claude-opus-4-7')
      expect(snap?.variant).toBe('token_spend')
      expect(snap?.tokenSpend?.spentMinor).toBe(241)
      expect(snap?.tokenSpend?.capMinor).toBe(5000)
      expect(snap?.tokenSpend?.thresholdAmber).toBe(80)
      expect(snap?.tokenSpend?.thresholdRed).toBe(95)
      expect(snap?.rateLimit).toBeNull()
    })

    it('overwrites a prior TokenSpend snapshot for the same key (later event wins)', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent(buildTokenSpendEvent())
      store.applyProviderQuotaEvent({
        ...buildTokenSpendEvent(),
        tokenSpend: {
          spentMinor: 482,
          spentCurrency: 'USD',
          spentUsdMinor: 482,
          capMinor: 5000,
          capCurrency: 'USD',
          period: 'monthly',
          periodStart: '2026-05-01T00:00:00Z',
          periodEnd: '2026-06-01T00:00:00Z',
          thresholdAmber: 80,
          thresholdRed: 95,
        },
      })
      const snap = store.currentQuotaFor('anthropic', 'deadbeef', 'claude-opus-4-7')
      expect(snap?.tokenSpend?.spentMinor).toBe(482)
    })

    it('partitions snapshots by accountHash (distinct keys for distinct accounts)', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent({ ...buildTokenSpendEvent(), accountHash: 'acc-A' })
      store.applyProviderQuotaEvent({
        ...buildTokenSpendEvent(),
        accountHash: 'acc-B',
        tokenSpend: {
          spentMinor: 482,
          spentCurrency: 'USD',
          spentUsdMinor: 482,
          capMinor: 5000,
          capCurrency: 'USD',
          period: 'monthly',
          periodStart: '2026-05-01T00:00:00Z',
          periodEnd: '2026-06-01T00:00:00Z',
          thresholdAmber: 80,
          thresholdRed: 95,
        },
      })
      const a = store.currentQuotaFor('anthropic', 'acc-A', 'claude-opus-4-7')
      const b = store.currentQuotaFor('anthropic', 'acc-B', 'claude-opus-4-7')
      expect(a?.tokenSpend?.spentMinor).toBe(241)
      expect(b?.tokenSpend?.spentMinor).toBe(482)
    })
  })

  describe('applyProviderQuotaEvent — NotConfigured branch', () => {
    it('stores a NotConfigured snapshot with the Reason verbatim', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent(buildNotConfiguredEvent())
      const snap = store.currentQuotaFor('ollama', '', 'llama3')
      expect(snap?.variant).toBe('not_configured')
      expect(snap?.notConfigured?.reason).toBe('local-model')
    })
  })

  describe('currentQuotaFor', () => {
    it('returns null when no snapshot has been seen for the tuple', () => {
      const store = useQuotaStore()
      expect(store.currentQuotaFor('anthropic', 'deadbeef', 'claude-opus-4-7')).toBeNull()
    })

    it('returns null for a different (provider, account, model) tuple', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent(buildTokenSpendEvent())
      expect(store.currentQuotaFor('anthropic', 'different-account', 'claude-opus-4-7')).toBeNull()
    })
  })

  describe('anyQuotaFor', () => {
    it('returns the first match for (provider, model) regardless of accountHash', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent({ ...buildTokenSpendEvent(), accountHash: 'some-account' })
      const snap = store.anyQuotaFor('anthropic', 'claude-opus-4-7')
      expect(snap?.variant).toBe('token_spend')
    })

    it('returns null when no matching (provider, model) entry exists', () => {
      const store = useQuotaStore()
      expect(store.anyQuotaFor('anthropic', 'claude-opus-4-7')).toBeNull()
    })

    it('skips snapshots whose model does not match', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent({
        ...buildTokenSpendEvent(),
        model: 'claude-sonnet-4-5',
      })
      expect(store.anyQuotaFor('anthropic', 'claude-opus-4-7')).toBeNull()
    })
  })

  describe('reset', () => {
    it('clears all snapshots so the chip falls back to its bootstrap empty state', () => {
      const store = useQuotaStore()
      store.applyProviderQuotaEvent(buildRateLimitEvent())
      store.applyProviderQuotaEvent(buildNotConfiguredEvent())
      expect(Object.keys(store.snapshots).length).toBe(2)
      store.reset()
      expect(Object.keys(store.snapshots).length).toBe(0)
      expect(store.currentQuotaFor('anthropic', 'deadbeef', 'claude-opus-4-7')).toBeNull()
    })
  })

  describe('Pinia post-mount seed gotcha (memory feedback_pinia_onmounted_clobbers_seed)', () => {
    it('a snapshot dispatched after mount is preserved (no clobbering)', async () => {
      // The chip subscribes to the store reactively; this spec
      // mirrors the post-mount seed pattern used in QuotaChip.spec.ts
      // and asserts the store does not lose the dispatch.
      const store = useQuotaStore()
      // Simulate a post-mount seed via a microtask boundary.
      await Promise.resolve()
      store.applyProviderQuotaEvent(buildTokenSpendEvent())
      await Promise.resolve()
      const snap: ProviderQuotaSnapshot | null = store.currentQuotaFor(
        'anthropic',
        'deadbeef',
        'claude-opus-4-7',
      )
      expect(snap).not.toBeNull()
      expect(snap?.variant).toBe('token_spend')
    })
  })
})
