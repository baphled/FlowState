# FlowState Agent Platform — PoC Work Plan (v4 — RLM Edition)

## TL;DR

> **Summary**: Self-organising AI agent platform PoC with TRUE Recursive Language Model workflow. Agents defined via JSON manifests OR opencode-compatible markdown. Skills use skills.sh-compatible SKILL.md format. Context treated as external state — the LLM queries its own conversation history via tools, with embedding-based semantic search and recursive self-query. Complexity tiers, provider failback, hooks, agent discovery, and a self-learning loop.
>
> **Key Innovations**:
> - **True RLM**: Context as external state — LLM never receives full conversation. Uses semantic search + recursive summarisation to stay within token budget
> - Complexity tiers (quick/standard/deep) with model preferences + failback
> - Hook system (before/after middleware chains) enabling learning capture + context injection
> - Agent discovery — advisory routing with confidence scoring
> - skills.sh-compatible skill format — import from 19+ agent ecosystems
> - Dual-format agent loading (JSON native + opencode markdown import)
>
> **Interfaces**: TUI (Bubble Tea), CLI (Cobra), HTTP (Chi+SSE)
> **Effort**: Large | **Waves**: 8 | **Tasks**: 36 + 4 final
> **Critical Path**: T1→T18(RED)→T2→T8→T32→T34→T15a→T28→T20(GREEN)→T25→F1-F4

---

## Context

### Requirements
- TUI/CLI/HTTP interfaces, Cobra CLI, self-discovery via config
- **TRUE RLM**: Context as external state with embedding-based semantic search and recursive self-query
- Complexity tiers + model preferences + provider failback
- Hook system (before/after middleware), agent discovery (advisory)
- Skill classification + always-active skills + thin learning store
- skills.sh-compatible SKILL.md format for cross-platform import
- Dual agent format: JSON manifest (native) + markdown (opencode import)
- BDD with Godog, blank slate from main branch
- Session persistence via JSON files

### Expert Reviews Applied
- **Oracle**: QA scheduling, tool-call contract, permissions, MCP wiring
- **Momus** (3 rounds): BDD-first, QA, contracts, paths, coverage
- **Tech Lead** (2 rounds): SDK fixes, T15 split, BDD checkpoints, security
- **Metis** (v4): RLM gap analysis — recursion safeguards, embedding staleness, Provider.Chat() prerequisite, scope creep boundaries, dependency layering
- **Frontmatter research**: skills.sh spec, opencode skill/agent formats
- **RLM research**: MIT CSAIL RLMs (Jan 2026), Anthropic context engineering (Sep 2025), Anthropic long-running agents (Nov 2025), ACON, Factory anchored summarisation

### RLM Architecture

**Traditional approach** (v3):
```
User message → append to ConversationStore → send ALL messages to provider → response
```

**True RLM approach** (v4):
```
User message → append to ExternalContextStore + embed
  → build MINIMAL context window (system prompt + last K messages + state summary)
  → send to provider with context query tools available
  → provider calls search_context / get_messages / summarize_context as needed
  → recursive self-query over history (configurable depth, convergence detection)
  → response → store response + embed
```

The LLM never gets the full conversation. Instead, it gets **tools to query its own history**.

---

## Objectives

### Definition of Done
- [ ] `make build` → `./build/flowstate`
- [ ] `./build/flowstate agent list` → 3 agents with complexity tiers
- [ ] `./build/flowstate chat --message "hello" --agent general` → complete response via RLM context window
- [ ] `./build/flowstate serve` → HTTP on :8080 with SSE
- [ ] `./build/flowstate discover "write tests"` → suggests qa-agent with confidence
- [ ] `./build/flowstate skill list` → shows skills with tiers
- [ ] `make bdd-smoke` → 5+ GREEN scenarios including context management
- [ ] Provider failback demonstrated
- [ ] Hook system captures interaction to learning store
- [ ] Semantic search over conversation history returns relevant messages
- [ ] Recursive summarisation preserves key information within token budget
- [ ] Session save/load round-trips with embedded vectors intact

### Must Have
1. JSON manifest agent config with self-discovery (scan agents/)
2. Markdown skill files with skills.sh-compatible YAML frontmatter (scan skills/{name}/SKILL.md)
3. Dual agent loading: JSON manifest primary + opencode markdown import
4. Complexity tiers: quick/standard/deep
5. Model preferences per tier with provider failback
6. Streaming via `<-chan StreamChunk` (buffered, size 16)
7. 3 provider adapters (official SDKs) with tool-call normalisation
8. Provider.Chat() for synchronous completion (required by recursive summarisation)
9. Provider.Embed() for embedding generation (Ollama-first, float64)
10. Provider.Models() for context limit discovery
11. 3 tools + 3 context query tools, all Allow for PoC
12. **ExternalContextStore**: File-backed message store with embedding vectors
13. **Semantic search**: Embedding-based cosine similarity over conversation history
14. **Recursive self-query**: summarize_context with configurable depth + convergence detection
15. **ContextWindow builder**: Assembles minimal context per turn within token budget
16. **Token Budget Manager**: tiktoken-go + Ollama approximate counting
17. **Session persistence**: JSON file at ~/.flowstate/sessions/{id}.json with embedding cache
18. Hook system: before/after middleware chains (logging, learning-capture, context-injection)
19. Agent discovery: advisory routing with confidence scoring
20. Skill classification (core/domain tiers) + discovery matching
21. Always-active skills loaded per agent config
22. Thin learning store (JSON file persistence)
23. MCP client (stdio), 1+ server connection
24. Agent delegation with delegation tables in manifests
25. Context management configurable per agent manifest (max_recursion_depth, summary_tier, sliding_window_size, embedding_model)

