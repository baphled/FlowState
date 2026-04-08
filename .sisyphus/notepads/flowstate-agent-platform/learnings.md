# Learnings

## 2026-03-17 Session: ses_302a3f473ffei4QswQo2BTlywI

### Worktree
- Working in: `/home/baphled/Projects/FlowState.git/agent-platform`
- Branch: `feature/agent-platform` (off `main` at 70f65c8)
- Protected branches: `main`, `next` — never modify directly

### Plan Adjustments Applied
1. Commit template: `AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5"`
2. T19: 6 mandatory always-active skills must be in sample manifests: pre-action, memory-keeper, token-cost-estimation, retrospective, note-taking, knowledge-base
3. F4: KB Curator trigger added after scope verification passes

### Key Architecture Decisions
- TRUE RLM: LLM never gets full conversation — queries its own history via tools
- Embeddings use SEPARATE provider from chat (embeddingProvider reference in Engine)
- Anthropic has NO embedding API — falls back to recency-only
- No SQLite, no vector DB — file-backed only
- All channels buffered size 16
- float64 for embeddings (native Ollama return type)
- Tool output messages stored but NOT embedded (too noisy)

### Commit Convention
```bash
AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5" make ai-commit FILE=/tmp/commit.txt
```

## 2026-03-17 Constraints Update (user corrections)

### .sisyphus must NEVER be committed
- Added `.sisyphus/` to `.gitignore`
- Applies to all worktrees — planning artefacts stay local only

### Commit rule (ABSOLUTE — NO EXCEPTIONS)
- ALWAYS: `AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5" make ai-commit FILE=/tmp/commit.txt`
- NEVER: `git commit` directly
- Team-Lead and all delegates must enforce this

### CI + Code Quality (MANDATORY — baked into T1 scaffolding)
- golangci-lint MUST be configured (`.golangci.yml`) modelled on KaRiya
- KaRiya config at: `/home/baphled/Projects/KaRiya/.golangci.yml`
- `make lint` must run golangci-lint (not just `go vet`)
- `make check` = fmt + lint + test — must all pass before any commit
- `make check-compliance` target should run full quality gate
- CI workflow (`.github/workflows/ci.yml`) must run on every push/PR
- CI must: build, test, lint, bdd-smoke

### Legacy features — DELETE, do not retag
- `features/navigation/vim_motions.feature` → DELETE
- `features/commands/command_palette.feature` → DELETE
- `features/commands/model_management.feature` → DELETE (out of scope for PoC)
- `features/ui/task_panel.feature` → DELETE (out of scope)
- `features/sessions/session_management.feature` → KEEP (renamed/merged into session_persistence)
- `features/tools/bash_tool.feature` → KEEP (in scope)
- No `@legacy` tag — just remove the files entirely

### Team-Lead delegation rule
- Team-Lead MUST use task() to delegate ALL implementation
- Team-Lead NEVER writes code directly
- Each wave = one or more task() calls to Senior-Engineer / specialist agents
- Verify after each wave with make build && make test before proceeding

## 2026-03-17 Constraints Update #2 (user corrections)

### Testing framework: Ginkgo/Gomega (NOT godog/testify)
- Unit + integration tests: Ginkgo v2 + Gomega (NOT go test + testify)
- BDD acceptance tests: Godog (Cucumber/Gherkin) — kept for outside-in BDD scenarios
- go.mod MUST add: github.com/onsi/ginkgo/v2, github.com/onsi/gomega
- All `*_test.go` files use Ginkgo Describe/It/Expect pattern
- Each package needs a suite_test.go bootstrapping RunSpecs
- golangci-lint: ginkgolinter enabled (already in KaRiya config)
- make test runs: go test ./... (which runs Ginkgo suites)
- KaRiya uses Ginkgo — reference: /home/baphled/Projects/KaRiya/

