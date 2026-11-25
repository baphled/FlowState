---
id: adr-session-run-lifecycle
title: ADR - Session Run Lifecycle
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
  - topic/streaming
  - topic/sessions
  - flowstate/app
  - flowstate/session
  - flowstate/cli
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Session Run Lifecycle
  - Go CLI Session Status Machine
  - Budget-Pause Resume
---

# ADR - Session Run Lifecycle

## Status

**Accepted** — 2026-11-18. Evolved across three merged packets: budget-pause explicit resume (PR #282), config
  composition (PR #292), and crash recovery (PR #296). All lifecycle transitions are implemented with e2e and unit
  specs; the exit-code mapping is contract-tested. The prototype-scale lifecycle narrative lives in [[ADR - Agent Turn
  Lifecycle Architecture]] (proposed); this ADR is its greenfield counterpart with the parts that are actually
  shipped.

## Context

A session in the Go CLI transitions through exactly one status at a time:

| Status | Meaning | Set by |
|---|---|---|
| `active` | Created or resumed, ready for a run | `session new`, `app.ResumePausedSession`, recovery path |
| `paused` | Budget exhausted, awaiting explicit resume | Budget stop during `session run` (exit 5) |
| `cancelled` | a signal, run-route disconnect or shutdown, `session cancel` | Signal handler, run route, `session cancel` |
| `completed` | Turn finished without error | Turn loop completion |
| `failed` | Provider error with terminal=true, or the turn limit reached while still requesting tools (`max_turns`) | Provider stream failure; `ErrMaxTurns` |

A run cancelled mid-turn appends `session.cancelled` with `{"status":"cancelled","reason":R}`, where R says what
happened: `sigint`, `sigterm` or `sighup` for the command-line signal (carried as the root context's cancellation
cause), `client_disconnected` or `shutdown` on `POST /sessions/{id}/run`, and the neutral `cancelled` when the
context carries no named cause. The explicit `session cancel` command and the tool-loop run record no reason.

The command line cancels a run on SIGINT, SIGTERM or SIGHUP, except that SIGHUP is left alone when it was ignored at
start (`nohup`); every one exits 130. SIGQUIT is deliberately not handled: Go's default (dump goroutines, exit 2) is
kept as the debugging behaviour operators expect.

An event append has a definite outcome under cancellation: a context that is already done refuses it before anything
is written, and a write that has started is not interrupted. Otherwise a cancellation arriving during an append could
report failure for a committed event, and the append recording the cancellation would collide with it, leaving the
session active with no final event.

Terminal statuses (`completed`, `cancelled`, `failed`) reject further `session run` and `session cancel` invocations
  with `session_terminal` (exit 6). The valid-status set is validated in `internal/session/storeext.go`; exit codes
  are contract constants in `cmd/flowstate/cli.go`.

### Pre-existing gaps this ADR closes

Before PRs #282 and #296, two lifecycle transitions were unhandled:

1. **Budget-pause → resume.** Budget exhaustion paused the session (exit 5, `session.paused` event), but a rerun
  replayed events into the provider and started a new turn without acknowledging the pause. The `session.resumed`
  event type existed in the store's writer but had no production caller — dead plumbing, exactly like the composition
  hash in [[ADR - Owning-Layer Boundary: CLI Wires Application Only]].
2. **Crash → recovery.** A process that died mid-turn left the session `active` with a dangling `turn.start`. No
  detection or handling existed; the next rerun silently replayed the incomplete events and started a new turn on top.
  See [[ADR - Crash-Recovery Detection and Recovery]] for the detection design.

### The exec loop

PR #260 introduced the provider-driven execution loop (`app.RunSessionExecLoop`) and PR #278 wired it into `cmdRun`.
  The loop drives the provider stream, detects tool calls, executes them through the allowlist-gated tool registry
  (see [[ADR - Tool-Capability Allowlist Gate]] for the prototype-scale statement), and retries the provider with
  results appended until a terminal condition. Termination guards: max turns reached, budget exhausted, provider error
  (terminal, except the one non-terminal `context_overflow` marker that ADR-010 writes before it compacts and retries,
  which leaves the session `active`), context cancelled (SIGINT, SIGTERM or SIGHUP; on the serve run route, a client
  disconnect or shutdown).

The loop was originally designed without resume or crash-recovery awareness: a rerun after pause or crash silently
  skipped the lifecycle event and started a fresh turn. This ADR makes the lifecycle explicit so the loop's entry
  conditions are testable.

## Problem

Without an explicit, event-recorded lifecycle:

1. **Pause is unobservable.** A budget-paused session looks identical to a fresh one; downstream tooling cannot
  distinguish "never ran" from "ran and paused".
2. **Rerun semantics are ambiguous.** Does a rerun continue the same logical turn, start a new one, or replay? Without
  a resume event, the event log cannot answer.
3. **Crashes leave lying logs.** The event log should record the gap between crash and recovery; silently replaying an
  incomplete turn presents an interrupted conversation as intentional.
4. **Exit codes drift.** Without contract-tested mapping, each command handler invents its own success/failure
  signalling.

## Decision

**The session run lifecycle is governed by five ordered phases, each emitting its lifecycle event before any turn work
  begins. Entry conditions are checked in order; exit codes map from turn outcome per the contract.**

### Phase order

```text
loadRunnableSession → reject terminal
                   → detect crash → recover (if crashed)     [ADR-006]
                   → resume pause  (if paused)               [this ADR]
                   → turn loop (single-turn or exec loop)
                   → terminal mapping → exit code
```

### Phase 1 — Load and reject terminal states

`loadRunnableSession` fetches the session row and rejects terminal statuses (`completed`, `cancelled`, `failed`) with
  `session_terminal` (exit 6). This check runs before any other processing, so terminal sessions never emit recovery
  or resume events.

### Phase 2 — Crash-recovery detection and recovery

For active sessions with run-activity evidence, the run checks for a dangling turn. `app.DetectCrashedSession`
  inspects the event log; `app.RecoverCrashedSession` appends `session.interrupted` (actor `engine`, reason
  `process_lost`) atomically. Full design in [[ADR - Crash-Recovery Detection and Recovery]]. Detection is idempotent:
  `session.interrupted` is itself a terminal marker in the dangling-turn scan, so a double call cannot double-append.

### Phase 3 — Budget-pause resume

For paused sessions, `app.ResumePausedSession` appends `session.resumed` (actor `user`, `from_status: paused`)
  atomically via the store's event writer and restores status to `active` in the same transaction. The resumed run
  uses a **fresh budget** derived from the rerun's flags — the pre-pause consumption is recorded in the event log but
  never carried over.

### Phase 4 — Turn execution

The turn loop (single-turn or exec loop with tool surface) runs with the resolved budget. Each turn emits correlated
  events: `user.message`, `provider.request`, content deltas, `tool.call`/`tool.result` (tool-surface runs),
  `turn.end` with terminal status. Event payloads are pinned by the payload SHA-256 recorded in the envelope; replay
  is byte-faithful.

  A provider finish event is never recorded as an `assistant.delta`. When the provider reported a finish reason, the
  completed `turn.end` payload carries it (`{"status":"completed","finish_reason":"length"}`); without one the
  payload stays `{"status":"completed"}`. Neutral values are `stop`, `length` (cut off at the max-token limit) and
  `tool_calls`; others pass through. A `length` turn is still `completed`, but the record shows it was truncated,
  and `session doctor` (and `GET /sessions/{id}/doctor`) surfaces it as `finish_reason` plus `truncated: true`. In
  a tool-surface run the terminal `turn.end` carries the reason of the final provider turn, so a tool-surface run that
  answers on its last allowed turn records that turn's reason on a `completed` turn. A run that is still requesting
  tools after its last allowed turn ends `failed` instead (see Phase 5). A failure to persist
  the provider error or a delta ends the turn with an error and never records `turn.end` or `completed`.

### Phase 5 — Terminal mapping

The turn outcome maps to the exit-code contract:

| Outcome | Exit | Terminal? |
|---|---|---|
| completed | 0 | yes |
| budget stop | 5 | no (paused) |
| provider error | 3 | yes (failed) |
| turn limit reached while still requesting tools (`max_turns`) | 5 | yes (failed) |
| SIGINT, SIGTERM, SIGHUP (SIGHUP unless ignored at start) | 130 | yes (cancelled) |
| unknown tool | 2 | no (pre-flight) |
| config invalid | 4 | no (pre-flight) |
| terminal rejection | 6 | — |
| replay corruption found during a turn (`replay_mismatch`) | 7 | no |
| unknown session | 1 | — |
| internal failure (code `internal`; HTTP 500 on the serve run endpoint) | 1 | no |

Exit 8 (`receipt_invalid`) is not a turn outcome and `session run` cannot return it: it is returned by `swarm run`
when a run is not accepted (a failed gate or a rejected packet) and by `receipt validate`.

An unclassified error (for example a storage failure while recording the turn) is code `internal` and exits 1 — the
same number as an unknown session; the stderr `code` tells them apart. Exit 1 is the CLI contract's number, not a
new one. A `length` finish (answer truncated) does not change the exit: `session run` exits 0 and writes one
warning line to stderr naming `session doctor <id>` and `--max-tokens`; stdout stays the event stream.

### Invariants

1. **Non-paused reruns are byte-identical.** Rerunning a non-paused active session emits no `session.resumed` event.
  Adding recovery did not change the normal rerun path — verified by the existing e2e suite passing unchanged.
2. **Fresh budget on resume.** The resumed run's budget comes from the rerun's `--budget` flag. Verified live: a
  `--budget 20` rerun consumed 17 fresh tokens where a carried-over 28 would have re-paused immediately.
3. **Recovery idempotency.** After crash recovery, the detection predicate sees `session.interrupted` as a terminal
  marker and returns false.
4. **Terminal rejection precedes recovery.** A terminal session never reaches the detection or resume phases.
5. **Event ordering.** Lifecycle events (`session.interrupted`, `session.resumed`) appear at strictly lower seq than
  the next `user.message`.

## Consequences

### Positive

1. **The event log answers lifecycle questions.** "Was this session paused? crashed? resumed?" are queries over
  events, not heuristics over status.
2. **Resume is explicit and auditable.** The `session.resumed` event records actor and prior status; nothing about
  pause/resume is implicit.
3. **Fresh budgets are predictable.** Operators reason about rerun cost without modelling prior consumption.
4. **The exit-code contract is mechanical.** Each outcome has one exit code, contract-tested; command handlers cannot
  drift.
5. **Recovery composes.** The recovery phase is a library call in the run preamble; future phases (checkpoint,
  delegation resume) slot into the same ordered chain.

### Negative

1. **Two event types on the critical path.** `session.resumed` and `session.interrupted` add write amplification to
  reruns of paused/crashed sessions — one event each, negligible at current scale.
2. **The phase chain is order-sensitive.** Reordering recovery and resume would double-emit for a session that is both
  paused and crashed (a state the store cannot currently represent, but the ordering invariant guards anyway).
3. **Fresh-budget semantics surprise some users.** Operators expecting cumulative budget accounting must read the
  contract; the alternative (carry-over) was explicitly rejected.
4. **Status machine is stringly-typed at the store boundary.** Valid statuses are a validated string set, not a Go
  enum, because the store schema predates the lifecycle ADR. A typed migration is possible but not warranted.

### Neutral

1. The five statuses match the prototype's session model; no vocabulary change was needed.
2. The exec loop's termination guards were unchanged; this ADR formalises their ordering, not their behaviour.
3. Exit codes 2/4 were pre-existing (unknown tool, config invalid); the lifecycle mapping only claims 0/3/5/6/130.

## Alternatives considered

### A. Automatic resume without explicit detection

**Rejected.** The run could detect the dangling turn or paused status and silently continue. This avoids the extra
  events but loses the audit trail; the contract's event-ordering requirements exist precisely to make lifecycle
  transitions observable. The explicit events are also what make recovery idempotency checkable.

### B. A new `crashed` session status

**Rejected.** It would make the state machine explicit but requires a new status filter in `session list`, a new exit
  code, schema migration, and status-validation changes. Keeping the session `active` (the truthful state — its work
  is incomplete) with a `session.interrupted` event marking the gap delivers the same audit trail at a fraction of the
  surface change. The same reasoning appears in [[ADR - Crash-Recovery Detection and Recovery]].

### C. Budget carry-over on resume

**Rejected.** The contract explicitly requires a "fresh bounded limit" on resume. Carry-over would mean a tight
  pre-pause budget re-pauses immediately — a resume that cannot make progress is not a resume. Verified live with a
  `--budget 20` rerun: 17 fresh tokens consumed; carry-over of 28 would have re-paused.

### D. Continue-the-same-turn semantics for reruns

**Rejected.** Treating a rerun as continuation of the interrupted turn conflates "new instruction" with "retry after
  interruption". The event log would need turn-scoping to disambiguate, and the provider history would interleave
  unrelated instructions. Starting a fresh turn after a lifecycle event keeps each turn's provenance clean.

### E. Store the lifecycle state outside the event log (status column only)

**Rejected.** The status column is the projection; the events are the truth. A crash between a status write and its
  event would leave the two inconsistent. The atomic append (event + status in one store transaction) closes that
  window; a status-only design reopens it.

## Cross-links

### Sibling ADRs (Go CLI group)

- [[ADR - Crash-Recovery Detection and Recovery]] — Phase 2's design; detection predicate, idempotency proof,
  run-activity gate.
- [[ADR - Owning-Layer Boundary: CLI Wires Application Only]] — why the lifecycle entry points live in app wrappers,
  not cmd.
- [[ADR - Config-Driven Session Composition]] — budget defaults feeding Phase 3's fresh-budget rule.
- [[ADR - Schema Validation of JSON Output]] — the event envelope's seven pinned fields; lifecycle events validate
  like any other envelope.

### Prototype ADRs

- [[ADR - Agent Turn Lifecycle Architecture]] — the prototype-scale turn lifecycle (hooks, plugins, compression); its
  event taxonomy survives in the Go CLI's event types.
- [[ADR - Streaming Architecture]] — SSE provider streaming feeding the turn loop.
- [[ADR - Hierarchical Session Navigation]] — M2 child sessions will inherit this lifecycle per session node.
- [[ADR - Tool-Capability Allowlist Gate]] — the allowlist gate guarding tool execution inside Phase 4.

## Sources

- `internal/session/storeext.go:32-39` — terminal set and valid-status validation.
- `cmd/flowstate/cli.go:18-21` — exit-code constants (4, 5, 130, 6).
- `cmd/flowstate/sessionrun.go:22` — provider-error exit 3; `:161-180` — the phase chain.
- `internal/app/session_resume.go:26` — `ResumePausedSession`; `internal/store/writer.go:236-255` — atomic resume
  append.
- `internal/app/session_recovery.go` — detection and recovery (Phase 2; see ADR-006 sources).
- PR #282 (resume), PR #292 (composition), PR #296 (recovery).
- Dropbox `CLI-AND-API-CONTRACT.md` sections 1–3 — status machine and exit-code contract.

## Related Plans

- *Completed:* budget-pause resume (PR #282), crash recovery (PR #296).
- *M2:* child sessions ([[ADR - Hierarchical Session Navigation]]) compose this lifecycle per node; shared budgets may
  amend the fresh-budget rule for delegated runs.
