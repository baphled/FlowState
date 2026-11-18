# ADR-006: Crash-Recovery Detection and Recovery

## Status

Accepted (18 November 2026). Landed as PR #296. Completes the POST-M1-BACKLOG M2 scope item "cancellation, checkpoint and recovery" alongside SIGINT cancellation (exit 130) and budget-pause explicit resume (#282).

## Context

### The crash scenario

When a `session run` process terminates without emitting a terminal event (killed, power loss, OOM, dropped SSH session), the session stays `active` with a dangling `turn.start` and no matching terminal event. Without recovery, the next run replays the incomplete turn into the provider history, starts a new turn on top, and leaves two `turn.start` events with no intervening terminal marker — semantically wrong: the event log should record the gap, not silently replay the incomplete turn as if it were intentional.

### The detection challenge

Distinguishing a crashed session from a fresh session is non-trivial because both have `turn.start` as their only turn event:
- A fresh session (created via `session new`, never run) has `turn.start` with payload `{task: ...}` and no other events
- A crashed session has `turn.start` plus additional events (provider.request, tool.call, content deltas) proving the run was in progress

The naive detection (turn.start without turn.end = crashed) false-positives on fresh sessions, breaking the existing test suite.

## Decision

Crash recovery uses a two-part predicate:

### Run-activity evidence gate

`DetectCrashedSession` requires at least one `provider.request` or `tool.call` event in the session's event log. A session with only the initial `turn.start` (from `session.Create`) has no run activity and is not detected as crashed. This gate prevents the predicate from firing on freshly created sessions.

### Dangling-turn check

With run activity confirmed, the detection scans backward through the event log for the last turn-lifecycle event (`turn.start`, `turn.end`, `session.cancelled`, `session.paused`, `session.interrupted`). If the last such event is `turn.start`, the turn is dangling and the session is crashed.

### Recovery action

`RecoverCrashedSession` appends `session.interrupted` (actor `engine`, reason `process_lost`) via the store's event writer, using the same atomic append pattern as `ResumePausedSession`. The recovery is called only when `DetectCrashedSession` returns true, making it naturally idempotent: after the first recovery, `session.interrupted` is the last turn-lifecycle event, and the detection predicate returns false.

### Run-activity refinement

The initial implementation detected ALL active sessions with `turn.start` as crashed, including freshly created sessions that had never been run. This caused the existing session-run tests to fail (the crash-recovery check fired on sessions that were actively running with the fake provider). The fix: require run-activity evidence (at least one `provider.request` or `tool.call` event) before considering the session crashed. A session with only the initial `turn.start` from `session.Create` is not crashed.

## Integration with the session run lifecycle

The crash-recovery check runs in `cmdRun` after `loadRunnableSession` and before `runTurnOrLoop`:

```
loadRunnableSession → reject terminal → detect crash → recover → turn loop
```

The recovery appends `session.interrupted` before the turn loop begins, so the event ordering proves recovery happened before turn work. The recovered session then enters the normal turn execution with a fresh budget from the rerun's flags.

## Alternatives considered

**Heartbeat-based detection** was rejected as adding complexity: the heartbeat interval would need tuning, and the detection still requires the same dangling-turn check.

**Lease-based ownership** (the admission runner's model for PR-level ownership) was rejected for session-level detection: the lease would need SQLite storage with an expiry timestamp, adding complexity without clear benefit for the single-process MVP.

**A new "crashed" session status** would make the state machine explicit but changes the session schema and adds a status filter to `session list`. The `session.interrupted` event provides the same audit trail without a new status.

**Optimistic recovery** (append `session.interrupted` unconditionally) was rejected because it would fire on non-crashed sessions (e.g., a session being run for the first time).

## Consequences

### Positive

- A crashed session is detected and recovered on the next run, with the event log recording the interruption
- The recovery is idempotent: after recovery, the detection predicate returns false
- Fresh sessions are correctly excluded by the run-activity gate
- The event ordering proves recovery happened before turn work: `session.interrupted` at seq N, then `user.message` at seq N+1
- The recovery uses the same event writer and atomic append as the resume path, maintaining consistency

### Negative

- The run-activity gate (provider.request or tool.call) requires the fake provider in test fixtures to emit those events for crash-recovery tests
- The detection scans the full event log (O(n) where n is the event count) — acceptable for the MVP session sizes but worth monitoring for long-running sessions
- The `session.interrupted` event type is not in the store's event-type validation — it passes through as a pass-through event type, relying on the event envelope schema for structure validation

### Risks

- If the detection predicate is too permissive (e.g., a session created but not yet run has a provider.request from the config composition's default tool allowlist probe), fresh sessions could be false-positived — mitigated by requiring provider.request specifically (not just any event)
- If the recovery append fails, the session stays active with the dangling turn and the error surfaces — this is correct behaviour (the error blocks turn execution)
- Multi-process concurrent runs of the same session could interleave events — mitigated by the store's single-writer event model

## References

CLI-AND-API-CONTRACT.md section 3; PR #296 (crash recovery); PR #282 (paused-session resume); POST-M1-BACKLOG.md M2 scope; ADR-002 (session run lifecycle — crash recovery is phase 2).