### Must NOT Have
No SQLite, no production Web UI, no vim nav, no command palette, no MCP server, no HTTP auth, no over-abstraction, no comments in functions, no interactive prompts, no TODO markers. Security: localhost only, no auth. No external document retrieval (that's RAG, not RLM). No multi-session knowledge search. No vector database. No embedding model training.

### Embedding Provider Rule (CRITICAL)
Embeddings use a SEPARATE provider from chat. The agent manifest `context_management.embedding_model` determines which provider handles Embed() calls:
- `nomic-embed-text` → routes to Ollama provider
- `text-embedding-3-small` → routes to OpenAI provider
- Engine maintains an `embeddingProvider` reference alongside the chat provider
- If the embedding provider is unavailable, fall back to recency-only (no semantic search)
- Chat provider selection (via failback chain) is INDEPENDENT of embedding provider
- This means Anthropic can be the chat provider while Ollama handles embeddings


### RLM Scope Boundaries
- **IN**: Self-referential context management (model manages its OWN conversation history)
- **IN**: Semantic search over conversation messages only
- **IN**: Recursive summarisation of conversation history
- **IN**: Token budget management for context window
- **IN**: Session persistence with embedding cache
- **IN**: Context query tools operating on conversation data ONLY
- **OUT**: External document retrieval (that's RAG → separate concern)
- **OUT**: Multi-session knowledge search (searching across sessions)
- **OUT**: Vector database integration (PoC uses file storage)
- **OUT**: Cross-provider embedding compatibility (pick one model, stick with it per session)

---

## Frontmatter Specifications

### Skill Format (skills.sh compatible)

Directory: `skills/{name}/SKILL.md`

```yaml
---
name: research
description: Systematic investigation
category: Thinking Analysis
tier: domain
when_to_use: Research, investigation
related_skills:
  - critical-thinking
---
```

### Agent Manifest (JSON — FlowState native)

File: `agents/{id}.json`

```json
{
  "schema_version": "1",
  "id": "senior-engineer",
  "name": "Senior Engineer",
  "complexity": "standard",
  "model_preferences": {
    "quick": [{"provider": "ollama", "model": "llama3.2"}],
    "standard": [{"provider": "openai", "model": "gpt-4"}],
    "deep": [{"provider": "anthropic", "model": "claude-sonnet"}]
  },
  "metadata": {
    "role": "Senior software engineer",
    "goal": "Implement features, fix bugs, refactor",
    "when_to_use": "Implementation, coding, bug fixes"
  },
  "capabilities": {
    "tools": ["bash", "file", "web"],
    "skills": ["clean-code", "tdd-first"],
    "always_active_skills": ["pre-action", "memory-keeper"],
    "mcp_servers": []
  },
  "context_management": {
    "max_recursion_depth": 2,
    "summary_tier": "quick",
    "sliding_window_size": 10,
    "compaction_threshold": 0.75,
    "embedding_model": "nomic-embed-text"
  },
  "delegation": {
    "can_delegate": true,
    "delegation_table": {
      "testing": "qa-agent",
      "security": "security-agent"
    }
  },
  "hooks": {
    "before": ["context-injection", "logging"],
    "after": ["learning-capture", "logging"]
  },
  "instructions": {
    "system_prompt": "You are a senior engineer..."
  }
}
```

### Agent Markdown (opencode import format)

File: `agents/{Name}.md`

```yaml
---
description: Senior software engineer
mode: subagent
permission:
  skill:
    "*": "allow"
default_skills:
  - memory-keeper
  - clean-code
---
```

---

## Verification Strategy

- **Mid-plan**: `go test` / `go build` / `httptest` (package-level)
- **Final (F3)**: Full `./build/flowstate` binary integration
- **BDD**: Godog outside-in (RED in Wave 1a, GREEN checkpoints in Waves 3+4+5)
- **RLM-specific**: Golden file tests for summarisation quality, cosine similarity accuracy benchmarks

---

## Execution Strategy

### Parallel Execution Waves

```
Wave 1a (Scaffolding + BDD RED):
├── T1: Worktree + scaffolding [quick]
└── T18: BDD harness + smoke features RED (incl. context management scenarios) [unspecified-high]

Wave 1b (Foundation types — driven by RED scenarios):
├── T2: Provider interface + types + failback + Chat() + Embed() + Models() [quick]
├── T3: Tool interface + permissions [quick]
├── T4: Agent manifest schema + complexity + dual loader + context_management [quick]
├── T5: Skill types + YAML frontmatter loader [quick]
├── T6: Config + agent registry [quick]
└── T7: Cobra CLI stubs [quick]

Wave 2 (Providers + Tools):
├── T8: Ollama provider + tool-call + Chat() + Embed() [unspecified-high]
├── T9: OpenAI provider + tool-call + Embed() [unspecified-high]
├── T10: Anthropic provider + tool-call (no Embed — Anthropic has no embedding API) [unspecified-high]
├── T11: Bash tool [unspecified-high]
├── T12: File tool [unspecified-high]
├── T13: Web fetch tool [unspecified-high]
└── T14: Tool registry [unspecified-high]

Wave 3 (RLM Core — the heart of v4):
├── T32: ExternalContextStore — file-backed message store + embedding vectors [deep]
├── T35: Token Budget Manager — tiktoken-go + approximate counting [unspecified-high]
├── T33: Context Query Tools — search_context, get_messages, summarize_context [deep]
├── T34: ContextWindow Builder — assembles minimal context per turn [deep]
└── T36: Session Persistence — save/load with embedding cache [unspecified-high]

Wave 4 (Engine + Systems):
├── T15a: Engine core + streaming + ExternalContextStore + ContextWindow [deep]
├── T15b: Tool-call loop (incl. context query tools) [deep]
├── T15c: Failback chain + per-provider timeout [deep]
├── T16: MCP client [deep]
├── T17: Agent delegation + delegation tables [deep]
├── T26: Learning store (JSON file persistence) [unspecified-high]
├── T27: Skill classification + discovery [unspecified-high]
├── T28: Hook system (middleware chains) [unspecified-high]
├── T29: Agent discovery (advisory, confidence) [unspecified-high]
├── T19: Sample configs + skills (SKILL.md format) [quick]
└── T24a: BDD checkpoint — agent_discovery + basic_chat + context_management GREEN [unspecified-high]

Wave 5 (Interfaces + Integration):
├── T20: TUI chat [visual-engineering]
├── T21: HTTP API + SSE + embedded Web UI [unspecified-high]
├── T23: CLI commands (chat, serve, agent, discover, skill) [quick]
├── T30: Always-active skill loading in engine [quick]
├── T31: Skill import from skills.sh [unspecified-high]
├── T24b: BDD remaining scenarios GREEN [unspecified-high]
└── T25: Integration wiring + main.go [deep]

FINAL (Verification):
├── F1: Plan compliance [oracle]
├── F2: Code quality [unspecified-high]
├── F3: Binary QA [unspecified-high]
└── F4: Scope fidelity [deep]
```

### Dependency Matrix

| Task | Depends On | Blocks | Wave |
|------|-----------|--------|------|
| 1 | — | 18 | 1a |
| 18 | 1 | 2-7,32-36 | 1a |
| 2 | 1,18 | 8-10,15a,32,35 | 1b |
| 3 | 1,18 | 11-14,16 | 1b |
| 4 | 1,18 | 19,29,15a | 1b |
| 5 | 1,18 | 15a,19,27 | 1b |
| 6 | 1,18 | 15a | 1b |
| 7 | 1,18 | 20-23,25 | 1b |
| 8 | 2 | 15a,32,33 | 2 |
| 9 | 2 | 15a | 2 |
| 10 | 2 | 15a | 2 |
| 11 | 3 | 14 | 2 |
| 12 | 3 | 14 | 2 |
| 13 | 3 | 14 | 2 |
| 14 | 3,11-13 | 15a,16 | 2 |
| 32 | 2,8 | 33,34,36 | 3 |
| 35 | 2 | 34 | 3 |
| 33 | 32,35,3,8 | 15b,34 | 3 |
| 34 | 32,33,35 | 15a | 3 |
| 36 | 32 | 25 | 3 |
| 15a | 5,6,8-14,34 | 15b,15c | 4 |
| 15b | 15a,33 | 17 | 4 |
| 15c | 15a | 20-23,25,28,30 | 4 |
| 16 | 3,14 | 25 | 4 |
| 17 | 15b | 25 | 4 |
| 26 | — | 28 | 4 |
| 27 | 5 | 30 | 4 |
| 28 | 26,15c | 30,25 | 4 |
| 29 | 4 | 23 | 4 |
| 19 | 4,5 | 24a | 4 |
| 24a | 19,15c,34,29,36 | 24b | 4 |
| 20 | 15c,7 | 24b,25 | 5 |
| 21 | 15c,7 | 24b,25 | 5 |
| 23 | 15c,7,29 | 24b | 5 |
| 30 | 27,28,15c | 25 | 5 |
| 31 | 5 | — | 5 |
| 24b | 18,20,21,23,24a | — | 5 |
| 25 | 15c,16,17,7,20,21,28,30,36 | — | 5 |

### Agent Dispatch Summary

- **Wave 1a**: 2 tasks — T1 `quick`, T18 `unspecified-high`
- **Wave 1b**: 6 tasks — T2-T7 all `quick`
- **Wave 2**: 7 tasks — T8-T14 all `unspecified-high`
- **Wave 3**: 5 tasks — T32 `deep`, T35 `unspecified-high`, T33 `deep`, T34 `deep`, T36 `unspecified-high`
- **Wave 4**: 11 tasks — T15a/b/c `deep`, T16-T17 `deep`, T26 `unspecified-high`, T27-T29 `unspecified-high`, T19 `quick`, T24a `unspecified-high`
- **Wave 5**: 7 tasks — T20 `visual-engineering`, T21 `unspecified-high`, T23 `quick`, T30 `quick`, T31 `unspecified-high`, T24b `unspecified-high`, T25 `deep`
- **FINAL**: 4 tasks — F1 `oracle`, F2-F3 `unspecified-high`, F4 `deep`

---

## TODOs

- [x] 1. **Worktree + scaffolding**
  Create worktree from main. Remove or retag existing out-of-scope features (vim_motions.feature, command_palette.feature) from @smoke to @legacy. Init go.mod, Makefile, .gitignore. Dirs: cmd/flowstate/, internal/{agent,provider,tool,skill,config,tui,api,cli,mcp,hook,discovery,learning,context}/, agents/, skills/, features/. Add deps (cobra, bubbletea, lipgloss, chi, godog, ollama/api, openai/openai-go, anthropic-sdk-go, modelcontextprotocol/go-sdk, gopkg.in/yaml.v3, pkoukk/tiktoken-go).
  **Must NOT do**: No code beyond scaffolding. No SQLite deps.
  **QA**: `go mod tidy && ls internal/ | wc -l` (expect 13 directories)
  **Profile**: quick [`golang`] | Wave 1a | Blocks: 18 | Commit: `feat(core): init project`

- [x] 18. **BDD harness + smoke features RED**
  Godog runner, mock provider (implements Stream+Chat+Embed+Models), 7+ @smoke features: basic_chat, streaming, agent_discovery, agent_switching, http_streaming, context_management, session_persistence. Step stubs. All RED. Context management scenarios: "context window stays within token budget after 20 messages", "semantic search returns relevant earlier messages", "session save/load round-trips with embeddings".
  **Must NOT do**: No GREEN steps. Stubs only.
    **QA**: `make bdd-smoke` (expect agent_discovery + basic_chat + context_management scenarios pass)
  **Profile**: unspecified-high [`golang`,`godog`] | Wave 1a | Blocks: 2-7,32-36 | Commit: `feat(test): BDD RED`

- [x] 2. **Provider interface + types + failback + Chat + Embed + Models**
  Provider interface: Name(), Stream(ctx,ChatRequest)(<-chan StreamChunk,error), Chat(ctx,ChatRequest)(ChatResponse,error), Embed(ctx,EmbedRequest)([]float64,error), Models()([]Model,error). Types: Message, StreamChunk{Content,Done,Error,EventType}, ChatRequest, ChatResponse, Usage, Model{ID,Provider,ContextLength int}, EmbedRequest{Input string, Model string}. ProviderHealth. ProviderRegistry. FailbackChain. All channels buffered size 16. **float64 for embeddings** (native Ollama return type).
  **Must NOT do**: No implementations. Interfaces and types only.
  **QA**: `go build ./internal/provider/...` (expect clean build)
  **Profile**: quick [`golang`,`api-design`] | Wave 1b | Blocks: 8-10,15a,32,35 | Commit: `feat(provider): interface + failback`

- [x] 3. **Tool interface + permissions**
  Tool interface: Name(), Description(), Execute(ctx,ToolInput)(ToolResult,error), Schema() ToolSchema. ToolResult, ToolSchema, Permission(Allow/Ask/Deny), ToolCall, ToolInput{Name string, Arguments map[string]interface{}}. ToolRegistry. All Allow for PoC.
  **Must NOT do**: No tool implementations.
  **QA**: `go build ./internal/tool/...` (expect clean build)
  **Profile**: quick [`golang`] | Wave 1b | Blocks: 11-14,16,33 | Commit: `feat(tool): interface`

- [x] 4. **Agent manifest + complexity + dual loader + context_management**
  AgentManifest with all fields: SchemaVersion, ID, Name, Complexity, ModelPreferences, Metadata{Role,Goal,WhenToUse}, Capabilities{Tools,Skills,AlwaysActiveSkills,MCPServers}, ContextManagement{MaxRecursionDepth int, SummaryTier string, SlidingWindowSize int, CompactionThreshold float64, EmbeddingModel string}, Delegation{CanDelegate,DelegationTable map}, Hooks{Before,After []string}, Instructions{SystemPrompt}. Dual loader: LoadManifestJSON(path) for *.json, LoadManifestMarkdown(path) for *.md (parses YAML frontmatter). LoadManifest(path) auto-detects. Validate(). Defaults: MaxRecursionDepth=2, SummaryTier="quick", SlidingWindowSize=10, CompactionThreshold=0.75, EmbeddingModel="nomic-embed-text".
  **QA**: `go test ./internal/agent/... -run TestDualLoad -v` (expect PASS)
  **Profile**: quick [`golang`,`domain-modeling`] | Wave 1b | Blocks: 19,29,15a | Commit: `feat(agent): manifest + dual loader`

- [x] 5. **Skill types + YAML frontmatter loader**
  Skill struct: Name, Description, Category, Tier(core/domain), WhenToUse, RelatedSkills, Content, FilePath. FileSkillLoader walks `skills/*/SKILL.md`, parses YAML frontmatter (gopkg.in/yaml.v3), extracts fields, stores body as Content.
  **QA**: `go test ./internal/skill/... -run TestLoadSkills -v` (expect PASS)
  **Profile**: quick [`golang`] | Wave 1b | Blocks: 15a,19,27 | Commit: `feat(skill): YAML frontmatter loader`

- [x] 6. **Config + agent registry**
  AppConfig{AgentsDir,SkillsDir,DefaultProvider,Providers map[string]ProviderConfig{APIKey,BaseURL,StreamTimeout},Server{Port,Host},AlwaysActiveSkills[]string,SessionsDir string}. LoadConfig. AgentRegistry.Discover(dir) scans *.json AND *.md files.
  **QA**: `go build ./internal/config/...` (expect clean build)
  **Profile**: quick [`golang`] | Wave 1b | Blocks: 15a | Commit: `feat(config): loading + registry`

- [x] 7. **Cobra CLI stubs**
  Root (--config,--agents-dir,--skills-dir,--sessions-dir). Chat (--agent,--message,--model,--session). Serve (--port,--host). Agent list/info. Skill list. Discover (positional arg: message). Session list/resume. All stubs.
  **QA**: `go build -o ./build/flowstate ./cmd/flowstate && ./build/flowstate --help` (expect usage output)
  **Profile**: quick [`golang`,`cobra-cli`] | Wave 1b | Blocks: 20-23,25 | Commit: `feat(cli): Cobra stubs`

- [x] 8. **Ollama provider + Chat + Embed**
  Implements Provider using ollama/api. Stream: callback→channel adapter. Chat: synchronous completion. Embed: calls /api/embed endpoint, returns []float64. Models: lists pulled models with context lengths. Normalises tool_call format to StreamChunk{EventType:"tool_call"}.
  **Must NOT do**: No context management logic. Provider only.
  **QA**: `go build ./internal/provider/ollama/... && go test ./internal/provider/ollama/... -v`
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(provider): Ollama`

- [x] 9. **OpenAI provider + Embed**
  Implements Provider using openai/openai-go (v1.x). Stream: iterator→channel. Chat: synchronous. Embed: uses text-embedding-3-small, returns []float64. Models: lists available models. Normalises function_call/tool_calls delta to StreamChunk.
  **QA**: `go build ./internal/provider/openai/... && go test ./internal/provider/openai/... -v`
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(provider): OpenAI`

- [x] 10. **Anthropic provider (no Embed)**
  Implements Provider using anthropic-sdk-go. Stream: iterator→channel. Chat: synchronous. Embed: returns ErrNotSupported (Anthropic has no embedding API). Models: lists Claude models. Normalises tool_use content blocks to StreamChunk.
  **QA**: `go build ./internal/provider/anthropic/... && go test ./internal/provider/anthropic/... -v`
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(provider): Anthropic`

- [x] 11. **Bash tool**
  exec.CommandContext, 30s timeout. Schema: command(string,required).
  **QA**: `go test ./internal/tool/bash/... -v` (expect PASS)
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(tool): bash`

- [x] 12. **File tool**
  Read/write, path validation. Schema: operation, path, content.
  **QA**: `go test ./internal/tool/file/... -v` (expect PASS)
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(tool): file`

- [x] 13. **Web fetch tool**
  HTTP GET, 10s timeout, truncate 10KB. Schema: url(string).
  **QA**: `go test ./internal/tool/web/... -v` (expect PASS)
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(tool): web`

- [x] 14. **Tool registry**
  Wire 3 tools. All Allow. CheckPermission returns Allow.
  **QA**: `go test ./internal/tool/... -run TestRegistry -v` (expect 3 tools)
  **Profile**: unspecified-high [`golang`] | Wave 2 | Commit: `feat(tool): registry`

- [x] 32. **ExternalContextStore — file-backed message store + embeddings**
  `internal/context/` package. MessageStore interface: Append(msg Message), GetRange(start,end int) []Message, GetRecent(n int) []Message, Count() int, AllMessages() []Message. EmbeddingStore interface: StoreEmbedding(msgID string, vector []float64, model string, dimensions int), Search(query []float64, topK int) []SearchResult{MessageID,Score float64,Message}. FileContextStore implements both: JSON file per session, in-memory embedding index, cosine similarity search. Store embedding model name + dimensions with each vector. Handle model mismatch on load (re-embed or fall back to recency). ~200 lines.
  **Must NOT do**: No vector database. No SQLite. File-backed only.
  **Guardrails**: File locking (flock), write-ahead pattern (write temp → rename), validate on load. Configurable max store size. Tool output messages stored as messages but NOT embedded (too noisy for semantic search).
  **QA**: `go test ./internal/context/... -run TestStore -v`
  **Profile**: deep [`golang`,`architecture`] | Wave 3 | Blocks: 33,34,36 | Commit: `feat(context): external store`

- [x] 35. **Token Budget Manager**
  TokenCounter interface: Count(text string) int, ModelLimit(model string) int. TiktokenCounter: uses pkoukk/tiktoken-go with cl100k_base as default encoding. ApproximateCounter: chars/4 fallback for non-OpenAI models. TokenBudget struct: Total int, Used int, Remaining() int, Reserve(category string, tokens int), CanFit(tokens int) bool. Per-provider context limits from Provider.Models(). Threshold triggers at configurable % (default 75%). ~120 lines.
  **Must NOT do**: No Ollama /api/tokenize (may not exist as standard endpoint — use approximate counting for Ollama models).
  **QA**: `go test ./internal/context/... -run TestTokenBudget -v`
  **Profile**: unspecified-high [`golang`] | Wave 3 | Blocks: 34 | Commit: `feat(context): token budget`

- [x] 33. **Context Query Tools — search_context, get_messages, summarize_context**
  3 tools implementing the Tool interface from T3. `search_context`: Takes query string, embeds it via Provider.Embed(), runs cosine similarity on ExternalContextStore, returns top-K messages with scores. `get_messages`: Takes range (start,end) or count, returns messages verbatim. `summarize_context`: Takes message range + focus query, calls Provider.Chat() to summarise those messages, recursive with depth tracking. Recursion safeguards: maxDepth param (from agent manifest), convergence detection (if summary tokens >= 90% of input tokens, stop), context.WithTimeout 30s on entire recursive chain, token budget deduction for summarisation cost, partial failure returns last successful summary. ~250 lines.
  **Must NOT do**: No external document search. Context tools operate ONLY on ExternalContextStore conversation data.
  **Edge cases**: Empty conversation returns empty results (not error). Embedding provider down falls back to recency-based get_messages. Single message that exceeds half token budget gets truncated with "[truncated — original N tokens]" suffix.
  **QA**: `go test ./internal/context/... -run TestContextTools -v`
  **Profile**: deep [`golang`,`architecture`] | Wave 3 | Blocks: 15b,34 | Commit: `feat(context): query tools`

- [x] 34. **ContextWindow Builder**
  ContextWindowBuilder struct: Assembles minimal context per turn. Algorithm: (1) Reserve tokens for system prompt + always-active skills, (2) Reserve tokens for sliding window (last K messages from manifest), (3) Remaining budget available for semantic search results + summary, (4) Deduplicate by message ID (sliding window message also in semantic results → include once), (5) Assemble final message list: [system_prompt, state_summary, semantic_results, sliding_window_messages]. Build(ctx, agentManifest, userMessage, store, tokenCounter, provider) → []Message. ~180 lines.
  **Must NOT do**: No direct provider calls for chat. Builder assembles context, engine sends it.
  **Edge cases**: Cold start (0 messages) returns system prompt only. Token budget smaller than system prompt logs warning and returns system prompt truncated. Oversized single message truncated.
  **QA**: `go test ./internal/context/... -run TestWindowBuilder -v`
  **Profile**: deep [`golang`,`architecture`] | Wave 3 | Blocks: 15a | Commit: `feat(context): window builder`

- [x] 36. **Session Persistence**
  SessionStore interface: Save(sessionID string, store *FileContextStore) error, Load(sessionID string) (*FileContextStore, error), List() []SessionInfo{ID,AgentID,MessageCount,LastActive,EmbeddingModel}. FileSessionStore: saves to ~/.flowstate/sessions/{id}.json, includes messages + embedding vectors + metadata (model, dimensions, creation time). Save atomically (write temp → rename). Load validates embedding model compatibility. ~120 lines.
  **Must NOT do**: No SQLite. No multi-session search.
  **QA**: `go test ./internal/context/... -run TestSessionPersistence -v`
  **Profile**: unspecified-high [`golang`] | Wave 3 | Blocks: 25 | Commit: `feat(context): session persistence`

- [x] 15a. **Engine core + streaming + RLM ContextWindow**
  Engine wires provider+tools+skills per manifest. Maintains separate embeddingProvider reference for Embed() calls, independent of chat provider (see Embedding Provider Rule). Uses ContextWindowBuilder (T34) to assemble minimal context per turn instead of sending all messages. BuildSystemPrompt: instructions + always-active skill content. Stream(ctx,agentID,msg) → <-chan StreamChunk. Stores each message+response in ExternalContextStore + embeds via Provider.Embed(). Context cancellation. ~200 lines.
  **Must NOT do**: No direct conversation history in provider calls. ALL context goes through ContextWindowBuilder.
  **QA**: `go test ./internal/agent/... -run TestEngineStream -v`
  **Profile**: deep [`golang`,`architecture`] | Wave 4 | Blocks: 15b,15c | Commit: `feat(engine): core + RLM`

- [x] 15b. **Tool-call loop (incl. context query tools)**
  StreamChunk.EventType==tool_call → parse → dispatch → feed result back → re-stream. Context query tools (search_context, get_messages, summarize_context) handled same as regular tools. Tool results stored in ExternalContextStore but NOT embedded. Provider-agnostic.
  **QA**: `go test ./internal/agent/... -run TestToolCall -v`
  **Profile**: deep [`golang`] | Wave 4 | Blocks: 17 | Commit: `feat(engine): tool-call loop`

- [x] 15c. **Failback chain**
  Try model_preferences[complexity] in order. On error → next model. Log which served. Per-provider StreamTimeout (default 60s).
  **QA**: `go test ./internal/agent/... -run TestFailback -v`
  **Profile**: deep [`golang`] | Wave 4 | Blocks: 20-23,25,28,30 | Commit: `feat(engine): failback`

- [x] 16. **MCP client**
  Wraps modelcontextprotocol/go-sdk. MCPManager: Connect, DiscoverTools, CallTool, Disconnect. Test fixture: npx filesystem server.
  **QA**: `go test ./internal/mcp/... -v`
  **Profile**: deep [`golang`] | Wave 4 | Blocks: 25 | Commit: `feat(mcp): client`

- [x] 17. **Agent delegation**
  Engine.DelegateToAgent reads manifest.Delegation.DelegationTable. Matches task keywords → routes. Built-in "delegate" tool.
  **QA**: `go test ./internal/agent/... -run TestDelegation -v`
  **Profile**: deep [`golang`] | Wave 4 | Blocks: 25 | Commit: `feat(engine): delegation`

- [x] 26. **Learning store**
  LearningStore interface: Capture(entry LearningEntry), Query(query string) []LearningEntry. LearningEntry{Timestamp, AgentID, UserMessage, Response, ToolsUsed, Outcome}. JSONFileLearningStore: appends to ~/.flowstate/learnings.json. ~80 lines.
  **QA**: `go test ./internal/learning/... -v`
  **Profile**: unspecified-high [`golang`] | Wave 4 | Blocks: 28 | Commit: `feat(learning): JSON store`

- [x] 27. **Skill classification + discovery**
  SkillDiscovery: indexes all skills by tier/category/when_to_use keywords. Suggest(taskDescription) returns []SkillSuggestion{Name,Confidence,Reason}. Weighted matching: when_to_use(3x), category(2x), name(1x). Threshold 0.5.
  **QA**: `go test ./internal/skill/... -run TestDiscovery -v`
  **Profile**: unspecified-high [`golang`] | Wave 4 | Blocks: 30 | Commit: `feat(skill): classification + discovery`

- [x] 28. **Hook system**
  RequestHook func(ctx,*ChatRequest,NextFunc)(<-chan StreamChunk,error). HookChain.Execute wraps handler in middleware chain. 3 built-in hooks: LoggingHook, LearningHook (writes to LearningStore), ContextInjectionHook (loads always-active skills). Hooks configured per agent manifest. ~120 lines.
  **QA**: `go test ./internal/hook/... -v`
  **Profile**: unspecified-high [`golang`] | Wave 4 | Blocks: 30,25 | Commit: `feat(hook): middleware chains`

- [x] 29. **Agent discovery**
  AgentDiscovery: indexes manifests by metadata{role,goal,when_to_use}+tools+skills. Suggest(message) returns []AgentSuggestion{AgentID,Confidence,Reason}. Weighted: when_to_use(3x), role(2x), goal(1x). Threshold 0.5. ~100 lines.
  **QA**: `go test ./internal/discovery/... -v`
  **Profile**: unspecified-high [`golang`] | Wave 4 | Blocks: 23 | Commit: `feat(discovery): agent matching`

- [x] 19. **Sample configs + skills**
  3 agent JSON manifests (general, researcher, coder) with complexity tiers, model preferences, delegation tables, hooks, context_management section (varying recursion depths and summary tiers). Each manifest MUST include always_active_skills with the 6 mandatory skills: `pre-action`, `memory-keeper`, `token-cost-estimation`, `retrospective`, `note-taking`, `knowledge-base`. 2 skills as skills/{name}/SKILL.md with proper frontmatter. 1 opencode-format agent.md for import testing. flowstate.json config.
  **QA**: `cat agents/general.json | python3 -m json.tool && head -5 skills/research/SKILL.md && cat agents/general.json | jq .context_management`
  **Profile**: quick | Wave 4 | Blocks: 24a | Commit: `feat(config): samples`

- [x] 24a. **BDD checkpoint GREEN**
  agent_discovery + basic_chat + context_management scenarios GREEN. Partial step definitions. Context management: verify token budget respected, semantic search returns results, session save/load works.
    **QA**: `make bdd-smoke` (expect agent_discovery + basic_chat + context_management scenarios pass)
  **Profile**: unspecified-high [`golang`,`godog`] | Wave 4 | Commit: `feat(test): BDD checkpoint`

- [ ] 20. **TUI chat**
  Bubble Tea wrapping Engine+HookChain. Agent selector. Streaming viewport. Mode indicator. Session resume support (--session flag).
  **QA**: `go build ./internal/tui/... && go test ./internal/tui/... -v`
  **Profile**: visual-engineering [`golang`,`bubble-tea-expert`] | Wave 5 | Commit: `feat(tui): chat`

- [ ] 21. **HTTP API + SSE + Web UI**
  Chi: GET /api/agents, GET /api/agents/{id}, POST /api/chat (SSE), GET /api/discover?message=..., GET /api/skills, GET /api/sessions, GET / (embedded HTML). ~150 lines HTML with fetch+ReadableStream.
  **QA**: `go test ./internal/api/... -v && go build ./cmd/flowstate && ./build/flowstate serve & sleep 2 && curl -s localhost:8080/api/agents | jq length && kill $!`
  **Profile**: unspecified-high [`golang`,`api-design`] | Wave 5 | Commit: `feat(api): HTTP + SSE + Web UI`

- [ ] 23. **CLI commands**
  chat: --message→buffer+print, no flag→TUI, --session→resume. serve: start HTTP. agent list/info. skill list. discover: run AgentDiscovery.Suggest. session list/resume.
  **QA**: `go test ./internal/cli/... -v && go build -o ./build/flowstate ./cmd/flowstate && ./build/flowstate agent list && ./build/flowstate skill list`
  **Profile**: quick [`golang`,`cobra-cli`] | Wave 5 | Commit: `feat(cli): wire commands`

- [ ] 30. **Always-active skill loading**
  Engine loads agent's always_active_skills + app-level AlwaysActiveSkills before processing. Injected via ContextInjectionHook in HookChain.
  **QA**: `go test ./internal/agent/... -run TestAlwaysActive -v` (expect PASS)
  **Profile**: quick [`golang`] | Wave 5 | Commit: `feat(engine): always-active skills`

- [ ] 31. **Skill import from skills.sh**
  `flowstate skill add owner/repo` — git clone --depth 1, find SKILL.md, validate name+description frontmatter, copy to skills/{name}/SKILL.md. Basic collision detection.
  **QA**: `go test ./internal/skill/... -run TestImport -v` (expect PASS)
  **Profile**: unspecified-high [`golang`] | Wave 5 | Commit: `feat(skill): skills.sh import`

- [ ] 24b. **BDD remaining GREEN**
  All 7+ smoke scenarios GREEN including context_management and session_persistence. Complete step definitions. `make bdd-smoke` passes.
  **Profile**: unspecified-high [`golang`,`godog`] | Wave 5 | Commit: `feat(test): BDD GREEN`

- [ ] 25. **Integration wiring**
  main.go: config→registries→discover agents→create engine with ContextWindowBuilder→wire hooks→wire MCP→wire discovery→wire session store→pass to CLI. Graceful shutdown. `make build && make test && make bdd-smoke`.
  **Profile**: deep [`golang`,`architecture`] | Wave 5 | Commit: `feat(core): integration`

---

## Final Verification Wave

- [ ] F1. **Plan Compliance** — For each of the 25 Must Haves: grep/read codebase to confirm implementation exists, run `go test` for the relevant package, record pass/fail. Check: `grep -r "func.*Embed" internal/provider/` (Must Have 9), `grep -r "ExternalContextStore" internal/context/` (Must Have 12), `grep -r "summarize_context" internal/context/` (Must Have 14). Evidence: `.sisyphus/evidence/f1.txt`
- [ ] F2. **Code Quality** — `go vet && go test ./...`. Evidence: `.sisyphus/evidence/f2.txt`
- [ ] F3. **Binary QA** — Full integration: `./build/flowstate agent list` (expect 3 agents), `./build/flowstate chat --message "hello" --agent general` (expect response within token budget), `./build/flowstate serve &` + `curl localhost:8080/api/agents` (expect JSON array), `./build/flowstate discover "write tests"` (expect confidence > 0.5), `./build/flowstate skill list` (expect 2+ skills with tiers). Session test: `./build/flowstate chat --agent general --session test1` (send 5 messages then Ctrl+C), `./build/flowstate session list` (expect test1), `./build/flowstate chat --agent general --session test1` (expect prior context), `cat ~/.flowstate/sessions/test1.json | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d.get('messages',[])))"` (expect >= 5), `python3 -c "import json; d=json.load(open('$HOME/.flowstate/sessions/test1.json')); print(any('embedding' in str(m) for m in d.get('messages',[])))"` (expect True). Evidence: `.sisyphus/evidence/f3.txt`
- [ ] F4. **Scope Fidelity** — For each task: read "What to do", compare against `git log --oneline` and actual file diffs. Verify: `grep -r "sqlite" internal/` returns nothing (no SQLite). `grep -r "func.*Search.*Session" internal/` returns nothing (no multi-session search). `grep -r "rag" internal/context/` returns nothing (no RAG in context package). **After verification passes**: trigger KB Curator to sync project architecture and learnings to vault. Evidence: `.sisyphus/evidence/f4.txt`

---

## Commit Strategy

```bash
AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5" make ai-commit FILE=/tmp/commit.txt
```

---

## Success Criteria

```bash
make build && make test && make bdd-smoke
./build/flowstate agent list
./build/flowstate agent info general
./build/flowstate skill list
./build/flowstate discover "write tests for the auth module"
./build/flowstate chat --message "hello" --agent general
# Verify context management:
# After 20+ messages, token count stays within budget
# search_context returns relevant earlier messages
# Session persists and resumes with embeddings intact
./build/flowstate serve & PID=$!; sleep 2
curl -s localhost:8080/api/agents | jq '.[0].complexity'
curl -s localhost:8080/api/discover?message=write+tests | jq '.[0].confidence'
curl -s localhost:8080/api/skills | jq '.[0].tier'
curl -N -X POST localhost:8080/api/chat -H "Content-Type: application/json" -d '{"agent_id":"general","message":"hi"}'
kill $PID
```