### Team-Lead delegation discipline (CRITICAL)
- Team-Lead is a COORDINATOR ONLY — never writes code, never edits files
- Every implementation task → task() call to Senior-Engineer or specialist
- Wave 1b (T2-T7): 6 parallel task() calls in ONE message
- Wave 2 (T8-T14): 7 parallel task() calls in ONE message
- Team-Lead only runs Bash for VERIFICATION after delegates complete
- Team-Lead reads notepads, reads plan, delegates, verifies — nothing else

### Current uncommitted state (as of this session)
- Deleted: features/navigation/vim_motions.feature
- Deleted: features/commands/command_palette.feature
- Deleted: features/commands/model_management.feature
- Deleted: features/ui/task_panel.feature
- New: .github/workflows/ci.yml
- New: .golangci.yml
- New: internal/agent/loader.go, loader_test.go, manifest.go (partial Wave 1b)
- New: internal/provider/failback.go, registry.go, types.go (partial Wave 1b)
- New: internal/skill/loader.go, types.go (partial Wave 1b)
- New: internal/tool/registry.go, types.go (partial Wave 1b)
- Modified: .gitignore (.sisyphus/ added)
- All of the above need to be committed as Step 1 + partial Wave 1b

## 2026-03-18 Commit Convention Fix

### Problem
Previous commits used inconsistent trailer formats:
- `Co-authored-by:`, `AI-Agent:`, `AI-Model:`, `AI-assisted-by:` — all WRONG

### Canonical format (ENFORCED)
```
AI-Generated-By: Opencode (claude-opus-4.5)
Reviewed-By: Yomi Colledge <baphled@boodah.net>
```

### How to commit (NO EXCEPTIONS)
```bash
printf 'feat(scope): description\n' > /tmp/commit.txt
AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5" make ai-commit FILE=/tmp/commit.txt
```

### NEVER use
- `git commit -m "..."` — FORBIDDEN
- `git commit --amend` — FORBIDDEN unless orchestrator explicitly authorises
- Any other trailer format

### Git config
- `commit.gpgsign=false` set in worktree local config (no signing required)
- All 18 previous commits rewritten via `git filter-branch --msg-filter` to use canonical trailers

## 2026-03-18 T34: ContextWindowBuilder

### Implementation Approach
- T34 builds minimal context per turn using token budget
- Always-active skills are NOT loaded by ContextWindowBuilder — they're composed into the system prompt by Engine (T15a)
- Builder receives pre-composed system prompt via `manifest.Instructions.SystemPrompt`
- Clean separation: Engine composes, Builder assembles within budget

### Assembly Order
1. System prompt (includes always-active skill content from T15a)
2. State summary (if provided)
3. Semantic search results (deduplicated)
4. Sliding window messages (deduplicated against semantic results)

### Key Patterns
- `TokenBudget` struct tracks used tokens by category (system, summary, semantic, sliding)
- Deduplication via `seenIDs` map keyed by message ID
- Truncation via character-ratio approximation (0.9x safety margin)
- Default sliding window size: 10 messages

### Test Structure
- Single `TestWindowBuilder` entry point in `suite_test.go`
- Ginkgo Describe/Context/It blocks for BDD-style specs
- 67 specs covering all behaviors + edge cases

## 2026-03-18 T34 Alignment Update

### API Additions for T15a Compatibility
- Added `BuildContext(ctx, manifest, userMessage, store, tokenBudget) []provider.Message`
- This is the T15a-compatible entrypoint that:
  - Accepts `context.Context` for cancellation propagation
  - Accepts `userMessage` string to append to context window
  - Returns `[]provider.Message` directly (not BuildResult)
  - Enables warning logging for truncation

### Warning Behavior
- When system prompt exceeds token budget, logs: `warning: system prompt truncated from X to Y tokens (budget: Z)`
- Warning only logged via `BuildContext` (logWarnings=true), not via internal `Build` calls
- Uses standard `log.Printf` matching project convention (see `internal/agent/registry.go`)

