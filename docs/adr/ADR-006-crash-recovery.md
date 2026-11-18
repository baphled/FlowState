---
id: adr-crash-recovery-detection-and-recovery
title: ADR - Crash-Recovery Detection and Recovery
type: adr
status: accepted
adr_group: Go CLI
created: 2026-11-18
modified: 2026-11-18T21:40
deciders:
  - Engineering Team
consulted:
  - Architecture Review Board
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/lifecycle
  - topic/recovery
  - topic/sessions
  - flowstate/app
  - flowstate/session
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Crash-Recovery Detection
  - session.interrupted
  - Dangling-Turn Detection
---

# ADR - Crash-Recovery Detection and Recovery

## Status

**Accepted** — 2026-11-18. Landed as PR #296. Completes the Dropbox `POST-M1-BACKLOG.md` M2 scope item "cancellation,
  checkpoint and recovery" alongside SIGINT cancellation (exit 130) and budget-pause explicit resume (PR #282). Sits
  at Phase 2 of the lifecycle chain defined in [[ADR - Session Run Lifecycle]].

## Context

### The crash scenario

When a `session run` process terminates without emitting a terminal event — killed, power loss, OOM, dropped SSH
  session — the session remains `active` with a dangling `turn.start`: a turn begun but never closed by `turn.end`,
  `session.cancelled`, or `session.paused`. The SQLite store preserves every persisted event; nothing is lost. What is
  missing is *recognition*: before PR #296, no mechanism detected the interrupted state, so the next run replayed the
  incomplete turn into the provider history and started a fresh turn on top.

### Why silent replay is wrong

The event log is the audit spine (see [[ADR - Session Run Lifecycle]]). Silently replaying an incomplete turn presents
  an interrupted conversation as intentional: two `turn.start` events with no intervening terminal marker, a provider
  history containing a turn whose question was never fully answered. Downstream reasoning over the log cannot
  distinguish "the model abandoned the turn" from "the process died". The log must record the gap.

### The detection challenge

Distinguishing a crashed session from a fresh one is non-trivial because both contain `turn.start` as their only
  turn-lifecycle event:

- A **fresh** session (`session new`, never run) has the initial `turn.start` (payload `{task: ...}`) and no other
  events.
- A **crashed** session has `turn.start` plus run-activity events — `provider.request`, `tool.call`, content deltas —
  proving a run was in progress.

The naive predicate (`turn.start` without `turn.end` ⇒ crashed) false-positives on every fresh session. The first
  implementation of PR #296 did exactly this and broke the existing session-run e2e suite: ordinary first runs were
  detected as crashes and emitted spurious `session.interrupted` events.

## Problem

1. **Interrupted sessions lie by omission.** The gap between crash and recovery is unrecorded.
2. **Reruns compound the lie.** Silent replay stacks a fresh turn on the dangling one; the log grows a prefix that no
  terminal event ever closes.
3. **Recovery must be idempotent.** A detection predicate that re-fires after recovery would double-append
  `session.interrupted` on every subsequent run.
4. **Fresh sessions must not false-positive.** The most common session state (created, about to run) is superficially
  identical to the crashed state at the status-column level.

## Decision

**Crash recovery uses a two-part predicate — run-activity evidence plus dangling-turn scan — and recovers by
  atomically appending `session.interrupted` before the turn loop begins.**

### Part 1 — Run-activity evidence gate

`DetectCrashedSession` requires at least one `provider.request` or `tool.call` event in the session's log. A session
  with only the initial `turn.start` from `session.Create` has no run activity and is never detected as crashed. This
  gate is what separates "created but not yet run" from "run interrupted mid-flight".

### Part 2 — Dangling-turn scan

With run activity confirmed, the detection scans backward through the event log for the last turn-lifecycle event
  (`turn.start`, `turn.end`, `session.cancelled`, `session.paused`, `session.interrupted`). If that event is
  `turn.start`, the turn is dangling and the session is crashed. If it is any terminal marker — including
  `session.interrupted` itself — the session is not crashed.

### Recovery action

`RecoverCrashedSession` appends `session.interrupted` (actor `engine`, reason `process_lost`) via the store's event
  writer, using the same atomic append pattern as `ResumePausedSession`: event and status projection in one store
  transaction. Recovery runs only when detection returns true, and because `session.interrupted` is itself a terminal
  marker in the Part-2 scan, the predicate returns false after the first recovery — idempotency by construction, not
  by bookkeeping.

### Placement in the run preamble

The check runs in `cmdRun` after `loadRunnableSession` and before `runTurnOrLoop`:

```text
loadRunnableSession → reject terminal → detect crash → recover → resume pause → turn loop
```

Recovery precedes pause-resume: a session cannot be both paused and crashed (pause writes a terminal-for-scan marker),
  and the ordering makes the event log read cause-then-response. The recovered run then enters normal turn execution
  with a fresh budget from the rerun's flags (per the lifecycle ADR's fresh-budget rule).

### The false-positive fix

The run-activity gate was added *after* the first implementation broke the suite. The lesson is recorded because it
  generalises: a detection predicate must distinguish the state it targets from the most common adjacent state, and
  the cheapest discriminator (presence of provider/tool activity) was available in the log all along. The fake
  provider in test fixtures was also updated to emit `provider.request` events so crash-recovery specs exercise the
  real predicate.

## Consequences

### Positive

1. **The log tells the truth.** `session.interrupted` (actor `engine`, reason `process_lost`) marks the gap; the
  following `user.message` starts a clean turn.
