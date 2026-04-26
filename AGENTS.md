# FlowState AI Agent Instructions

> **Reference document** for AI coding agents working on FlowState — a Go TUI application built with the Bubble Tea framework. Read this before writing any code, making any commits, or asking for clarification. All rules here are non-negotiable unless an explicit exception is stated.

## Git Worktree Setup

FlowState uses git worktrees. The structure is:

```
~/Projects/
└── FlowState.git/           # Bare repository
    ├── main/                # Primary development worktree
    ├── feature-xxx/         # Feature worktrees (temporary)
    └── hooks/               # Git hooks (shared)
```

**Always work in a worktree, never in the bare repo directly.**

### Creating Feature Branches

```bash
cd /home/baphled/Projects/FlowState.git
git worktree add -b feature/my-feature feature-my-feature main
cd feature-my-feature
```

### Cleaning Up

```bash
cd /home/baphled/Projects/FlowState.git/main
git worktree remove ../feature-my-feature
git branch -d feature/my-feature
```

## Getting Started

### Essential Commands

| Task | Command |
|------|---------|
| **Run all tests** | `make test` |
| **Run BDD tests** | `make bdd` |
| **Run smoke tests** | `make bdd-smoke` |
| **Build** | `make build` |
| **Format** | `make fmt` |
| **Lint** | `make lint` |
| **Full check** | `make check` |
| **Install hooks** | `make install-hooks` |

## Keybindings

The chat intent's keyboard shortcuts are listed below. Use `Ctrl+T` to toggle the swarm activity pane that renders delegation, tool-call, plan, and review events in real time. The activity pane is visible by default; toggling hides it and falls back to a single-pane layout.

### Chat intent

| Key | Action |
|-----|--------|
| `Enter` | Send the current message |
| `Alt+Enter` | Insert a new line in the input buffer |
| `Tab` | Cycle the active agent |
| `Esc` | Dismiss modals, pickers, or the session viewer; otherwise no-op |
| `Ctrl+C` | Cancel the active stream, save the session, and quit |
| `Ctrl+D` | Open the delegation picker |
| `Ctrl+A` | Open the agent picker |
| `Ctrl+P` | Open the model selector |
| `Ctrl+S` | Open the session browser |
| `Ctrl+G` | Open the session tree overlay |
| `Ctrl+E` | Open event details modal (most recent swarm event) |
| `Ctrl+T` | Toggle the swarm activity pane (visible by default) |
| `↑` / `↓` | Scroll the message viewport line by line |
| `PgUp` / `PgDn` | Scroll the message viewport a page at a time |
| `Home` / `End` | Jump to the top or bottom of the message viewport |

### Session tree modal (`Ctrl+G`)

| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate session tree |
| `Enter` | Select session |
| `Esc` | Close modal |

### Event details modal (`Ctrl+E`)

| Key | Action |
|-----|--------|
| `↑` / `↓` / `j` / `k` | Scroll event details |
| `Esc` | Close modal |

### Notes

- **Narrow terminals.** On terminals narrower than 80 columns the activity pane is suppressed; the `Ctrl+T` keybinding remains bound but has no visible effect.
- **`Ctrl+S` and XOFF.** Some terminals intercept `Ctrl+S` as XOFF flow-control, which can present as an apparent freeze. If you hit this, run `stty -ixon` before launching FlowState.

## Commit Rules (MANDATORY — NO EXCEPTIONS)

**CRITICAL: ALL commits MUST use `make ai-commit`. NEVER use `git commit` directly.**

```bash
# CORRECT — always use this:
printf 'feat(scope): description\n' > /tmp/commit.txt
AI_AGENT="Opencode" AI_MODEL="claude-opus-4.5" make ai-commit FILE=/tmp/commit.txt

# FORBIDDEN — never do this:
git commit -m "..."          # ❌ NO
git commit --amend           # ❌ NO (unless orchestrator explicitly authorises)
git commit --no-verify       # ❌ NO
```

**Required trailer format** (enforced by `scripts/ai-commit.sh`):
```
AI-Generated-By: Opencode (claude-opus-4.5)
Reviewed-By: Yomi Colledge <baphled@boodah.net>
```

**Why this is non-negotiable:**
- Ensures proper AI attribution on every commit
- Maintains audit trail of AI-assisted code
- Violations will be caught by `make check-ai-attribution`

**Pre-commit hook setup (run once after checkout or worktree creation):**
```bash
make install-hooks
```
This configures the version-controlled pre-commit hook in `.git-hooks/` which enforces `make check` before every commit. Commits will be blocked if checks fail.

