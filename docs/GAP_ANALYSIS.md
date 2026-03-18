# FlowState Comprehensive Gap Analysis
Date: 2026-03-18

## Executive Summary

**Critical Gaps (P0):** The platform has working test infrastructure but several core E2E pathways are broken:
1. CLI chat fails with "all providers failed" because agent manifests default to OpenAI, not Ollama
2. TUI sendMessage goroutine doesn't feed chunks back to Bubble Tea update loop correctly
3. Session `--session` flag is declared but not wired to actually load/resume sessions
4. Provider coverage is 4-15% - no real integration tests

**What Genuinely Works:**
- All unit test suites pass (191 specs across 15 packages)
- BDD scenarios pass (58 scenarios, all green)
- Engine architecture with tool-call loop, failback chain, context management is implemented
- API endpoints respond correctly (agents, sessions, discover, skills)
- Session/Learning stores persist to disk

**The Gap:** The pieces exist but the E2E wiring is incomplete or misconfigured.

## Feature Status Matrix

| # | Feature | Status | Evidence | Fix Priority |
|---|---------|--------|----------|-------------|
| **Engine Layer** |||||
| 1 | Engine.Stream() with real provider | **BROKEN** | CLI fails: "all providers failed: %!w(<nil>)" - agent defaults to OpenAI, not Ollama | P0 |
| 2 | Tool-call loop | **PARTIAL** | `executeToolCall()` exists and is tested with mocks; no specific TestToolCall test exists | P1 |
| 3 | Failback chain | **WORKS** | 8 tests in engine_test.go lines 315-506, all pass | - |
| 4 | Agent delegation | **UNTESTED** | No TestDelegation test; delegation table is parsed but `delegate()` method not found | P1 |
| 5 | Always-active skills in system prompt | **WORKS** | Tests at lines 145-197 verify `BuildSystemPrompt()` includes skill content | - |
| 6 | Hook chain in chat flow | **WORKS** | `app.go:69-73` wires LoggingHook, LearningHook, ContextInjectionHook | - |
| **Context Layer** |||||
| 7 | ExternalContextStore | **WORKS** | FileContextStore at line 84-88, 70 specs pass | - |
| 8 | TokenBudget | **WORKS** | Part of ContextWindowBuilder, tested in context suite | - |
| 9 | ContextWindowBuilder | **WORKS** | `buildContextWindow()` at engine.go:294-309 | - |
| 10 | ContextQueryTools | **UNTESTED** | No specific test; tools not found in context package | P2 |
| 11 | Session persistence | **PARTIAL** | FileSessionStore works, but CLI `--session` flag is **NOT WIRED** to load/save | P0 |
| **Provider Layer** |||||
| 12 | Ollama provider unit tests | **BROKEN** | 4.0% coverage - only Name() and boolPtr() tested | P0 |
| 13 | OpenAI provider unit tests | **BROKEN** | 13.3% coverage - minimal testing | P1 |
| 14 | Anthropic provider unit tests | **BROKEN** | 15.6% coverage - minimal testing | P1 |
| 15 | Provider registration | **PARTIAL** | Only Ollama registered in `app.go:41-44`; OpenAI/Anthropic NOT registered | P0 |
| **Tool Layer** |||||
| 16 | Bash tool | **WORKS** | 7 specs, 100% coverage | - |
| 17 | File tool | **WORKS** | 8 specs, 86.2% coverage | - |
| 18 | Web tool | **WORKS** | 9 specs, 94.7% coverage | - |
| 19 | Tools wired into engine | **BROKEN** | `app.go` creates Engine without passing Tools to config (line 98-106) | P0 |
| **Discovery Layer** |||||
| 20 | Skill discovery | **WORKS** | 12 specs pass, threshold is 0.3 | - |
| 21 | Agent discovery | **WORKS** | 8 specs pass, 96.9% coverage | - |
| 22 | Discovery threshold | **CORRECT** | Uses 0.3 as coded (line 43); but CLI returns "No agents" if agents dir missing | - |
| **Learning Layer** |||||
| 23 | LearningStore | **WORKS** | 8 specs pass, 76.3% coverage | - |
| 24 | Learning wired in hooks | **WORKS** | `hook.LearningHook(learningStore)` at app.go:71 | - |
| **MCP Layer** |||||
| 25 | MCP manager | **STUB** | 10 specs pass, 100% coverage but Manager struct is minimal | P2 |
| **CLI Layer** |||||
| 26 | chat --message E2E | **BROKEN** | Calls engine.Stream() correctly but fails due to provider issues | P0 |
| 27 | chat interactive | **STUB** | Line 76: prints "TUI not wired yet" | P1 |
| 28 | session list | **WORKS** | Reads from FileSessionStore, outputs correctly | - |
| 29 | session resume | **STUB** | Line 57: just prints "Resuming session: {ID}" - no actual load | P1 |
| 30 | skill add owner/repo | **WORKS** | Importer clones from GitHub, validates frontmatter | - |
| **TUI Layer** |||||
| 31 | TUI sendMessage | **BROKEN** | Goroutine at lines 142-148 consumes chunks but never sends ChunkMsg to Bubble Tea | P0 |
| **HTTP API Layer** |||||
| 32 | POST /api/chat SSE | **WORKS** | Correctly streams chunks, tested with curl | - |
| 33 | GET /api/sessions | **PLACEHOLDER** | Line 143: returns `[]interface{}{}` - hardcoded empty array | P1 |
| **BDD Layer** |||||
| 34 | BDD scenarios passing | **WORKS** | 58 scenarios pass, 82.2% coverage | - |