### Test Coverage
- 70 specs total covering:
   - Cold start (system prompt only)
   - Sliding window messages
   - Token budget enforcement with skill content
   - System prompt truncation
   - Oversized message truncation
   - Deduplication
   - Assembly order
   - State summary inclusion
   - Semantic search results
   - BuildContext T15a entrypoint
   - Warning on truncation path

## 2026-04-07 Tool Docblock Remediation

### What changed
- Added structured docblocks to the newly introduced internal/tool packages.
- Brought exported tool constructors and methods into the FlowState three-section format: Expected, Returns, Side effects.
- Added plain doc comments to unexported helper functions and small internal types.

### What mattered
- `make check-docblocks` now reports no `internal/tool/` issues for the touched files.
- `lsp_diagnostics` on `internal/tool` returned zero errors after the documentation-only pass.

### Pattern
- For tool packages, keep comments mechanically consistent and treat documentation-only changes as first-class maintenance work.


## T20: TUI Chat — Bubble Tea wrapping Engine

### Patterns Applied

- **BDD-first**: Wrote 19 Ginkgo tests before implementation
- **Bubble Tea Model-View-Update**: Followed Elm architecture with Model struct containing all state, Update returning (Model, Cmd), View pure render
- **Custom tea.Msg types**: ChunkMsg, StreamDoneMsg, ErrorMsg for streaming flow
- **Modal editing**: normal/insert mode pattern similar to vim — clean separation of navigation vs typing
- **Accessor methods**: Mode(), Input(), IsStreaming() etc. for testability without exposing struct fields

### Key Implementation Details

1. **Message types** must be exported so tests can send them directly to Update()
2. **Engine.Stream() signature**: `Stream(ctx, agentID, message) (<-chan StreamChunk, error)`
3. **Test pattern**: BeforeEach creates mockProvider → engine.New() → tui.NewModel() → direct Update() calls
4. **No blocking in tests**: Never call Program.Run(), only test Model directly

### Files Created

- `internal/tui/chat.go` - Model, NewModel, Init, Update, View + accessor methods
- `internal/tui/run.go` - Run() wrapper starting tea.Program with AltScreen
- `internal/tui/suite_test.go` - Ginkgo bootstrap

## 2026-04-01 Provider model lists

- Z.AI and OpenZen now fetch model lists from `client.Models.List(ctx)` first.
- Fallback lists should stay minimal and provider-specific so startup still works if the API is unavailable.
- The OpenAI Go SDK returns model IDs directly from `Models.List`, so mapping stays simple and isolated in each provider.

## 2026-03-30 Agent Manifest Cleanup

- Removed `model_preferences` from all seven built-in agent markdown manifests under `internal/app/agents/`.
- Kept the rest of each frontmatter block intact and left the markdown bodies untouched.
- Provider selection is now left to configuration rather than agent manifests.
- `internal/tui/chat_test.go` - 19 specs covering all behaviours

## 2026-03-18 T31: Skill Importer

### Implementation

- Created `internal/skill/importer.go` with `Importer` struct
- Key methods:
  - `NewImporter(skillsDir string) *Importer`
  - `Add(ctx, ownerRepo string) (Skill, error)` - clones from GitHub
  - `AddFromPath(ctx, repoPath string) (Skill, error)` - for testing without git
- Error types: `ErrInvalidSkill`, `ErrSkillExists`

### Validation Rules

- Name MUST be present in frontmatter
- Description MUST be present in frontmatter
- Target directory must not already exist (collision detection)
- SKILL.md can be in root or any subdirectory (walks tree to find first one)

### Testing Pattern

- Tests use `AddFromPath()` with temp directories simulating cloned repos
- No actual git clone in tests — creates temp dir with SKILL.md directly
- 5 test cases: valid install, missing name, missing description, collision, nested subdirectory
- All use Ginkgo v2 with dot imports

### Git Clone Details

- Uses `git clone --depth 1` for shallow clone
- Clones to temp dir, processes, then removes temp dir (defer cleanup)
- Reuses existing `extractFrontmatter()` and YAML parsing from loader.go