2. **Idempotency is structural.** No recovery-state flag exists; the terminal-marker property of `session.interrupted`
  in the scan makes double-recovery impossible.
3. **Fresh sessions are safe.** The run-activity gate excludes them; the pre-#296 e2e suite passes unchanged.
4. **Recovery composes with resume.** Same atomic append pattern, same writer, same preamble position — one pattern to
  audit.
5. **Crash detection needs no extra infrastructure.** No heartbeats, no leases, no timestamps beyond the events
  already written.

### Negative

1. **Detection is O(n) over the event log.** Each run on an active session scans for run activity and the last
  lifecycle marker. Acceptable at current log sizes; a per-session lifecycle-index would be the first optimisation if
  long sessions slow reruns.
2. **Test fixtures must emit run activity.** The fake provider now emits `provider.request`; any new fixture that
  skips it silently disables the crash path in its specs.
3. **`session.interrupted` is a pass-through event type.** The store's type validation does not enumerate it;
  structural integrity rests on the envelope schema (see [[ADR - Schema Validation of JSON Output]]) rather than a
  type whitelist.
4. **The gate discriminates on event type, not process evidence.** A run that crashed before its first
  `provider.request` (e.g., during prompt assembly) is indistinguishable from a fresh session. The window is small and
  the consequence benign (no gap event), but it exists.

### Neutral

1. The reason code `process_lost` is free text in the payload; no enum constrains it. If recovery later gains distinct
  paths (SIGKILL vs OOM vs host loss), the payload grows a structured cause.
2. Recovery never mutates prior events; it only appends. Replay stays append-only and byte-faithful.
3. Multi-process concurrent runs of one session remain unsupported overall (single-writer store model); recovery
  neither helps nor harms that boundary.

## Alternatives considered

### A. Heartbeat-based detection

**Rejected.** The run would emit periodic heartbeats and a stale heartbeat would signal crash. This adds a write path
  and an interval-tuning problem, and detection *still* needs the dangling-turn scan to know what was interrupted.
  Heartbeats buy wall-clock recency the event log does not need — the gap's duration is recoverable from event
  timestamps after the fact.

### B. Lease-based ownership (the admission runner's model)

**Rejected for sessions.** The admission runner uses leases for PR-level ownership because PRs are long-lived and
  contention is real. Session runs are single-process by design; a lease would add an expiry timestamp and a takeover
  protocol to solve a contention problem the store's single-writer model already excludes. See [[ADR - FlowState-Owned
  GitHub OAuth App]] for where leases *are* warranted.

### C. A new `crashed` session status

**Rejected.** Same reasoning as the lifecycle ADR's Alternative B: a new status means schema migration, a new list
  filter, a new exit code, and status-validation changes — for information the event already carries. Keeping the
  session `active` (truthfully: work is incomplete) with `session.interrupted` marking the gap delivers the audit
  trail at a fraction of the surface change.

### D. Optimistic recovery (append `session.interrupted` on every active-session rerun)

**Rejected.** It would fire on ordinary first runs — exactly the false-positive the gate exists to prevent. Optimism
  in recovery direction is backwards: the cost of a spurious gap event (lying log) exceeds the cost of a missed one
  (next run detects it).

### E. Detect at session-open time (in `InspectSession`/`ListSessions`) rather than run time

**Rejected for now.** Surfacing crash state to read commands would let tooling flag interrupted sessions without
  running them — genuinely useful for a future `session doctor` surface. But it widens the detection's blast radius to
  every read path and invites UI churn before the run-path behaviour is proven. The run-time placement is the minimal
  correct slice; read-path surfacing is a noted follow-up.

## Cross-links

### Sibling ADRs (Go CLI group)

- [[ADR - Session Run Lifecycle]] — the phase chain this detection occupies (Phase 2); terminal-status rejection,
  fresh-budget resume, exit-code mapping.
- [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] — detection and recovery are app-layer functions called
  from the run preamble via the owning wrapper.
- [[ADR - Schema Validation of JSON Output]] — the `session.interrupted` envelope validates like every event;
  structure-pinning is the pass-through type's safety net.
- [[ADR - Config-Driven Session Composition]] — the recovered run's fresh budget comes from config/flag resolution.

### Prototype ADRs

- [[ADR - Agent Turn Lifecycle Architecture]] — the prototype's turn lifecycle, including interruption hooks; the Go
  CLI's recovery is its event-log-native counterpart.
- [[ADR - Streaming Architecture]] — the provider stream whose premature termination produces the dangling turn.
- [[ADR - FlowState-Owned GitHub OAuth App]] — where lease-based ownership *is* used (PR admission), and why sessions
  differ.

## Sources

- `internal/app/session_recovery.go:30-37` — run-activity gate (`provider.request` or `tool.call`).
- `internal/app/session_recovery.go:38-46` — backward dangling-turn scan; terminal marker set includes
  `session.interrupted`.
- `internal/app/session_recovery.go:52-72` — `RecoverCrashedSession` atomic append; actor `engine`, reason
  `process_lost`.
- `cmd/flowstate/sessionrun.go:161-180` — preamble ordering: load → terminal reject → detect/recover → resume → turn
  loop.
- `internal/store/writer.go` — atomic event+status append shared with the resume path.
- PR #296 (landing); PR #282 (resume; the pattern recovery reuses).
- Dropbox `POST-M1-BACKLOG.md` M2 scope — "cancellation, checkpoint and recovery".

## Related Plans

- *Completed:* PR #296; M2 recovery item closed.
- *Noted follow-up:* read-path surfacing of interrupted state (`session doctor`-style), deliberately deferred.