## Development Workflow

### BDD-Driven Development (MANDATORY)

**ALWAYS develop from the outside-in:**

1. **Write Cucumber scenario FIRST** - Start with acceptance test
2. **Watch it fail** - Verify it fails for the right reason
3. **Smallest change** - Implement just enough to pass ONE step
4. **Run scenario** - See next failure
5. **Repeat** - Until scenario passes
6. **Refactor** - Clean up while green

```gherkin
# Example: features/chat/basic_chat.feature
Feature: Basic Chat
  Scenario: Send message and receive response
    Given FlowState is running
    When I type "Hello"
    And I press Enter
    Then I should see a response from the AI
```

### The Smallest Change

| Situation | Smallest Change | NOT Smallest Change |
|-----------|-----------------|---------------------|
| Need a function | Create empty function returning nil | Implement full logic |
| Need a struct | Create struct with needed field | Add all possible fields |
| Need validation | Add one validation rule | Add all validations |
| Need UI element | Add minimal element | Full styled component |

### Commit Cadence

```
feat(chat): add scenario for basic chat [RED]
feat(chat): create app struct [GREEN step 1]
feat(chat): add input handling [GREEN step 2]
feat(chat): implement provider call [GREEN step 3]
feat(chat): display response [GREEN all steps]
refactor(chat): extract message formatting [REFACTOR]
```

## Architecture

### Layer Hierarchy (MUST follow)

```
App -> Intents -> UIKit + Behaviors
```

### Dependency Rules

- `uikit/` **NEVER** imports `intents/` or `behaviours/`
- `behaviours/` **NEVER** imports `intents/`
- Intents communicate results via `IntentResult[T]` — never direct state mutation
- **NO `screens/` package** — screens are legacy; intents use UIKit directly

### Intent Pattern

```go
type Intent interface {
    Init() tea.Cmd
    Update(msg tea.Msg) tea.Cmd  // Returns Cmd only, NOT (Model, Cmd)
    View() string
    Result() *IntentResult
}
```

### BaseIntent Pattern

All intents embed `*BaseIntent` for common functionality:

```go
type MyIntent struct {
    *intents.BaseIntent
    // ... intent-specific fields
}

func NewMyIntent() *MyIntent {
    return &MyIntent{
        BaseIntent: intents.NewBaseIntent(),
    }
}

func (i *MyIntent) View() string {
    view := i.CreateViewWithBreadcrumbs("Main", "My Intent")
    view.WithContent(i.renderContent())
    view.WithHelp(intents.ThemedNavigationFooter(i.Theme()))
    return view.Render()
}
```

### ContentProvider Pattern

```go
type ContentProvider interface {
    Content(width, height int) string
    Title() string
    Footer() string
}

// Usage
asScreen := render.AsScreen(myComponent, layout)
asModal := render.AsModal(myComponent, background, w, h, theme)
```

### Multi-Agent Chat UX

The chat intent uses a dual-pane layout with a 70/30 horizontal split
(`ScreenLayout.WithSecondaryContent()` in `internal/tui/uikit/layout/`). The
primary pane renders the conversation; the secondary pane shows a live swarm
activity timeline of delegation, tool-call, plan, and review events.

Key components:

| Component | Location |
|-----------|----------|
| **SwarmEvent model** | `internal/streaming/swarm_event.go` |
| **MemorySwarmStore** | `internal/streaming/event_store_memory.go` |
| **JSONL persistence** | `internal/streaming/swarm_event_persistence.go` |
| **Session tree modal** | `internal/tui/intents/sessiontree/` |
| **Event details modal** | `internal/tui/intents/eventdetails/` |
| **Dual-pane layout** | `internal/tui/uikit/layout/screen_layout.go` |

Events are persisted in JSONL format (one JSON object per line, RFC3339
timestamps, `omitempty` on metadata). See `docs/design/swarm_event_model.md`
for the full schema and persistence contract.

## Code Style and Documentation

### Imports

```go
import (
    // 1. Standard library
    "context"
    "fmt"
    
    // 2. External (alphabetical)
    tea "github.com/charmbracelet/bubbletea"
    
    // 3. Internal (alphabetical)
    "github.com/baphled/flowstate/internal/tui/intents"
)
```

### Naming

- **Files:** `snake_case.go`
- **Packages:** `lowercase` (single word)
- **Types:** `PascalCase`
- **Private:** `camelCase`

