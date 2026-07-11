# ADR 002: Unified Rate-Limit Visibility — Merge Cooldown and Window Reset into Single Query Surface

**Title**: Unified Rate-Limit Visibility — Merge Cooldown and Window Reset into Single Query Surface

## Context

FlowState exposes rate-limit information through two separate signals, but consumers need both to answer the complete question "what is the availability status of this provider/model?"

### Signal A: Rate-Limit Window Resets (Quota Tracker)

The `provider_quota` SSE chunk and `GET /api/v1/providers/quota` response carry a `rate_limit` variant with four windows (requests, tokens, input, output). Each window has:

```json
{
  "limit": 1000,
  "remaining": 0,
  "reset": "2026-07-11T12:05:00Z"
}
```

And a summary:

```json
{
  "tightest_percent_remaining": 0,
  "tightest_reset_at": "2026-07-11T12:05:00Z"
}
```

These come from the provider's response headers (`anthropic-ratelimit-*`, `x-ratelimit-*`, etc.) and reflect the provider-side quota window. When `remaining = 0` on the tightest window, the provider will reject requests until `tightest_reset_at`.

### Signal B: Failover Cooldown Expiry (HealthManager)

Separately, the failover system (`internal/plugin/failover/healthmanager.go`) tracks per-(provider, model) cooldowns. When the provider returns a 429 with a `retry-after` header, the failover system stores the expiry time in its in-memory map and in `~/.cache/flowstate/provider-health.json`. The failover stream hook uses this to skip rate-limited providers during fallback.

### The Gap

Today, these two signals are not unified:

- A provider may have `remaining > 0` on all windows (quota tracker says "no problem") but still be rate-limited in the failover system (because the carrier returned a 429 with `retry-after` that the quota tracker's success-path headers never carried, or the failover system applied a default cooldown for a non-429 error).

- Conversely, a provider may have `remaining = 0` on the tightest window but NOT be in the failover cooldown map (because the failover's `RetryAfterDetector` didn't see a 429/retry-after — the provider's response headers carried the zero remaining but no error was raised).

- The FE currently has to subscribe to two event streams (`provider_quota` for window resets and `provider_changed` for failover transitions) to build a complete picture.

- There is no single "is this provider healthy?" boolean or "rate-limited until" timestamp that merges both signals.

### What We Want

A single query surface that lets a consumer (FE chip, CLI command, REST API caller) ask:

> "What is the current availability status of provider X / model Y?"

And get back:

- The quota windows and remaining budgets (existing)
- The cumulative spend and cap (existing)
- Whether the failover system considers this provider rate-limited (new)
- When the failover cooldown expires (new)
- A synthesised "overall status" that merges both signals (new)

## Options

### Option A: Stitch at the Engine Layer (ADR 001 approach)

Add a `RateLimitedUntil time.Time` field to `quota.Snapshot`. The engine's `buildProviderQuotaChunk` and `QuotaSnapshots()` stamp it from `failoverManager.Health().RateLimitedUntil(provider, model)`. The FE consumes it alongside the existing `rate_limit` / `token_spend` variants.

Sub-option: add a synthesised `status` field to the wire payload — one of `"healthy"`, `"rate_limited"` (failover cooldown active), `"exhausted"` (window remaining = 0), `"spent"` (cap reached).

### Option B: SSE Side-Channel

Emit a new `provider_cooldown` SSE event whenever the failover health state changes. The FE merges it with `provider_quota` events client-side.

- **Pros**: Clean separation of concerns.
- **Cons**: FE complexity increases; two event streams to manage; timing issues between events.

### Option C: Enrich the Turn Polling Response

Add a `cooldowns` map to the `GET /turns/{turn_id}` response but keep the SSE streams separate.

- **Pros**: Polling consumers get the full picture.
- **Cons**: SSE-only consumers still need two subscriptions. Inconsistent.

## Decision

**Adopt Option A (ADR 001 approach) with the status synthesis sub-option, AND a dedicated SSE side-channel for real-time cooldown notifications (Option B complement).**

These are complementary — Option A provides the unified data on all existing query surfaces; the SSE side-channel provides sub-second push when status transitions occur. Neither replaces the other.

### Part A: Unified Fields on All Existing Surfaces (DONE)

1. Add `RateLimitedUntil time.Time` to `quota.Snapshot`. Zero = not rate-limited by the failover system.

2. The engine stamps this field from `failoverManager.Health().RateLimitedUntil(provider, model)` in two places:
   - `buildProviderQuotaChunk` / `buildProviderQuotaChunkExplicit` — for the `provider_quota` chunk in-stream
   - `QuotaSnapshots()` — for the REST API aggregator and turn polling

3. Add a synthesised `status` field to the wire payload (`providerQuotaPayload`) that combines the three signals:
   - `"rate_limited"` — `RateLimitedUntil` is set and in the future (failover cooldown active)
   - `"exhausted"` — `tightest_percent_remaining == 0` on the rate-limit variant (window exhausted but no failover cooldown)
   - `"spent"` — `token_spend` variant with `Spent >= Cap` (cap reached)
   - `"healthy"` — none of the above

4. Derive `rate_limited_until` from the `RateLimitedUntil` field on the snapshot, serialised as an RFC 3339 string. Omitted when zero.

### Part B: SSE Side-Channel for Real-Time Status (NEW)

Add a dedicated `GET /api/v1/providers/status/stream` SSE endpoint that delivers lightweight `provider_status` events in real time. This is a separate stream from the retired `/sessions/{id}/stream` — it carries no message content and has no coupling to the turn lifecycle.

**Event shape:**
```
event: provider_status
data: {"provider":"anthropic","model":"claude-sonnet-4-20250514","status":"rate_limited","rate_limited_until":"2026-07-11T12:30:00Z","observed_at":"2026-07-11T12:05:00Z"}
```

**When it fires:**
- On a **status transition** (healthy→rate_limited, rate_limited→healthy, healthy→exhausted, etc.)
- When a cooldown **expires** (rate_limited→healthy transition detected by sweep)
- On initial connection (current state snapshot for all tracked providers)

**How it works:**
1. The engine's `stampRateLimitedUntil` tracks previous status per (provider, model) and emits a bus event `provider.status_changed` on transition via `e.bus.Publish("provider.status_changed", ...)`.
2. The API layer subscribes to `provider.status_changed` on startup and fans out to all connected SSE clients for `GET /api/v1/providers/status/stream`.
3. The HealthManager's existing expiry sweep also publishes `provider.status_changed` when a cooldown expires.
4. The FE opens this stream once at app boot and uses incoming events for optimistic UI updates. On each turn poll, it reconciles against the authoritative `provider_quotas` data.

**Endpoint lifecycle:**
- Long-lived SSE connection (standard `text/event-stream`)
- Client reconnects with `Last-Event-ID` for resumption
- Server sends periodic heartbeats (`: keepalive`) every 30s
- Connection closed on client disconnect (standard SSE behaviour)

**Why this respects the Turn-Based boundary (Turn-Based Post-Then-Poll Architecture, Phase 4):**
- The SSE stream carries **observability events only** — no message content, no turn state
- **Turn polling is still the canonical contract** for all queryable data
- SSE events are optimistic push notifications; the FE reconciles against the authoritative poll data on the next cycle
- The stream is self-contained (one endpoint, one event type), not a general-purpose SSE side-channel

Rationale:

- Turn polling has inherent latency (1s+ adaptive backoff). A provider getting rate-limited mid-stream would take up to 1s+ to appear on the quota chip via polling alone.
- The price of polling at sub-second intervals is wasted bandwidth and server load when nothing changes.
- The SSE side-channel is zero-overhead when idle (no provider transitions = no events) and sub-second when active.
- The existing eventbus (`internal/plugin/eventbus/eventbus.go`) already has `provider.error` and `provider.rate_limited` event types — `provider.status_changed` extends this pattern.
- The Turn-Based boundary is respected because the SSE stream carries only provider health observability, not message or turn state.

## Consequences

**Good:**
- Single `provider_quota` event that answers "what's the availability status?" with no client-side merging (via Part A).
- FE chip updates within milliseconds of a status transition (via Part B).
- No polling overhead for provider status — SSE is push-only, zero traffic when idle.
- The `rate_limited_until` field directly answers the operator's most common question.
- Turn polling, REST API, and SSE all benefit from the same enrichment.
- Backwards compatible: existing consumers ignore the new fields and the new endpoint.

**Bad:**
- New SSE endpoint to maintain, document, and test.
- FE has one additional connection to manage (but it's a long-lived single endpoint, not per-session).
- The status synthesis adds a small amount of engine-side logic.

**Neutral:**
- Existing consumers that ignore the new fields continue to work unchanged.
- The failover system's internal backoff logic is not affected — the snapshot fields are informational projections only.
- Operators with failover disabled always see `healthy` status (or whatever the quota tracker shows), which is correct.
- The SSE endpoint is optional — if the FE never opens it, the system works fine via polling alone.