## 2026-03-26 Session: T21 - Fix build errors and add embedding tests

### Build Errors Fixed

1. **resolveTargetWithOptions incomplete (errors 1&2)**: Missing `resolveWithDiscovery` call before constructing `delegationTarget`. Fix: wire the call between chainID assignment and the return statement.

2. **Unexported field access (error 3)**: `d.embeddingDiscovery.embedder` — the `embedder` field is unexported. The `Match()` method handles nil embedder internally (returns empty). Fix: check only `d.embeddingDiscovery != nil`.

3. **Cosine similarity bug (error 4)**: Formula was `dot / (normA * normB)` — missing `math.Sqrt()`. Correct formula: `dot / (math.Sqrt(normA) * math.Sqrt(normB))`.

4. **Godoc missing (error 5)**: `CosineSimilarity` lacked Expected/Returns/Side effects sections.

### Linter Issues Encountered After First Fix

- `contextcheck`: `resolveTargetWithOptions` called `context.Background()` internally via `resolveWithDiscovery`. Fix: propagate `ctx` from `Execute` → `resolveTargetWithOptions(ctx, input)` → `resolveWithDiscovery(ctx, taskType, message)`.
- `nilerr`: `Match()` swallowed embed errors. Fix: return wrapped error.
- `revive: public interface method not commented`: `EmbeddingProvider.Embed` needed godoc.
- `revive: import-shadowing`: Parameter named `discovery` shadowed the `discovery` package import. Fix: rename to `ed`.
- `unparam: always false bool return`: `resolveWithDiscovery` returned `(string, bool, error)` but bool was always `false`. Fix: remove bool from return signature.

### Pattern: LSP shows workspace-context errors

The LSP in this worktree shows many errors from the agent-platform workspace (different module). Always verify with `go build ./...` rather than trusting LSP diagnostic output. The LSP errors for `internal/agent`, `internal/discovery` etc. were all false positives.

### Tests Created

- `internal/discovery/embedding_test.go` — Ginkgo v2 specs for `EmbeddingDiscovery.Match` and `CosineSimilarity`
- Mock embedder pattern: `fixedEmbedder` struct with `map[string][]float64` for deterministic vectors
- `suite_test.go` already existed; no need to recreate it

## 2026-03-26 Session: T22 — KB Documentation (Deterministic Planning Loop)

### KB Notes Written

Six permanent reference notes created at:
`/home/baphled/vaults/baphled/1. Projects/FlowState/deterministic-planning-loop/`

| File | Content |
|------|---------|
| `PLANNING_LOOP.md` | 3-role loop (coordinator→writer→reviewer), circuit breaker, Mermaid sequence diagram |
| `CREATING_AGENTS.md` | Manifest schema, capability_description ML discovery, delegation_table registration |
| `DELEGATION.md` | Handoff struct, DelegateTool, wireDelegateToolIfEnabled, createDelegateEngine, resolveWithDiscovery |
| `EVENTS.md` | All 7 event types with JSON examples, harness_retry, SSE/curl consumption |
| `EXECUTOR.md` | Expanded plan format (plan.File, PlanContext, WorkObjectives, Task), PlanHarness retries |
| `RESEARCH_AGENTS.md` | Explorer/Librarian/Analyst scopes, Coordination Store key schema, custom agent guide |

### Key Facts Confirmed from Source

- `PlanHarness.maxRetries` = **3** (hardcoded in `NewPlanHarness()` — `internal/plan/harness.go:104`)
- `CircuitBreaker` states: `closed → open → half_open`; default `maxFailures` passed by caller
- `ChainID` format: `chain-{time.Now().UTC().UnixNano()}`
- Coordination Store key pattern: `{chainID}/{purpose}`
- `plan-writer` has `harness_enabled: true`; all other agents have it `false`
- ML discovery threshold: confidence **≥ 0.70** (`resolveWithDiscovery`)
- `wireDelegateToolIfEnabled` and `createDelegateEngine` live in `internal/app/app.go` (not `internal/engine/`)
- Task has `Wave int` field for parallel task batching across waves