### Comments

Comments inside function bodies are **forbidden** — express intent through naming and structure instead.

- **NO** comments inside function bodies
- **NO** inline comments
- **NO** markers: `TODO`, `FIXME`, `HACK`

**Exception:** E2E tests may have inline comments.

### Package Documentation

Every package **MUST** have a `doc.go` file providing a high-level overview.

- Start with: `// Package <name> provides <one-line summary>.`
- Follow with a blank comment line and a bulleted list of responsibilities.
- End with `package <name>` (no imports).
- Use British English throughout.

**Example:**
```go
// Package agent provides agent manifest loading, validation, and registry management.
//
// This package handles the core agent abstraction for FlowState, including:
//   - Loading agent manifests from JSON or Markdown frontmatter files
//   - Validating manifest structure and required fields
//   - Maintaining a registry of available agents for discovery
package agent
```

### Exported Identifiers

Every exported type, function, method, variable, and constant **MUST** have a godoc comment.

- Start with the identifier name: `// TypeName does...` or `// FunctionName returns...`
- Use present tense, third-person singular (e.g. "provides", "returns", "manages").
- Keep it to one sentence for simple cases; use multiple lines for complex ones.
- Ensure all prose uses British English (e.g. "behaviour", "organise").

**Example:**
```go
// Manifest defines the complete configuration for a FlowState agent.
type Manifest struct { ... }

// Capabilities defines the tools and skills available to an agent.
type Capabilities struct { ... }
```

### Interface Documentation

Document what the interface represents and when to use it, rather than how to implement it. Each method on the interface must have its own godoc comment explaining its purpose.

### Constants and Variables

Group related constants with a single leading comment for the block. Individual constants or variables only require a comment if their name is not self-explanatory.

### What NOT to Document

- **Private identifiers:** Unless the logic is non-obvious.
- **Obvious accessors:** Do not document simple getter/setter functions.
- **Test helpers:** Internal test utility functions do not require godoc.
- **Verbatim repetition:** Do not simply repeat the type name or field names — add meaningful context.
- **The obvious:** If the code already makes the purpose clear, do not add redundant comments.

## Testing

### Test File Organisation (MANDATORY)

**One test file per source file in a package.** A source file `foo.go`
maps to AT MOST two test files in the same directory:

- `foo_test.go` — external tests (`package foo_test`). The default home
  for everything that exercises only the public API.
- `foo_internal_test.go` — internal tests (`package foo`). Use ONLY when
  the test must reach into unexported helpers and adding an export shim
  would force a wider surface than the test needs.

Per-aspect splits (`foo_wiring_test.go`, `foo_thresholds_test.go`,
`foo_smoke_test.go`) are **not allowed** as a way to grow tests.
Group every spec for `foo.go` into the canonical pair above using
multiple `Describe(...)` blocks at file scope. Each `Describe` block
covers one logical surface; sibling `Describe`s within the same file
cover related surfaces of the same source file.

**Exceptions** that justify a *third* file:

- **Package-level integration tests** that span >1 source file in the
  package: name them `<topic>_integration_test.go` and label them
  `Label("integration")` so `make bdd` filters them.
- **Cross-package smoke tests** that exercise wiring through the
  binary: live under `tools/smoke/<topic>/` as runnable `main`
  packages, not `_test.go` files.
- **BDD acceptance scenarios**: live under `features/` as Gherkin +
  step files, not in the package's `_test.go`.

If you find yourself reaching for a fifth filename for the same source
file, that's a smell — the source file is doing too much. Split the
production code first; the per-source convention falls out for free.

### BDD Tags

```gherkin
@smoke       # Critical path, always run
@wip         # Work in progress
@navigation  # Navigation tests
@chat        # Chat functionality
```

### Running Tests

```bash
make bdd           # All BDD tests
make bdd-smoke     # Smoke tests only
make bdd-wip       # WIP tests only
make test          # Go tests
```

## When to REFUSE

**Immediately refuse if asked to:**

- Skip writing scenario/test first (BDD violation)
- Build components bottom-up without acceptance test
- Import `intents/` from `screens/`
- Put comments inside function bodies
- Work directly in the bare repo instead of a worktree

## Key Files

| File | Purpose |
|------|---------|
| `AGENTS.md` | This file — AI agent instructions |
| `Makefile` | Build and development commands |
| `rules/*.md` | Development rules |
| `tasks/*.md` | Current tasks |
| `features/*.feature` | BDD scenarios |
| `.sisyphus/plans/` | Active and historical delivery plans |

