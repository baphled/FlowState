# FlowState AI Agent Instructions

**Quick reference for AI coding agents working on FlowState - a Go TUI application using Bubble Tea framework.**

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

- `uikit/` **NEVER** imports `intents/` or `behaviors/`
- `behaviors/` **NEVER** imports `intents/`
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

## Code Style

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

### Comments (FORBIDDEN inside functions)

- **NO** comments inside function bodies
- **NO** inline comments
- **NO** markers: `TODO`, `FIXME`, `HACK`

**Exception:** E2E tests may have inline comments.

## Code Documentation

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

**Note:** The rule prohibiting comments inside function bodies (from the Code Style section) still applies.

## Testing

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
| `docs/PLAN.md` | Complete project plan |
| `AGENTS.md` | This file - AI instructions |
| `Makefile` | Build and development commands |
| `rules/*.md` | Development rules |
| `tasks/*.md` | Current tasks |
| `features/*.feature` | BDD scenarios |

## Provider Requirements for Planner/Executor

### Anthropic is Required

The planner and executor agents require **Anthropic** (or GitHub Copilot with Claude models) for reliable tool calling and skill loading. Llama3.2 (Ollama) does not reliably follow `skill_load` tool call instructions — it outputs code-like text instead of making actual tool calls.

### Authentication

FlowState reads Anthropic credentials from `~/.local/share/opencode/auth.json` (the same file OpenCode uses). No separate configuration is needed if OpenCode is already installed and authenticated. The provider supports both direct API keys and OAuth tokens with automatic refresh.

To verify authentication is available:
```bash
cat ~/.local/share/opencode/auth.json | python3 -c "import json,sys; d=json.load(sys.stdin); print([k for k in d.keys()])"
# Should show: ['anthropic', 'github-copilot'] or similar
```

### Skill Directory Configuration

Set `skill_dir` in `~/.config/flowstate/config.yaml` to point to your skills directory:

```yaml
skill_dir: "/home/<user>/.config/opencode/skills"
```

Without this, FlowState defaults to `~/.local/share/flowstate/skills/` which only contains `test-skill`. The always-active skills (`pre-action`, `memory-keeper`, `token-cost-estimation`, `retrospective`, `note-taking`, `knowledge-base`) live in the OpenCode skills directory.

### How Skill Loading Works

The `SkillAutoLoaderHook` (at `internal/hook/skill_autoloader.go`) prepends a lean header to every system message:

```
Your load_skills: [pre-action, memory-keeper, ...]. Call skill_load(name) for each before starting work.
```

Claude then calls the `skill_load` tool to fetch each skill's SKILL.md content at runtime. The skill names are selected based on the agent manifest's `always_active_skills` and the user's config-level `always_active_skills`.

### Provider Priority

FlowState builds a failback chain with providers in this order: **anthropic → github-copilot → openai → ollama**. The `buildModelPreferences` function (at `internal/engine/engine.go`) iterates providers in this order when constructing the failback chain. If the first provider fails (e.g. model not found, auth error), the next is tried automatically.

### Agent Manifest Model Names

Agent manifests in `~/.local/share/flowstate/agents/` must use **current model names** from the provider. Stale model names (e.g. `claude-3-5-sonnet-20241022`) cause silent failback to the next provider. Use `flowstate models` to list available models and verify names.

### Known Limitation: Streaming Tool Call Arguments

Anthropic's streaming API sends tool call arguments via `input_json_delta` events. The current `convertStreamEvent` implementation captures the tool call name and ID from `content_block_start` but does not accumulate argument JSON from subsequent delta events. This means `skill_load` tool calls execute with empty arguments, returning an error. Text-only responses from Claude work correctly. This will be resolved in a future update to the Anthropic streaming handler.
