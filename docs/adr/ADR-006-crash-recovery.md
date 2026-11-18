# ADR-006: Crash-Recovery Detection and Recovery

## Status

Accepted (18 November 2026). Landed as PR #296. Completes the POST-M1-BACKLOG M2 scope item "cancellation, checkpoint and recovery".

## Context

A session run process can die without emitting a terminal event, leaving the session active with a dangling turn.start. Without crash recovery, the next session run silently replays the incomplete events. The detection challenge is distinguishing a crashed session from a fresh one: both have turn.start as their only turn event initially. The fix: require run-activity evidence (provider.request or tool.call events) before considering the session crashed.

## Decision

DetectCrashedSession requires at least one provider.request or tool.call event (run-activity gate) and scans backward for a dangling turn.start. RecoverCrashedSession checks the detection predicate first and appends session.interrupted (actor engine, reason process_lost) idempotently via the event writer. After recovery, session.interrupted is a terminal marker that suppresses re-detection.

## Consequences

Positive: budget-paused sessions survive restarts. The event log records the full lifecycle. Detection is idempotent. Fresh sessions correctly excluded.

Negative: one event-log replay per active-session run. The run-activity gate requires fake provider fixtures to emit provider.request/tool.call events. session.interrupted is a pass-through event type.

Risks: overly permissive detection would fire on non-crashed sessions. Recovery append failure leaves the session active with the dangling turn. Concurrent runs could interleave events.

## References

CLI-AND-API-CONTRACT section 3, PR #296, PR #282 (paused-session resume).
