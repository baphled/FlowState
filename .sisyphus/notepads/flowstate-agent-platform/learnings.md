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