### Conventions Used

- British English throughout (recognise, synthesise, behaviour, etc.)
- Obsidian YAML frontmatter with ISO 8601 `created:` dates
- `[[wikilinks]]` cross-linking all 6 notes to each other
- Mermaid sequence diagrams in PLANNING_LOOP and RESEARCH_AGENTS
- Added /docblocks and /.tmp to .gitignore to keep build/session artefacts out of version control.

## F4 Scope Fidelity v2 Re-Verification (2026-03-26)

- The `docblocks` binary and `.tmp` session file fix (commit 05b7a84) was confirmed clean via `git ls-files`.
- `internal/server/server.go` does not exist — server binding logic is in `internal/cli/serve.go` (defaults to localhost).
- `internal/api/server.go` is the HTTP handler (routes), not the listener.
- LSP diagnostics show false positives across `streaming/`, `session/`, `api/` — ignore them; `go build` and `make check` are authoritative.
- `tmp/nvim.baphled/*` and `internal/oauth/github.go.orig` are pre-existing tracked artefacts not introduced by this feature branch.
- `make check` exits 0 with 82.0% coverage (7359/8974 statements), zero failures across 60+ packages.

## F3 QA: JSON Consumer Architecture Pattern

**Date**: 2026-03-26
**Context**: Debugging why `--output json` produced plain text

### Pattern: Consumer-Writer Separation

FlowState uses a StreamConsumer interface pattern where the consumer encapsulates
the output format. BUT `runSingleMessageChat` always passes `io.Discard` to the consumer:

```go
// chat.go line 111 — WRONG
response, err := streamChatResponse(application, agentName, opts.Message, opts.Output, io.Discard)
// chat.go line 118 — writes plain text regardless
_, err = fmt.Fprintf(cmd.OutOrStdout(), "Response: %s\n", response)
```

The design intent is clear (JSONConsumer writes JSON events, WriterConsumer writes text),
but the wire-up is broken. The consumer's writer should be cmd.OutOrStdout() for JSON mode.

### Lesson

When reviewing CLI commands with multiple output modes, always trace the writer
from command.OutOrStdout() through to the actual consumer. io.Discard is a common
placeholder that survives refactoring and silently breaks output.

## F3 QA: SSE Infrastructure Is Correct

**Date**: 2026-03-26

The SSE server (`flowstate serve`) correctly:
- Sets Content-Type: text/event-stream
- Sets Cache-Control: no-cache  
- Sets Connection: keep-alive
- Uses Transfer-Encoding: chunked
- Prefixes all events with `data:` on their own lines
- Binds to localhost only (security requirement)

The infrastructure is sound. SSE events would appear if LLM credentials were present.

## F3 QA: Streaming Event Type Name Convention

**Date**: 2026-03-26

The streaming package (internal/streaming/events.go) defines event types as snake_case strings:
- "text_chunk", "tool_call", "delegation", "coordination_store"
- "status_transition", "plan_artifact", "review_verdict"

The Go struct names are PascalCase (TextChunkEvent, DelegationEvent, etc.)
Do NOT confuse struct names with JSON type discriminator values.

## F3 v2 QA: io.Discard Fix Confirmed — NDJSON Events Flow to Stdout

**Date**: 2026-03-26
**Context**: Re-verification after commit 0febcc7 fix

### Fix Pattern

The fix in `internal/cli/chat.go` follows a clean idiom:
```go
writer := io.Discard       // default: text mode discards consumer output
if opts.Output == "json" {
    writer = cmd.OutOrStdout()  // json mode: route consumer output to stdout
}
response, err := streamChatResponse(application, agentName, opts.Message, opts.Output, writer)
...
if opts.Output == "json" {
    return nil  // early return: no plain-text write in json mode
}
_, err = fmt.Fprintf(cmd.OutOrStdout(), "Response: %s\n", response)
```

