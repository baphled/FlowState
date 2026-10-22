# FlowState agent-platform — Bug & Quality Review (2026-09-27)

Reviewed branch: `feature/agent-platform` (068ad621), read-only. Note: `main` worktree does NOT contain the engine/session/app packages — the agent-platform code lives in the `agent-platform` worktree.

## HIGH

1. **`internal/app/app.go:4277` — learning hook misattributes AgentID.** Records use `toolEvt.Data.SessionID` as `AgentID`; the event type lacks AgentID and the engine's `activeAgentID` isn't threaded through `publishToolAfterEvent`. Fix: add `AgentID` to `ToolExecuteResultEventData`.
2. **`internal/plugin/failover/healthmanager.go` (~line 700, `GetHealthyAlternatives`) — failover candidate set incomplete.** Returns only previously-failed-then-recovered pairs; never-failed healthy providers are invisible, so candidate selection can come up empty. Fix: seed candidates from the configured provider list, not just the failure map.

## MEDIUM

3. **`healthmanager.go` — disk I/O under lock.** `Flush` / `maybePersistLocked` / `MarkPermanentFailure` marshal+rename while holding `hm.mu`; slow disk stalls all `IsRateLimited` reads. Fix: snapshot under lock, persist outside.
4. **`openaicompat.go` — missing Done chunk.** `!sawFinish` path (stream ends without finish_reason) emits no `Done` chunk; consumers relying on Done can hang until turn deadline. Fix: emit synthetic Done in epilogue unconditionally.
5. **`internal/session/manager.go:1980` — recorder loop can block cleanup.** `finalCh <- chunk` forwarded without ctx select; stalled consumer blocks inflight-map cleanup indefinitely. Fix: ctx-aware select (mirroring the accumulator fix).

## LOW / standards

6. Widespread inline comments inside function bodies (e.g. `openaicompat.go:472-530`) despite the AGENTS.md ban — apparently tolerated by the check-note-comments gate; clarify or ratchet.
7. `internal/engine`: 109 test files, only 3 `_internal_test.go` — 106 orphan per-aspect files vs the foo_test/foo_internal_test convention (grandfathered in `scripts/test-file-baseline.txt`).
8. Good marks: doc.go present in all in-scope packages; British English holds; no TODO/FIXME in core files; anthropic streaming handler clean (index keying, tool_use normalisation, usage suppression all sound).

## Follow-ups

- `make test` was launched but didn't complete before the review budget exhausted — verify green via `/tmp/fs-make-test.log` or re-run.
- Prior resolved-but-noteworthy: `execution.Loop.runLoop` nil-streamer SIGSEGV (fixed ae6c51a1).
