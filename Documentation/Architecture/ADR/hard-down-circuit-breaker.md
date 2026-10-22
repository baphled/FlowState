# ADR: Hard-Down Circuit Breaker for Permanently-Failed Providers

Date: 2026-09-19
Status: Accepted

## Context

The HealthManager only modelled cooldowns: every failure class mapped to
an expiry (billing/auth 24h, capped escalation), and expiry always
re-opened the pair. A provider whose credential cannot recover on its
own — billing exhausted, account deactivated, a revoked key — was
therefore retried into the same wall on every turn once each cooldown
lapsed, and strict-pinned agents died outright with "all providers
failed" because their single-element chain had no healthy successor.
Owner mandate: FlowState should stop hammering providers that are
unavailable, and users should not have to edit manifests because they
stopped paying one provider.

## Decision

The health entry gains a terminal **hard-down** state with a
class-aware trip seam (`MarkPermanentFailure`), mirroring
`delegation.CircuitBreaker.RecordTypedFailure`:

1. **Trip thresholds.** `ErrorTypeBilling` and auth failures carrying
   billing-class account codes (`account_deactivated`,
   `billing_not_active`) trip after **one** failure: no refresh path can
   recover an unpaid account. Other `ErrorTypeAuthFailure` occurrences
   trip on the **third** consecutive failure, leaving two windows for
   the reactive OAuth refresh to recover an expiring token. Transient
   classes (rate limit, overload, network, server) are never routed to
   the seam and keep today's cooldown semantics.
2. **No auto re-probe.** Hard-down is permanent until
   `flowstate health reset` (the existing `ResetProviderHealth` path)
   or a configuration change. There is no half-open timeout and no
   expiry: probing a dead credential again would burn a request to
   rediscover a known answer.
3. **Consultation parity.** Every health consultation site
   (`rankCandidatesWithTiers`, `selectAttemptCandidate`,
   `prependAgentChain`, the detector `Hook.Apply`) skips hard-down
   pairs exactly like rate-limited ones — `IsRateLimited` reports
   hard-down pairs as unavailable, while `RateLimitedUntil` reports no
   recoverable expiry so the recovery-wait path never sleeps on a dead
   pair.
4. **Persistence.** The terminal state round-trips through
   provider-health.json (`hard_down`, `permanent_fails`,
   `hard_down_reason`), `LoadState` keeps hard-down entries past any
   cooldown expiry, and a trip writes through the persistence debounce
   so a crash cannot lose it. The `flowstate health status` CLI renders
   a HARD-DOWN marker.
5. **Strict fallback.** When a strict manifest head (or a pinned pair)
   is hard-down, the healthy global chain is appended as a fallback tail
   at chain-construction time — via one shared helper — so a
   strict-pinned agent completes its turn on a healthy provider instead
   of failing the session. The dead pin is never re-inserted afterwards
   (the existing prependAgentChain/promotePinned loop-guard holds).

The breaker is deliberately zero-config: the trip thresholds are
compiled-in policy, not user-tunable surface.

## Consequences

- An unpaid provider stops receiving traffic after a single billing
  rejection; paying again plus `flowstate health reset` (or restarting
  with cleared cache) re-opens it.
- Three consecutive genuine 401s without a successful refresh break a
  provider until reset — accepted over the alternative of infinite
  retry; the count only accrues across separate failures, and a healthy
  turn never increments it.
- Plain-text (untyped) provider errors keep pure cooldown semantics;
  the breaker decides on typed classifications only, so text-heuristic
  classification cannot permanently break a provider on a fuzzy match.
