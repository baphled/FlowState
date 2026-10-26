# FlowState AI Agent Instructions

**Quick reference for AI coding agents working on FlowState - a Go TUI application using Bubble Tea framework.**

## Branch Policy (MANDATORY)

**Working directly against `main` is ILLEGAL.** All development MUST target the
`next` branch (or a feature worktree created FROM `next`). Never commit to
`main`, never check out `main` for editing. Follow the CI process on every
change: `make check` must pass, pre-commit hooks must run (never `--no-verify`),
and all commits must go through `make ai-commit`.

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
git worktree add -b feature/my-feature feature-my-feature next
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
| **Run race checks** | `go test -race -count=1 ./...` |
| **Build** | `make build` |
| **Format** | `make fmt` |
| **Lint** | `make lint` |
| **Full check** | `make check` |

## Development Workflow

### Choose Tests for the Change

Use focused Go unit and integration tests by default. For a bug fix, reproduce
the failure with a regression test before changing the implementation. Reuse
existing coverage when it already validates the change.

Cucumber is optional. Reserve it for a small number of acceptance flows where
the shared scenario adds value beyond a Go test. Do not add a feature file or
Godog dependency just to satisfy a process rule, or require Cucumber for routine
code, configuration, CI, or documentation changes.

See [the testing workflow](rules/BDD_WORKFLOW.md) for test selection and CI gates.

### The Smallest Change

| Situation | Smallest Change | NOT Smallest Change |
|-----------|-----------------|---------------------|
| Need a function | Create empty function returning nil | Implement full logic |
| Need a struct | Create struct with needed field | Add all possible fields |
| Need validation | Add one validation rule | Add all validations |
| Need UI element | Add minimal element | Full styled component |

### Commit Cadence

```
test(chat): reproduce missing response
fix(chat): preserve response events
refactor(chat): simplify message formatting
```

## Architecture

### Layer Hierarchy (MUST follow)

```
App -> Intents -> Screens/Modals -> UIKit -> Behaviors
```

### Dependency Rules

- `screens/` **NEVER** imports `intents/`
- `uikit/` **NEVER** imports `screens/` or `intents/`
- Use `ScreenResult` for screen -> intent communication

### Intent Pattern

```go
type Intent interface {
    Init() tea.Cmd
    Update(msg tea.Msg) tea.Cmd  // Returns Cmd only, NOT (Model, Cmd)
    View() string
    Result() *IntentResult
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

## Testing

### Running Tests

```bash
make test                          # Go tests
go test -race -count=1 ./...         # Uncached tests with race detection
make check                         # Format, lint, and test
```

The checked-in Gherkin files describe planned acceptance behaviour. There is
currently no runnable Godog suite, so `make bdd*` is not a CI gate and those files
are not evidence of passing tests. Any future required acceptance suite must
execute real assertions and fail on missing or pending steps and empty selection.

## When to REFUSE

**Immediately refuse if asked to:**

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
