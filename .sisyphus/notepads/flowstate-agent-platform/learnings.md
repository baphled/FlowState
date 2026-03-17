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
