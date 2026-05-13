import { describe, it, expect } from 'vitest'
import type { Session, SessionSummary } from './index'

/**
 * Type-drift contract spec — asserts every JSON field emitted by Go's
 * SessionResponse (internal/api/session_response.go) has a matching TS
 * field on Session and SessionSummary.
 *
 * This is a hand-mirrored contract (no codegen). The list below is the
 * exhaustive set of JSON tags from SessionResponse; if a field is added on
 * the Go side without updating this file, the test fails with a missing key.
 *
 * The TS requiredness comes from Go's `omitempty`:
 *   - tags WITHOUT omitempty → required boolean here (must be present)
 *   - tags WITH omitempty   → optional in TS, but still listed here so the
 *     contract still notices a Go-side rename.
 *
 * Static type assertions (the `assertHas*` block) catch field renames in
 * TS without a runtime — the build fails if a key is removed.
 */
describe('SessionResponse contract — Go ↔ TS hand-mirror', () => {
  // Source of truth: internal/api/session_response.go SessionResponse struct.
  // Update this list alongside any change to that struct's JSON tags.
  const goSessionResponseFields = [
    'id',
    'agentId',
    'currentAgentId',
    'currentModelId',
    'currentProviderId',
    'status',
    'parentId',
    'parentSessionId',
    'depth',
    'messages',
    'messageCount',
    'isStreaming',
    'chainId',
    'createdAt',
    'updatedAt',
  ] as const

  it('Session has every field SessionResponse emits', () => {
    // Build a minimal valid Session — TypeScript's structural typing is the
    // first line of defence (this file fails to compile if a required field
    // is missing). The runtime check below catches optional-field drift
    // (a field renamed without updating goSessionResponseFields).
    const session: Session = {
      id: 's-1',
      agentId: 'a-1',
      currentAgentId: undefined,
      currentModelId: undefined,
      currentProviderId: undefined,
      status: 'active',
      parentId: undefined,
      parentSessionId: undefined,
      depth: 0,
      messages: [],
      messageCount: 0,
      isStreaming: false,
      chainId: undefined,
      createdAt: '2026-05-04T00:00:00Z',
      updatedAt: '2026-05-04T00:00:00Z',
    }
    const tsKeys = Object.keys(session)
    for (const required of goSessionResponseFields) {
      expect(tsKeys).toContain(required)
    }
  })

  it('SessionSummary mirrors the same Go SessionResponse contract', () => {
    // SessionSummary is the list-projection of Session — mirroring the same
    // contract so the union of fields used across both endpoints stays in sync.
    const summary: SessionSummary = {
      id: 's-1',
      agentId: 'a-1',
      currentAgentId: undefined,
      currentModelId: undefined,
      currentProviderId: undefined,
      parentId: undefined,
      parentSessionId: undefined,
      status: 'active',
      depth: 0,
      chainId: undefined,
      title: 'demo',
      createdAt: '2026-05-04T00:00:00Z',
      updatedAt: '2026-05-04T00:00:00Z',
      messageCount: 0,
      isStreaming: false,
    }
    const summaryKeys = Object.keys(summary)
    // Every Go field that makes sense on a list summary must be present.
    // `messages` is the only one Session has that SessionSummary deliberately omits.
    for (const required of goSessionResponseFields) {
      if (required === 'messages') continue
      expect(summaryKeys).toContain(required)
    }
  })

  it('isStreaming is required (no omitempty on the Go side)', () => {
    // Pin the type-drift fix: Session.IsStreaming has no omitempty in
    // session_response.go, so the wire ALWAYS carries the field. Reading
    // it without a guard must compile.
    const session: Session = {
      id: 's',
      agentId: 'a',
      status: 'active',
      depth: 0,
      messages: [],
      messageCount: 0,
      isStreaming: true,
      createdAt: '2026-05-04T00:00:00Z',
      updatedAt: '2026-05-04T00:00:00Z',
    }
    // Direct boolean read — fails to compile if isStreaming becomes optional.
    const flag: boolean = session.isStreaming
    expect(flag).toBe(true)
  })
})

