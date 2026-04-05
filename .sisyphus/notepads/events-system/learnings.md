# Events System Plan - Learnings

## 2026-04-05 Session: ses_2a17348edffe0xitbR8GBYNsGI - Plan Start

### Architecture Facts (from memory/investigation)
- EventBus uses EXACT string matching for topics — no wildcard/prefix matching in internal bus
- Existing "*" catch-all topic support at eventbus.go:85-98 — MUST NOT break
- Two parallel extension planes: synchronous hook chain (mutable, internal) + EventBus (fire-and-forget, observational)
- External plugins: JSON-RPC over stdio with 4 declared hook types
- BackgroundTaskManager (engine/background.go) publishes background.task.started/completed/failed

### CRITICAL TYPE MISMATCHES (from investigation 2026-04-04)
1. **Dispatcher (T2)**: subscribeDispatcherHooks (app.go:1414-1427) expects *external.ToolExecArgs but engine publishes *events.ToolEvent — silent failure
2. **Detector (T3)**: detector.go:81 asserts *events.ProviderEvent but engine publishes *events.ProviderErrorEvent — rate-limit detector never fires from engine errors
3. **chat.params (T4)**: ChatParamsHook declared in manifest protocol but NO wiring in subscribeDispatcherHooks
4. **SessionRecorder (T5)**: extractEventData missing BackgroundTask cases

### Implementation Record (from 2026-04-04 feature/agent-platform commits)
- Wildcard EventBus subscription '*' already added
- 20 event type constants in types.go (16 pre-existing + 4 new)
- 4 new event types added: session.resumed, tool.execute.error, tool.execute.result, provider.request.retry
- Engine triple-publishes: tool.execute.after + tool.execute.result OR tool.execute.error
- BackgroundTask constants use inconsistent `EventType*` prefix (not `Event*`) — T7 fixes this

### Codebase Paths
- Event types: internal/plugin/events/types.go, internal/plugin/events/events.go
- EventBus: internal/plugin/eventbus/eventbus.go
- Engine: internal/engine/engine.go, internal/engine/background.go, internal/engine/background_cancel.go
- App wiring: internal/app/app.go:1407-1443
- Dispatcher: internal/plugin/external/dispatcher.go
- EventLogger: internal/plugin/eventlogger/eventlogger.go
- SessionRecorder: internal/plugin/sessionrecorder/recorder.go
- Detector: internal/plugin/failover/detector.go
- Event bridge: internal/api/event_bridge.go

### Go Module / Commit Rules
- ALWAYS use: AI_AGENT="Claude" AI_MODEL="claude-sonnet-4-6" make ai-commit FILE=/tmp/commit.txt
- NEVER use git commit directly
- Valid commit scopes: agent, api, cli, config, context, core, database, deps, discovery, docs, event, handler, hook, learning, mcp, middleware, plugin, prompt, provider, release, repository, service, skill, test, tool, transport, tui, validation, workflow
- NOT valid: 'engine' — use 'event' or 'core'

### Testing
- Framework: Ginkgo v2 + Gomega
- Pattern: Characterisation spec (proves broken) → Fix → Verification spec (proves fixed)
- Run: go test -race -count=1 ./internal/...
- BDD: make bdd, make bdd-smoke

## T1 Baseline Results
- make check: PASS
- Total tests: 4008
- Pass: 4008
- Fail: 0
- Lint warnings: 0
- Build status: PASS
- go test -race -count=1 ./internal/plugin/... ./internal/engine/... ./internal/app/... ./internal/api/...: FAIL
- Total tests: 384
- Pass: 384
- Fail: 0
- Race detected: yes
