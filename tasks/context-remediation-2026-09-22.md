# Context-Window Remediation — 2026-09-22 — CONTINUATION REPORT (session 2)

**Status: TOOL LOOP BUDGET EXHAUSTED.** Work is present in the working tree but
unverified and uncommitted. No `make check` / `make bdd` run. Full state published
to coordination key `ctx-remediation/delivery-report`.

## Session 2 actions

- Inventoried uncommitted work (a prior continuation session had already written
  Tasks 1 & 2 implementation despite the original handoff claiming otherwise).
- Resolved the in-progress cherry-pick conflicts of `04256d84` in
  `internal/recall/query_tools.go` and `internal/recall/store.go`
  (kept HEAD doc-comment lines; 0 conflict markers remain). **Not yet `git add`ed
  or committed.**

## Current implementation state

### Task 1 — lean skill injection (implemented, unverified)
- `engine.LoadAlwaysActiveSkillNames` (names-only loader) added; app.go call site switched.
- `skill_gate.go` auto-injection now lean via `hook.BuildLeanInjection`.
- `engine_prompt.go` `assembleSystemPromptLocked` bakes lean `<available_skills>` header only.
- `skill_autoloader.go` `buildLeanInjection` exported as `BuildLeanInjection`; prose now directs
  `skill_load` on demand. Open question: `cache != nil` path still builds full `buildSkillContentBlocks`.
- BDD: `features/skill/lean_skill_injection.feature` + steps (untracked).

### Task 2 — in-flight tool result cap (implemented, unverified)
- `tool_result_cap.go`: shared `capToolResult`; `MaxInFlightToolResultChars` (24576);
  `capInFlightToolResult`; idempotent marker.
- `engine.go` `appendToolResultsBatchToMessages` caps in-flight results.
- BDD: `features/engine/inflight_tool_result_cap.feature` + steps (untracked).

### Task 4 — port 04256d84 (conflicts resolved, uncommitted)
Cherry-pick of model-invoked compaction (`truncate_context`, `pin_message`,
`Pinned` on `StoredMessage`) — conflicts resolved in working tree; needs
`git add` + `make ai-commit`.

### Task 3 — budget-guard series review (NOT STARTED)
Review `e7a2a2b8..d92c4dc3` for regressions.

### Task 5 — `make check` / `make bdd` (NOT RUN)

## Next-agent checklist (order matters)

1. `git add internal/recall/query_tools.go internal/recall/store.go`; check
   `git status` for active cherry-pick sequencer; commit via `make ai-commit`.
2. Decide the `buildSkillContentBlocks` cache-path question vs lean-only mandate
   and `tee_child_content` semantics (`internal/config/config.go:43`).
3. Run `make check` then `make bdd`; expect failures in godog_test.go wiring and
   tests that asserted full-body baking (system_prompt_skills_test.go, engine_test.go).
4. Commit Tasks 1+2 via `make ai-commit`.
5. Rewrite this note with final status and re-publish the coordination key.

## Constraints honoured
No direct `git commit` used; protected branches (main/next) untouched; branch
feature/context-remediation based on feature/agent-platform @ 678896da.
