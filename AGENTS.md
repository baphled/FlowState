# FlowState AI Agent Instructions

**Quick reference for AI coding agents working on FlowState - a Go TUI application using Bubble Tea framework.**

## Git Worktree Setup

FlowState uses git worktrees. The structure is:

```
~/Projects/
├── FlowState.git/           # Bare repository
│   └── main/                # Primary development worktree
└── FlowState-feature-xxx/   # Feature worktrees (temporary)
```

**Always work in a worktree, never in the bare repo directly.**

### Creating Feature Branches

```bash
cd /home/baphled/Projects/FlowState.git
git worktree add -b feature/my-feature ../FlowState-feature-my-feature main
cd ../FlowState-feature-my-feature
```

### Cleaning Up

```bash
cd /home/baphled/Projects/FlowState.git/main
git worktree remove ../FlowState-feature-my-feature
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
