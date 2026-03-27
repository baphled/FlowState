# Learnings - delegation-ux-parity

## 2026-03-27 Session: ses_2cee73645ffe95v1MfRvmNxfIj - Initial Setup

### Codebase Structure
- internal/session/ - session types, manager, context
- internal/engine/ - delegation.go, background.go (BackgroundTaskManager)
- internal/streaming/ - events.go, consumer.go, runner.go
- internal/delegation/ - circuit_breaker.go, handoff.go (NO limits.go yet)
- internal/tui/ - app, intents, views, uikit, adapters
- internal/config/ - config.go (existing config system)

### Key Patterns
- DelegateTool lives in internal/engine/delegation.go
- BackgroundTaskManager in internal/engine/background.go (flat 50-semaphore)
- DelegationInfo in internal/provider/types.go (ChainID pattern)
- Session store in internal/session/manager.go
- Tests use Ginkgo v2 + Gomega (suite_test.go pattern)
- Commits MUST use: AI_AGENT="Claude" AI_MODEL="claude-sonnet-4-6" make ai-commit FILE=...

### Known Gaps to Fill
- No ParentID/ChildSessions in session types
- No CategoryConfig/routing types
- No ProgressEvent/CompletionNotificationEvent in streaming
- No SpawnLimits in delegation/
- No toast component in tui/
- DelegateTool schema missing category, subagent_type, load_skills, session_id

### 2026-03-27 Session Hierarchy Update
- Added ParentID and ParentSessionID to session.Session with explicit JSON tags.
- Added Manager.ChildSessions and Manager.SessionTree for parent/child traversal.
- Added SessionDepth for recursive depth calculation using live session maps.
- Focused Ginkgo specs confirmed RED before implementation and GREEN after minimal code.
- Full go test ./... passed after the hierarchy update.
