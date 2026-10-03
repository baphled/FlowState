# Blackhole Artefact Audit — FlowState

Date: 2026-09-21 · Method: SysOp bash audit (grep/find/git across all worktrees)

## Result

"Blackhole" exists only as a Go test-harness identifier: `testutils.BlackholeServer`.

- **Definition:** `internal/testutils/streamguard.go` (lines 12–26) — `BlackholeServer(selfReturnAfter time.Duration) *httptest.Server`. An httptest server that accepts requests but never responds, used to test stream-guard/timeout behaviour.
- **Consumers:** `internal/provider/{anthropic,ollamacloud,openai,openaicompat,openzen,zai}/stream_guard_test.go` (e.g. `testutils.BlackholeServer(8 * time.Second)`).
- **Build artifacts:** compiled binaries in `build/` (`flowstate`, `flowstate.bak`, `flowstate-previous-working`, `flowstate-questionfix`, `flowstate-pre-question-fix`, `docblocks`, `harness.test`) contain the string.

## Distribution

| Worktree | Branch | Has it? |
|---|---|---|
| agent-platform | feature/agent-platform | Yes (source + tests + artifacts) |
| budget-guard-loop | feature/budget-guard-loop-detection | Yes |
| fix-session-cache-headers | fix/session-cache-headers | Yes |
| gate-registration-resilience-ap | feature/gate-registration-resilience-ap | Yes |
| next-wt | next (protected) | Yes |
| tool-input-whole-capture | feature/tool-input-whole-capture | Yes |
| feature-notifications (external dir) | feature/notifications | Yes |
| **main** | main (protected) | **No** — no `internal/testutils/streamguard.go` |

## Origin

Commit `6a39244a` — "refactor(test): centralise stream-guard harness in internal/testutils". Present on `next` and all feature branches; not yet merged to `main`.

No branches, commit messages, files, directories, docs, or configs contain "blackhole" otherwise.