## Qdrant Vector Store (Optional)

FlowState uses Qdrant for vector-backed recall and learning pipelines. These features are disabled by default and require external dependencies.

### Setup

1. **Start Qdrant**:
   ```bash
   docker run -p 6333:6333 qdrant/qdrant:v1.12.0
   ```

2. **Pull Embedding Model**:
   ```bash
   ollama pull nomic-embed-text
   ```

### Configuration

Add the following to your `config.yaml`:

```yaml
qdrant:
  url: "http://localhost:6333"
  collection: "flowstate-recall"
  api_key: ""
```

**Note**: If Qdrant is not configured, FlowState starts normally but logs a warning that recall and vector learning are disabled.

### External Integration Tests

Standard tests (`make test`) do not require Qdrant. To run external integration tests, ensure Qdrant is running and the `QDRANT_URL` environment variable is set:

```bash
QDRANT_URL=http://localhost:6333 make test-external
```

## Provider Requirements for Planner/Executor

### Anthropic is Required

The planner and executor agents require a provider with reliable tool calling support. **Anthropic**, **GitHub Copilot** (Claude models), **Z.AI**, and **OpenZen** all support tool calling correctly. Llama3.2 (Ollama) does not reliably follow `skill_load` tool call instructions — it outputs code-like text instead of making actual tool calls.

### Authentication

FlowState reads provider credentials from `~/.config/flowstate/config.yaml` and from environment variables (`ANTHROPIC_API_KEY`, `GITHUB_TOKEN`, `ZAI_API_KEY`, `OPENZEN_API_KEY`, `OPENAI_API_KEY`). The Anthropic provider also supports OAuth tokens with automatic refresh; refresh tokens live in `~/.local/share/flowstate/anthropic/oauth.json` (managed by `flowstate auth anthropic`).

> **Note:** Earlier builds read credentials from `~/.local/share/opencode/auth.json`. That bridge has been removed (April 2026). If FlowState detects an OpenCode auth.json on disk while no FlowState provider is authenticated, it logs a one-time WARN at startup pointing the operator at `flowstate auth <provider>` or `~/.config/flowstate/config.yaml`.

To verify authentication, run `flowstate auth status` or inspect your `~/.config/flowstate/config.yaml`.

### Skill Directory Configuration

Set `skill_dir` in `~/.config/flowstate/config.yaml` to point to your skills directory:

```yaml
skill_dir: "/home/<user>/.config/opencode/skills"
```

Without this, FlowState defaults to `~/.local/share/flowstate/skills/` which only contains `test-skill`. The always-active skills (`pre-action`, `memory-keeper`, `token-cost-estimation`, `retrospective`, `note-taking`, `knowledge-base`) live in the OpenCode skills directory.

### How Skill Loading Works

The `SkillAutoLoaderHook` (at `internal/hook/skill_autoloader.go`) prepends a lean header to every system message:

```
Your load_skills: [pre-action, memory-keeper, ...]. Use skill_load(name) only when relevant to the current task.
```

Claude then calls the `skill_load` tool to fetch each skill's SKILL.md content at runtime. The skill names are selected based on the agent manifest's `always_active_skills` and the user's config-level `always_active_skills`.

### Provider Priority

FlowState builds a failback chain with providers in this order: **anthropic → github-copilot → openai → zai → openzen → ollama**. The `buildModelPreferences` function (at `internal/engine/engine.go`) iterates providers in this order when constructing the failback chain. If the first provider fails (e.g. model not found, auth error), the next is tried automatically.

### Agent Manifest Model Names

Agent manifests in `~/.local/share/flowstate/agents/` must use **current model names** from the provider. Stale model names (e.g. `claude-3-5-sonnet-20241022`) cause silent failback to the next provider. Use `flowstate models` to list available models and verify names.

### Anthropic Streaming Tool Call Arguments

Anthropic's streaming API sends tool call arguments via `input_json_delta` events across multiple chunks. The Anthropic provider handles this via `streamEventHandler` (`internal/provider/anthropic/streaming.go`), which accumulates `input_json_delta` fragments by block index and emits a complete `tool_call` chunk on `content_block_stop`.

OpenAI-compatible providers (OpenAI, Z.AI, OpenZen) handle streaming tool calls correctly via the `openaicompat` package, which uses `ChatCompletionAccumulator` to reassemble fragmented tool call arguments before dispatching.
