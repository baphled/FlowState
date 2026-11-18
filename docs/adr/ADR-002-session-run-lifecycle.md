# ADR-002: Session Run Lifecycle

## Status

Accepted (18 November 2026). Evolved across PRs #282 (paused-session resume), #292 (config composition), and #296 (crash recovery). All lifecycle transitions implemented with e2e and unit specs.

## Context

A session transitions through exactly one status at a time:

| Status | Meaning | Set by |
|---|---|---|
| `active` | Created or resumed, ready for a run | `session new`, `app.ResumePausedSession` |
| `paused` | Budget exhausted, awaiting explicit resume | Budget stop during `session run` (exit 5) |
| `cancelled` | SIGINT or explicit `session cancel` | `session cancel` command or SIGINT handler |
| `completed` | Turn finished without error | Turn loop completion |
| `failed` | Provider error with terminal=true | Provider stream failure |

Terminal statuses (`completed`, `cancelled`, `failed`) reject further `session run` and `session cancel` invocations with `session_terminal` (exit 6).

### Pre-existing gaps

Before PRs #282 and #296, two lifecycle transitions were unhandled. Budget-pause → resume: budget exhaustion paused the session (exit 5, `session.paused`), but a rerun replayed events into the provider and started a new turn without acknowledging the pause. The `session.resumed` event existed in the store's writer but had no production caller — the composition hash was always passed as `""`. Crash → recovery: a process that died mid-turn left the session active with a dangling `turn.start`. No detection or handling existed — the next rerun silently replayed the incomplete events and started a new turn on top.

### The exec loop

PR #260 introduced the provider-driven execution loop (`app.RunSessionExecLoop`), wired into `cmdRun` by PR #278. The loop drives the provider stream, detects tool calls, executes them through the allowlist-gated registry, and retries the provider with results appended until a terminal condition (max turns, budget exhausted, provider error, or context cancelled). The loop was originally designed without resume or crash-recovery awareness: a rerun after pause or crash would silently skip the lifecycle event and start a fresh turn.

## Decision

The session run lifecycle is governed by six ordered phases:

### Phase 1: Load and reject terminal states

`loadRunnableSession` fetches the session row and rejects terminal statuses (`completed`, `cancelled`, `failed`) with `session_terminal` (exit 6). This check runs before any other processing.

### Phase 2: Crash-recovery detection and recovery

For active sessions with run-activity evidence (at least one `provider.request` or `tool.call` event), the run checks for a dangling `turn.start` (a turn that was started but never completed with a terminal event). If detected, `app.DetectCrashedSession` inspects the event log for run-activity evidence and a dangling turn, and `app.RecoverCrashedSession` appends `session.interrupted` (actor `engine`, reason `process_lost`) via the store's event writer. Detection is idempotent: after recovery, `session.interrupted` is a terminal marker that suppresses re-detection. Fresh sessions (no run-activity evidence) are not detected as crashed.

### Phase 3: Budget-pause resume

For paused sessions, `app.ResumePausedSession` appends `session.resumed` (actor `user`, `from_status` paused) atomically via the store's event writer and restores status to active. The resumed run uses a fresh budget derived from the rerun's flags — the pre-pause budget is never carried over.

### Phase 4: Turn execution

The turn loop (single-turn or exec loop with tool surface) runs with the resolved budget. Each turn emits correlated events (`user.message`, `provider.request`, content deltas, `tool.call`/`tool.result` for tool-surface runs, `turn.end` with terminal status).

### Phase 5: Terminal mapping

The turn outcome maps to the exit code contract: completed (0), budget stop (5, not terminal), provider error (3, terminal), SIGINT (130, terminal).

### Invariants

1. Non-paused reruns are byte-identical: rerunning a non-paused active session emits no `session.resumed` event. This ensures that adding crash recovery did not change the normal rerun path.
2. Fresh budget on resume: the resumed run's budget comes from the rerun's `--budget` flag. The pre-pause consumption is recorded in the event log but does not suppress or double-charge the resumed run.
3. Recovery idempotency: after crash recovery, the detection predicate sees `session.interrupted` as a terminal marker and returns false. A double call to `RecoverCrashedSession` does not double-append.
4. Terminal rejection: `loadRunnableSession` rejects terminal sessions before the crash-recovery check runs.
5. Event ordering: `session.interrupted` (seq N) appears before the next `user.message` (seq N+1) in the event log.

## Alternatives considered

**Automatic resume without explicit detection** would avoid the extra event but lose the audit trail proving a crash occurred; the explicit event is necessary for the contract's event-ordering requirements.

**A new "crashed" terminal status** would make the state machine explicit but requires a new status filter, exit code, and schema change; keeping `active` (the truthful state) with a `session.interrupted` event is simpler.

**Budget carry-over on resume** is explicitly excluded by the contract ("fresh bounded limit"). The fresh-budget decision was verified live: a `--budget 20` rerun consumed 17 fresh tokens where carried-over 28 would have re-paused.

## Consequences

Positive: a budget-paused session survives process restarts and continues cleanly. The event log records the full lifecycle. Detection is idempotent and safe to call on any active session. Fresh sessions are not false-positived as crashed. Non-paused reruns remain byte-identical to the pre-crash-recovery behaviour.

Negative: the crash-recovery check adds one event-log replay per `session run` on an active session. The run-activity gate (provider.request or tool.call) requires the fake provider in test fixtures to emit those events for crash-recovery tests. The `session.interrupted` event type is not in the store's event-type validation — it passes through as a pass-through event type.

Risks: if the detection predicate is too permissive (e.g., counts any active session as crashed), ordinary reruns would emit spurious `session.interrupted` events. If the recovery append fails, the session stays active with the dangling turn. Multi-process concurrent runs of the same session could double-append `session.interrupted` — mitigated by the store's single-writer event model.

## References

CLI-AND-API-CONTRACT.md sections 1-3; PR #282 (paused-session resume); PR #296 (crash recovery); POST-M1-BACKLOG.md M2 scope ("cancellation, checkpoint and recovery").
