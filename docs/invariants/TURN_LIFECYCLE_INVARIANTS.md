# Turn Lifecycle Invariants — Tests-First Ledger

**Status: awaiting owner review. No production code has been changed.**

## Methodology

This ledger records an invariant BDD suite written **tests-first** against
the current engine. The three feature files below pin the invariants the
owner mandated; every scenario that documents a violation is **expected to
FAIL and is committed red on purpose**. The failures are the deliverable:
they give the complete picture of broken invariants *before* any fix is
written, so fixes can be reviewed against a frozen, executable
specification.

- `features/engine/turn_completes_completely.feature` — invariant 1: *a
  request finishes completely*.
- `features/engine/session_bounded_lifecycle.feature` — invariant 2: *a
  session cannot go on forever*.
- `features/engine/skills_guarantee.feature` — always-active skills
  guarantee.

Run a suite with:

```bash
GODOG_PATHS=features/engine/turn_completes_completely.feature \
  GODOG_FORMAT=pretty go test -tags e2e ./features/... -run TestFeatures
```

Fixture knobs used (all pre-existing, none added): `MaxToolLoopIterations`,
`MaxToolLoopDuration`, `TodoStore`, `ToolRegistry.SetPermission`,
`Store` (temp-dir `FileContextStore`), `TokenCounter`, `AutoCompactor`,
`CompressionConfig.AutoCompaction`. No scenario was blocked on a missing
engine knob; there is no "untestable without knob X" entry.

**Full-suite impact (measured):** the e2e suite at the parent commit
(`e8e5ff6a`) fails 99 scenarios; with this suite it fails 108. The delta
is exactly the 9 intended red scenarios below — no other scenario
changed outcome in either direction.

## Root-cause register (from the three-lane code investigation)

| Ref | Finding | Site |
|-----|---------|------|
| RC1 | 12+ terminal paths in the tool-loop cap cascade (`doneAfterTodoCheck` family) emit only a `tool_loop_exceeded` sentinel Done with no final assistant message; transcript ends on `tool_result` | `internal/engine/toolloop.go:883-895`, cascade `1583-1870` |
| RC2 | "consecutive same-tool-pattern caps, stopping" does not stop — `doneAfterTodoCheck` → `checkIncompleteTodosBeforeComplete` injects another continuation while todo budgets remain (`maxTodoContinuations=20`) | `internal/engine/toolloop.go:1707-1718` → `595-657` |
| RC3 | no-progress guard neutralised — `workedSinceContinuation=true` on ANY tool-call batch, so re-reading the same file counts as progress and `maxNoProgressContinuations` (3) never trips | `internal/engine/toolloop.go:1349-1351`, `586-594` |
| RC4a | delivery-retry stop path returns without `completeResponse`; final assistant message never stored | `internal/engine/toolloop.go:1211-1224` |
| RC4b | `tool_loop_exceeded` sentinel never stamped on a persisted assistant message → session never flips to `failed` | sentinel emission `internal/engine/toolloop.go:888-893` |
| RC4c | permission-denied leaves a dangling tool_call: assistant tool_use is persisted, then the loop returns with no tool_result | persist `toolloop.go:1352`, deny-return `1356-1359`, deny chunk `engine.go:3249-3264` |
| RC5 | every continuation injection resets `iterations=0`, `loopStart=now()`, `toolExecDuration=0` — budgets are per-continuation-window; worst case ~43 windows × 50 iterations × 30 min | `internal/engine/toolloop.go:866-872`, `1755-1761`, `1797-1803` |
| RC6 | mid-loop compaction gate has no hysteresis: fires at `>usable`, rebuild targets `0.8×usable`, every new summary clears `sessionRehydrated`, memo keyed on `coldRangeHash` is invalidated by any new tool result → unbounded rebuild cycling (85/day observed on a real session) | `engine.go:3913` (`emitMidToolLoopRefreshExplicit`), `compaction.go:1113-1126` (`shouldAutoCompactForGate`), `compaction.go:397`, memo `compaction.go:344-347` |
| RC7 | no session-lifetime bound of any kind (no max age/messages/turns; CompletionOrchestrator auto re-prompts ≤3 per *interaction*, reset by user message; todoStore never cleared at session end; failed→active resurrection) | `engine.Config` (no field), `completion_orchestrator.go:137` |
| RC8 | code drift — comment says same_tool_pattern counts regardless of assistant text, but the detector requires `TrimSpace(responseContent)==""` → narrating models escape the cap | comment `engine.go:1286-1300` vs `toolloop.go:1544` |
| RC10 | always-active auto-inject works, but manifest skills missing on disk are silently dropped (no validation warning, no metrics on guard trips/rejections) | `internal/engine/skill_gate.go:48-67` (works), `internal/skill/loader.go:58-60` (silent `continue`), `internal/engine/skills.go` (`filterSkillsByName`) |

## Ledger

Expected ✅ = control the scenario pins as passing today. Expected ❌ =
violation documentation; the scenario must stay red until the fix lands.