## Critical Gaps (P0 — blocks real usage)

### 1. Provider Registration Incomplete
**Location:** `internal/app/app.go:41-44`

**Problem:** Only Ollama is registered. OpenAI and Anthropic providers exist but are NOT registered when environment variables are set.

**Evidence:**
```go
ollamaProvider, err := ollama.New(cfg.Providers.Ollama.Host)
if err == nil {
    providerRegistry.Register(ollamaProvider)
}
// OpenAI and Anthropic registration is MISSING
```

**Impact:** Agents with `model_preferences` pointing to OpenAI/Anthropic fail with "all providers failed".

**Fix:** Add conditional registration for OpenAI (`OPENAI_API_KEY`) and Anthropic (`ANTHROPIC_API_KEY`).

---

### 2. Agent Manifests Default to Unavailable Provider
**Location:** `agents/general.json`

**Problem:** The `general` agent's "standard" complexity uses OpenAI, not Ollama.

**Evidence:**
```json
"model_preferences": {
  "standard": [{"provider": "openai", "model": "gpt-4"}]
}
```

**Impact:** Default agent cannot stream because OpenAI isn't registered.

**Fix:** Either register OpenAI provider, or change default agent to use Ollama.

---

### 3. Tools Not Passed to Engine
**Location:** `internal/app/app.go:98-106`

**Problem:** The Engine is created but `Tools` field is not populated.

**Evidence:**
```go
eng := engine.New(engine.Config{
    ChatProvider:      defaultProvider,
    EmbeddingProvider: embeddingProvider,
    Registry:          providerRegistry,
    Manifest:          defaultManifest,
    Skills:            alwaysActiveSkills,
    Store:             contextStore,
    HookChain:         hooks,
    // Tools: MISSING
})
```

**Impact:** Tool calls from LLM will fail with "tool not found".

**Fix:** Add `Tools: buildTools()` or similar to engine config.

---

### 4. TUI sendMessage Goroutine Broken
**Location:** `internal/tui/chat.go:129-152`

**Problem:** The goroutine reads chunks but doesn't send them back to Bubble Tea.

**Evidence:**
```go
go func() {
    for chunk := range chunks {
        if chunk.Error != nil {
            return  // Silent exit
        }
        // MISSING: p.Send(ChunkMsg{Content: chunk.Content})
    }
}()
return nil  // Returns nil instead of continuing stream
```

**Impact:** Interactive TUI chat shows no AI responses.

**Fix:** Use `tea.Batch` or `tea.Exec` to send chunks back to update loop.