This cleanly separates text mode (consumer buffered, final response written) from
json mode (consumer events streamed directly to stdout, no final write).

### Evidence

With `--output json`, the binary produces NDJSON events:
- `{"content":"...","type":"chunk"}` — LLM text chunks
- `{"name":"...","type":"tool_call"}` — tool invocations  
- `{"content":"...","type":"tool_result"}` — tool results
- `{"type":"done"}` — completion sentinel
- `{"type":"delegation","source":"...","target":"...","status":"..."}` — delegation events

15 events observed in a single executor run.

## F3 v2 QA: Session API Field Names Now Lowercase

**Date**: 2026-03-26

After commit 0febcc7 (also fixes session json tags), the session API correctly
returns lowercase JSON fields:
- `id` (was `ID`)
- `agent_id` (was `AgentID`)
- `status` (was `Status`)
- `created_at` (was `CreatedAt`)

Verified with: python3 check for `'id' in d and 'agent_id' in d` → `True True`

## Code Review: deterministic-planning-loop (2026-03-26)

### Pattern: Non-doc.go files with package-level comments
- `circuit_breaker.go`, `background.go`, `delegation.go`, `server.go` all had duplicate package comments alongside their `doc.go` counterparts
- Go convention: only `doc.go` should carry the package-level `// Package …` comment
- Other files in the same package should NOT repeat or add package comments

### Pattern: Thought-process comments inside function bodies
- `delegation_status.go:47–54` contained 7 lines of implementation uncertainty comments inside `Update()` — classic violation of the no-comments-in-function-bodies rule
- These are easier to miss when they occur inside nested conditionals

### Pattern: Status string literals vs typed constants
- `session/manager.go` and `engine/background.go` both used bare string literals for state machines
- `"active"`, `"completed"`, `"running"`, `"pending"`, `"failed"`, `"cancelled"` scattered across files
- Should be typed constants (e.g. `type SessionStatus string; const SessionStatusActive SessionStatus = "active"`)

### Pattern: Sentinel error anti-pattern
- `background.go` initialised sentinel errors via wrapper functions (`errTaskNotFoundFn()`)
- Idiomatic Go: just `errors.New(...)` directly in var block
- Wrapper functions add complexity without benefit

### Pattern: Magic float64 constant
- `delegation.go:480` used `0.7` as a confidence threshold without naming it
- `const minEmbeddingConfidence = 0.7` would make intent clear

### What passed cleanly:
- Architecture boundaries: no TUI in engine/session/coordination layers
- No nolint, TODO, FIXME, HACK
- Consistent error wrapping with %w
- Proper sync.RWMutex usage throughout
- golang.org/x/text/cases used (not deprecated strings.Title)
- io.Discard fix in chat.go correctly applied

## todowrite Tool Implementation (2026-03-27)

### Pattern: Session-Scoped Tool via Context
- Session ID injected into context in `session/manager.go` `SendMessage()` via `context.WithValue(ctx, todo.SessionIDKey{}, sessionID)`
- Tool retrieves it via `ctx.Value(SessionIDKey{}).(string)` — returns error if missing
- `SessionIDKey` struct defined in tool package, not session package (avoids import cycle)

### Naming Convention
- `revive` lint rule `exported.sayRepetitiveInsteadOfStutters` fires for `TodoItem` in `todo` package
- Correct name: `Item` (consumers use `todo.Item`, not `todo.TodoItem`)
- Same pattern applies to any package where type name would duplicate package name

### Docblock Requirements (check-docblocks)
- Every exported AND unexported function needs: `Expected:`, `Returns:`, `Side effects:` sections
- Format from bash/coordination tool pattern — mandatory in this project
- Private functions also need these sections