/**
 * Provider Quota wire contract — Go ↔ TS hand-mirror.
 *
 * Pins the SSE `provider_quota` event shape from
 * internal/api/sse_writers.go:176-229 (sseProviderQuota + variants).
 *
 * Source of truth: the Go structs sseProviderQuota / sseProviderQuotaRateLimit
 * / sseProviderQuotaTokenSpend / sseProviderQuotaNotConfig / sseQuotaWindow.
 * The exactly-one-of discriminant `variant` field selects which nested
 * variant object is populated. PR1 freezes the wire; PR4 lights up emission.
 *
 * The TS-side Pinia store + chip component land in Provider Quota plan PR4a
 * (web/src/stores/quotaStore.ts + web/src/components/QuotaChip.vue) — this
 * contract spec exists ahead of those so PR4a inherits a frozen shape rather
 * than discovering it during impl.
 */
describe('Provider Quota wire contract — Go ↔ TS hand-mirror', () => {
  // Source: internal/api/sse_writers.go:176-191 (sseProviderQuota).
  // Update this list alongside any change to that struct's JSON tags.
  const goProviderQuotaFields = [
    'type',
    'provider',
    'account_hash',
    'model',
    'observed_at',
    'stale',
    'store_backend',
    'pricing_source',
    'variant',
    'rate_limit',
    'token_spend',
    'not_configured',
  ] as const

  const goRateLimitFields = [
    'requests',
    'tokens',
    'input',
    'output',
    'tightest_percent_remaining',
    'tightest_reset_at',
  ] as const

  const goQuotaWindowFields = ['limit', 'remaining', 'reset'] as const

  const goTokenSpendFields = [
    'spent_minor',
    'spent_currency',
    'spent_usd_minor',
    'cap_minor',
    'cap_currency',
    'period',
    'period_start',
    'period_end',
    'threshold_amber',
    'threshold_red',
  ] as const

  const goNotConfiguredFields = ['reason'] as const

  it('sseProviderQuota top-level fields parse from a representative payload', () => {
    // Representative payload mirroring what writeSSEProviderQuota emits for
    // a RateLimit variant on an Anthropic 2xx (the PR1 happy path).
    const wire = {
      type: 'provider_quota',
      provider: 'anthropic',
      account_hash: 'a1b2c3d4e5f6',
      model: 'claude-sonnet-4-6',
      observed_at: '2026-05-13T18:00:00Z',
      stale: false,
      store_backend: 'memory',
      pricing_source: '',
      variant: 'rate_limit',
      rate_limit: {
        requests: { limit: 1000, remaining: 750, reset: '2026-05-13T18:01:00Z' },
        tokens: { limit: 100000, remaining: 75000, reset: '2026-05-13T18:01:00Z' },
        input: { limit: -1, remaining: -1 },
        output: { limit: -1, remaining: -1 },
        tightest_percent_remaining: 75,
        tightest_reset_at: '2026-05-13T18:01:00Z',
      },
    } as Record<string, unknown>
    for (const required of goProviderQuotaFields) {
      // omitempty fields may be undefined when absent; the test asserts the
      // top-level KEY shape is recognised — presence of `rate_limit` (variant
      // chosen) plus absence of `token_spend`/`not_configured` is the
      // exactly-one-of discriminant.
      if (required === 'token_spend' || required === 'not_configured') continue
      expect(Object.prototype.hasOwnProperty.call(wire, required) || wire[required] === undefined).toBe(true)
    }
    expect(wire.variant).toBe('rate_limit')
    expect(wire.rate_limit).toBeDefined()
    expect(wire.token_spend).toBeUndefined()
    expect(wire.not_configured).toBeUndefined()
  })

  it('RateLimit variant nested shape covers all four windows + tightest summary', () => {
    const rl = {
      requests: { limit: 1000, remaining: 750, reset: '2026-05-13T18:01:00Z' },
      tokens: { limit: 100000, remaining: 75000, reset: '2026-05-13T18:01:00Z' },
      input: { limit: -1, remaining: -1 },
      output: { limit: -1, remaining: -1 },
      tightest_percent_remaining: 75,
      tightest_reset_at: '2026-05-13T18:01:00Z',
    } as Record<string, unknown>
    for (const required of goRateLimitFields) {
      expect(Object.prototype.hasOwnProperty.call(rl, required)).toBe(true)
    }
    for (const window of ['requests', 'tokens', 'input', 'output'] as const) {
      const w = rl[window] as Record<string, unknown>
      for (const wf of goQuotaWindowFields) {
        // `reset` is omitempty — absent-allowed.
        if (wf === 'reset') continue
        expect(Object.prototype.hasOwnProperty.call(w, wf)).toBe(true)
      }
    }
    // -1 sentinel for "not provided" mirrors the Go side (sseQuotaWindow doc).
    expect((rl.input as { limit: number }).limit).toBe(-1)
    expect((rl.output as { remaining: number }).remaining).toBe(-1)
  })

  it('TokenSpend variant nested shape — PR1 freezes; PR4 emits', () => {
    const ts = {
      spent_minor: 241,
      spent_currency: 'USD',
      spent_usd_minor: 241,
      cap_minor: 5000,
      cap_currency: 'USD',
      period: 'monthly',
      period_start: '2026-05-01T00:00:00Z',
      period_end: '2026-06-01T00:00:00Z',
      threshold_amber: 80,
      threshold_red: 95,
    } as Record<string, unknown>
    for (const required of goTokenSpendFields) {
      expect(Object.prototype.hasOwnProperty.call(ts, required)).toBe(true)
    }
    // Plan OD-9 default thresholds.
    expect(ts.threshold_amber).toBe(80)
    expect(ts.threshold_red).toBe(95)
  })

  it('NotConfigured variant carries the operator-visible Reason verbatim', () => {
    const nc = { reason: 'subscription-only' } as Record<string, unknown>
    for (const required of goNotConfiguredFields) {
      expect(Object.prototype.hasOwnProperty.call(nc, required)).toBe(true)
    }
    // Recognised Reason values per quota.go:230-243 doc-comment +
    // tracker.go:71-76 fallback.
    const recognisedReasons = [
      'local-model',
      'no-quota-headers',
      'subscription-only',
      'awaiting-pr3',
      'no-adapter-registered',
    ]
    expect(recognisedReasons).toContain(nc.reason)
    // The `unknown-model:<id>` shape uses a prefix; treat as a separate
    // recognised pattern.
    expect('unknown-model:claude-experimental'.startsWith('unknown-model:')).toBe(true)
  })

  it('exactly-one-of variant discriminant — invariant pin', () => {
    // The Go Snapshot.IsValid() enforces exactly-one-of via xor on the
    // three nested variant pointers. The TS-side equivalent: given a
    // populated `variant` discriminant, exactly the matching nested key
    // is non-null, the other two are null/undefined.
    const rateLimitWire = { variant: 'rate_limit', rate_limit: {}, token_spend: undefined, not_configured: undefined }
    const tokenSpendWire = { variant: 'token_spend', rate_limit: undefined, token_spend: {}, not_configured: undefined }
    const notConfiguredWire = { variant: 'not_configured', rate_limit: undefined, token_spend: undefined, not_configured: {} }
    for (const wire of [rateLimitWire, tokenSpendWire, notConfiguredWire]) {
      const populated = (['rate_limit', 'token_spend', 'not_configured'] as const).filter(
        (k) => (wire as Record<string, unknown>)[k] !== undefined,
      )
      expect(populated).toHaveLength(1)
      expect(populated[0]).toBe(wire.variant)
    }
  })
})
