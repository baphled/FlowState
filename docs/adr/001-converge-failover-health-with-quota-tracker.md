# ADR 001: Converge Failover Health State with Quota Tracker

**Title**: Converge Failover Health State with Quota Tracker

## Context

FlowState has two independent systems that track provider availability:

1. **Failover HealthManager** (`internal/plugin/failover/healthmanager.go`) — Tracks per-(provider, model) rate-limit cooldowns as wall-clock expiry times. Data is persisted to `~/.cache/flowstate/provider-health.json`. This system is used by the failover stream hook to decide whether to skip a rate-limited provider when attempting a fallback.

2. **Provider Quota Tracker** (`internal/provider/quota/`) — Tracks per-(provider, account_hash, model) quota snapshots containing rate-limit windows (`RateLimitVariant`), cumulative token spend (`TokenSpendVariant`), or a not-configured reason (`NotConfiguredVariant`). Data is emitted as `provider_quota` SSE chunks, served via `GET /api/v1/providers/quota`, and surfaced in turn-polling responses.

These two systems operate in isolation today. The failover cooldown expiry is not visible through any application-level interface — an operator who wants to know "when will provider X be available again?" must either:

- Read `~/.cache/flowstate/provider-health.json` directly, or
- Use the `provider-health` agent tool (which is only available inside an agent session)

Neither the SSE `provider_quota` stream, the `GET /api/v1/providers/quota` REST endpoint, nor the turn-polling `provider_quotas` field surface the failover cooldown. This forces consumers (the FE chip, CLI operators) to consult multiple data sources to get the full availability picture.

Additionally, when a provider IS rate-limited, the quota tracker's `RateLimitVariant` may show `remaining = 0` on one or more windows, but the tracker does not know the carrier-issued `retry-after` duration that the failover system already parsed and stored. The two datasets are complementary: the quota tracker knows *what* the limits are; the failover system knows *when* the cooldown expires.

## Options

### Option A: Stamp `RateLimitedUntil` onto `quota.Snapshot` via the Engine

Add a `RateLimitedUntil time.Time` field to `quota.Snapshot`. In the engine's `buildProviderQuotaChunk` and `QuotaSnapshots()` methods, query the FailoverManager's HealthManager and stamp the cooldown expiry onto each snapshot before serialisation.

- **Pros**: Single field addition; all downstream consumers (SSE, REST API, turn polling) pick it up automatically because they all convert from `quota.Snapshot`.
- **Pros**: Zero new interfaces; the engine already holds both the `quotaTracker` and the `failoverManager`.
- **Pros**: The zero value of `time.Time` naturally means "not rate-limited", so existing code with no failover wiring continues to work unchanged.
- **Cons**: Adds a cross-package concern to `quota.Snapshot` (the HealthManager is a failover concept). Mitigated by the engine being the stamping site, not the quota package itself.
- **Cons**: Requires changes to the wire payload structs, the REST API dashboard entry, and the turn snapshot type.

### Option B: Separate Cooldown Endpoint

Create a new REST endpoint `GET /api/v1/providers/cooldowns` that surfaces the failover health state directly from the HealthManager.

- **Pros**: Clean separation of concerns — no cross-package contamination.
- **Cons**: Forces FE to make two API calls and merge data client-side.
- **Cons**: SSE `provider_quota` stream would still lack cooldown info.
- **Cons**: More surface area to document, test, and maintain.

### Option C: Extend the Aggregator Interface

Add a `Cooldowns()` method to the `api.QuotaAggregator` interface that returns cooldown info alongside quota snapshots.

- **Pros**: Keeps the failover concern inside the API boundary.
- **Cons**: Still requires FE to merge two data structures.
- **Cons**: Adds complexity to the aggregator bridge.

## Decision

**Adopt Option A: Stamp `RateLimitedUntil` onto `quota.Snapshot` via the engine.**

Rationale:

- The engine is the natural integration point — it already holds both the quota tracker and the failover manager.
- A single `time.Time` field on `Snapshot` with zero-is-not-rate-limited semantics is the least invasive change.
- All four output channels (SSE, REST API, turn polling, and any future channel) automatically surface the cooldown without per-channel wiring.
- The `provider_quota` SSE chunk and `GET /api/v1/providers/quota` response are the canonical "provider availability" data sources; adding cooldown expiry there completes the picture.
- FE consumers can display a single "rate-limited until X" indicator without merging streams.

## Consequences

**Good:**
- Single source of truth for "when will this provider/model be available again?"
- All consumers benefit from one change.
- No new endpoints, no new interfaces, no new request/response types.
- Backwards compatible: existing consumers ignore the new field; zero value = not rate-limited.

**Bad:**
- The quota package's `Snapshot` struct gains a field that the failover system populates — a mild concern leak. Mitigated by the field name (`RateLimitedUntil`) being generic enough to describe any rate-limit cooldown, and by the stamping happening in engine code rather than the quota package itself.
- Operators who have disabled the failover system (no `FailoverManager` wired) will always see the zero value — correct behaviour, but they lose the cooldown signal.

**Neutral:**
- The `TimeLimitedUntil` field is informational only. The failover system's backoff logic is not changed — it continues to read from its own in-memory map. The snapshot field is a read-only projection for consumers.