### fatcontext Lint Rule
- `ctx = context.WithValue(context.Background(), ...)` in `BeforeEach` triggers fatcontext
- Fix: use package-level helper function `sessionCtx()` that returns the context — no outer var assignment
- Or use local `ctx := sessionCtx()` in each `It` block directly

### errcheck with type assertions
- `check-type-assertions: true` means `v, _ := m[key].(string)` is flagged
- Fix: `v, ok := m[key].(string); if !ok { return "" }`

### Boy Scout Rule Applied
- Fixed pre-existing `gocritic rangeValCopy` in `cli/chat.go`, `cli/run.go`, `tui/intents/chat/intent.go`
- Pattern: `for _, s := range loadedSkills` → `for i := range loadedSkills { loadedSkills[i].Name }`

## Todo Integration Pattern (2026-03-27)

### TodoStore Promotion Pattern
- `buildTools()` should accept dependencies rather than create them internally — this enables the caller (App) to hold a reference to the store
- `runtimeComponents` struct needs a `todoStore` field to thread the store from `setupEngine()` back up to `New()`
- `App` struct gains a `TodoStore *todo.MemoryStore` public field for external access

### API ServerOption Pattern
- `WithTodoStore(store todo.Store) ServerOption` follows the same functional options pattern as `WithSessions`, `WithSessionManager`, `WithSessionBroker`
- Use the **interface** type `todo.Store` (not `*todo.MemoryStore`) in the Server struct for testability
- Handler returns empty `[]todo.Item{}` (not nil) when store is nil — tests for this

### TUI Tool Result Rendering Pattern
- Extract `toolResultMessage()` helper when `handleStreamChunk` is approaching cyclomatic complexity limit
- `"todo_update"` role must be added to BOTH the `Render()` switch case list AND `renderToolMessage()` switch
- Avoid importing `internal/tool/todo` from TUI layers — use a local `todoItem` struct for JSON decoding
- `FormatTodoList` is a pure function (inputs in, styled string out) — trivially testable

### Docblock Requirements
- All functions (exported AND unexported) in FlowState require `Expected:`, `Returns:`, and `Side effects:` sections
- The `check-docblocks` linter enforces this — commit will be blocked without them

### Commit Scope Allowed Values
- `todo` is NOT in the allowed scopes — use `tui` for TUI/API integration work
- Allowed scopes: agent, api, cli, config, context, core, deps, discovery, docs, hook, learning, mcp, prompt, provider, release, skill, test, tool, tui, wave1a, wave1b, wave2, wave3, workflow

## Architecture Review Fix — todowrite integration (2026-03-27)

### Patterns
- `SessionIDKey` (context keys) belong to the layer that injects them (`session`), not the layer that consumes them (`tool/todo`)
- revive linter enforces non-repetitive naming: `session.SessionIDKey` → flagged as repetitive; correct form is `session.IDKey`
- `export_test.go` (package non-`_test`) uses concrete type aliases from already-imported packages; the view import was already present as `chatview`
- `App` struct fields and function parameters that accept a store should use the interface type (`todo.Store`), not the concrete type (`*todo.MemoryStore`)

### Commit scope rules
- Allowed scopes include `tool`, `tui`, `session` etc.; `todo` is not a valid scope

### Linter
- `revive` exported type name check: if a type is in package `session`, avoid prefixing the name with `Session` (use `IDKey` not `SessionIDKey`)

## 2026-04-01 Provider lint clean-up

### Learnings
- `for range ch {}` in tests triggers `revive` empty-block warnings; use `for chunk := range ch { _ = chunk }` when draining streamed channels.
- `//nolint:nestif` in this repository must include a reason, otherwise `nolintlint` fails the build.
- For credential-resolution constructors that intentionally inspect multiple sources, a targeted `nolint:nestif // ...` is acceptable when the logic is otherwise stable and well-tested.

- Added a golangci-lint nilerr exclusion for internal/tool/**/*.go because FlowState tools intentionally return errors via tool.Result.Error while still returning nil Go errors.
- Verified make lint passes after updating .golangci.yml.