| Scenario | Invariant | Expected | Actual (observed, trimmed) | Root cause | Proposed fix direction (NOT implemented) |
|---|---|---|---|---|---|
| turn_completes: Natural completion ends with a final assistant message | 1 | ✅ pass | **PASS** — transcript ends with assistant "All done."; one terminal chunk | — | — |
| turn_completes: same-tool-pattern cap ends with a final assistant message | 1 | ❌ fail | **FAIL** — `persisted transcript ends with role "tool" (content "ok")`; 21 `same_tool_pattern` cap trips, 60 todo-continuation requests before the sentinel | RC1, RC2 (RC3 keeps the loop alive) | Terminal paths must persist a final assistant message (store a bounded "stopped because…" notice) before emitting the sentinel Done |
| turn_completes: todo-continuation budget exhausted ends with a persisted terminal assistant message | 1 | ❌ fail | **FAIL** — `no persisted assistant message carries stop reason tool_loop_exceeded; terminal wire stop reasons were [tool_loop_exceeded]` (following transcript-ends assertion skipped, same path) | RC1, RC4b | Stamp the sentinel stop reason on the persisted terminal assistant message so session failover state can flip to `failed` |
| turn_completes: capped turn still emits exactly one terminal chunk on the wire | 1 (wire-level control) | ✅ pass | **PASS** on the wire — exactly one Done chunk. Transcript gap (final message is a tool_result) is recorded by the two scenarios above | RC1 (wire is fine; persistence is not) | — |
| turn_completes: permission-denied leaves no dangling tool_call | 1 | ❌ fail | **FAIL** — `dangling tool calls without a tool result: [call_0(echoer)]` | RC4c | On deny, persist a synthetic tool_result (`IsError: permission denied`) before returning, mirroring the exec-error path at `toolloop.go:1373-1393` |
| session_bounded: narration + identical tool calls trip the same-tool-pattern cap | 2 | ❌ fail | **FAIL** — `expected a same_tool_pattern cap trip, saw trips [iteration_backstop iteration_backstop]`; narration text resets the run every round | RC8 | Align the detector with its documented contract: count the tool-name pattern regardless of assistant text, or tighten the comment and add a narrating-model detector |
| session_bounded: todo no-progress guard trips despite ongoing tool activity | 2 | ❌ fail | **FAIL** — `expected the no-progress guard to stop the turn within 3 continuations, saw 20 continuation injections` (todo snapshot unchanged throughout) | RC3 | Derive progress from todo-state change (or tool-result content change), not from "any tool call happened" |
| session_bounded: continuation injection does not reset the duration budget | 2 | ❌ fail | **FAIL** — `turn ran for 3.40s, more than twice the original 150ms cap` (21 `total_tool_time_backstop` trips, 20 continuation injections) | RC5 | Give the turn (not the continuation window) a monotonic budget: keep `loopStart`/accumulated tool time across injections and decrement a remaining-budget counter |
| session_bounded: mid-loop compaction does not re-fire on marginal trim | 2 | ❌ fail | **FAIL** — `summariser ran 18 times` in one 42-iteration turn with a deliberately sliver-trimming summariser | RC6 | Add hysteresis (e.g. re-fire only when estimate exceeds `usable` by a margin, or back off after a fire that trimmed <N%); stop clearing `sessionRehydrated` unconditionally |
| session_bounded: a session has a lifetime bound | 2 | ❌ fail | **FAIL** — `no session-lifetime bound tripped: 6 auto-continued turns were accepted with 24 provider calls and 12 persisted messages, every turn completed naturally` | RC7 | Introduce a session-lifetime ceiling (max turns/messages/duration) enforced at Stream entry, with a distinct stop reason |
| session_bounded: background-task re-prompt chain is bounded | 2 | ✅ pass (control) | **PASS** — four sequential completions produced ≤3 re-prompt sends (existing `maxRePrompts=3` depth limit pinned) | pins current behaviour | — |
| skills_guarantee: always-active skill body in the first provider request | skills | ✅ pass | **PASS** — first request's system message embeds `INVARIANT_ALWAYS_ACTIVE_BODY` | pins current behaviour (`skill_gate.go:48-67`) | — |
| skills_guarantee: manifest skill missing on disk is reported | skills | ❌ fail | **FAIL** — `missing always-active skill "ghost-skill" was silently dropped: loader returned 1 of 2 requested skills, logged 0 distinct messages … and the API has no error return` | RC10 | Diff requested vs loaded names at load time; WARN (or fail validation) per missing skill, and add metrics for guard trips/rejections |

## Totals

- 13 scenarios: **4 controls PASS** (natural completion, single terminal
  chunk on the wire, re-prompt depth ≤3, always-active body in the first
  request), **9 FAIL documenting violations** (RC1, RC2, RC3, RC4b, RC4c,
  RC5, RC6, RC7, RC8, RC10).
- Step texts are pairwise disjoint from all 1038 pre-existing step
  definitions (godog v0.15.1 strict mode; no `ErrAmbiguous`).
- No production code, config, or existing scenario was modified. Failing
  scenarios are committed as-is per the repo's `[red]` convention.