---

### 5. Session Flag Not Wired
**Location:** `internal/cli/chat.go:37`

**Problem:** `--session` flag is declared but never used in `runChat()`.

**Evidence:** Line 37 declares `opts.Session` but lines 43-74 never reference it.

**Impact:** Cannot resume previous sessions from CLI.

**Fix:** Load session from store if `opts.Session` is set.

---

### 6. Provider Integration Tests Missing
**Location:** `internal/provider/*/`

**Problem:** Provider packages have 4-15% coverage; they test Name() but not Stream/Chat/Embed.

**Evidence:**
- Ollama: 4.0% coverage
- OpenAI: 13.3% coverage
- Anthropic: 15.6% coverage

**Impact:** Provider implementations may have bugs that unit tests don't catch.

**Fix:** Add integration tests with real or mocked HTTP servers.

---

## Important Gaps (P1 — degrades experience)

### 7. Interactive Chat Not Implemented
**Location:** `internal/cli/chat.go:76`

**Impact:** `flowstate chat` without `--message` shows stub message.

**Fix:** Wire up TUI model from `internal/tui/chat.go`.

---

### 8. Session Resume Stub
**Location:** `internal/cli/session.go:56-59`

**Impact:** `flowstate session resume ID` prints message but doesn't actually resume.

**Fix:** Load session from store and start chat with context.

---

### 9. API Sessions Placeholder
**Location:** `internal/api/server.go:143`

**Impact:** `GET /api/sessions` returns `[]` even when sessions exist.

**Fix:** Wire to `Sessions.List()` like CLI does.

---

### 10. Agent Delegation Untested
**Location:** `internal/engine/engine.go`

**Impact:** Delegation table is parsed in manifests but no delegation logic found.

**Fix:** Implement and test delegation.

---

## Minor Gaps (P2 — nice to have)

### 11. MCP Manager Minimal
Currently a stub with basic lifecycle tests.

### 12. Context Query Tools Missing
No dedicated tools for querying context.

### 13. Test Coverage Below 95%
Several packages below target: agent (38%), CLI (64%), skill (66%).

---

## What Is Genuinely Working End-to-End

1. **BDD Test Infrastructure** — 58 scenarios pass, covering agent discovery, streaming, sessions, permissions
2. **Engine Architecture** — Failback chain, tool-call loop, context windowing all implemented and unit tested
3. **Tool Implementations** — Bash, File, Web tools work correctly (86-100% coverage)
4. **Discovery System** — Agent and skill discovery with weighted token matching
5. **Session Persistence** — FileSessionStore saves/loads sessions correctly
6. **Learning Capture** — LearningHook captures and persists learnings
7. **HTTP API Structure** — All routes defined and respond correctly (except /api/sessions data)
8. **Build System** — `make build`, `make test`, `make bdd-smoke` all work

---

## Recommended Fix Order

1. **P0-1:** Register OpenAI/Anthropic providers when env vars set
2. **P0-2:** Update `agents/general.json` to use Ollama as fallback
3. **P0-3:** Pass Tools to Engine config
4. **P0-4:** Fix TUI sendMessage to properly send chunks
5. **P0-5:** Wire `--session` flag to load sessions
6. **P0-6:** Add provider integration tests (at least mock HTTP)
7. **P1-7:** Wire interactive chat to TUI
8. **P1-8:** Implement session resume
9. **P1-9:** Wire API /sessions to real data
10. **P1-10:** Implement and test delegation

---

## Test Commands Summary

```bash
# All unit tests
go test ./... -v

# BDD smoke tests
make bdd-smoke

# Coverage report
go test ./... -coverprofile=cover.out && go tool cover -func=cover.out

# E2E CLI test (requires Ollama running)
./build/flowstate chat --message "hello" --agents-dir ./agents --agent general

# API test
./build/flowstate serve --agents-dir ./agents --port 8080 &
curl -s http://localhost:8080/api/agents
curl -s -X POST http://localhost:8080/api/chat -H "Content-Type: application/json" -d '{"agent_id":"general","message":"hi"}'
```
