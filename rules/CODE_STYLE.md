# Code Style

FlowState follows strict code style guidelines for consistency and maintainability.

## Import Order

```go
import (
    // 1. Standard library (alphabetical)
    "context"
    "fmt"
    "strings"
    
    // 2. External packages (alphabetical)
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    
    // 3. Internal packages (alphabetical)
    "github.com/baphled/flowstate/internal/provider"
    "github.com/baphled/flowstate/internal/tui/intents"
)
```

## Naming Conventions

| Element | Convention | Example |
|---------|------------|---------|
| Files | `snake_case.go` | `chat_intent.go` |
| Packages | `lowercase` | `intents` |
| Types | `PascalCase` | `ChatIntent` |
| Private | `camelCase` | `handleMessage` |
| Constants | `PascalCase` | `MaxRetries` |
| Interfaces | `PascalCase` (often -er suffix) | `Provider`, `Renderer` |

## Comments

### FORBIDDEN Inside Functions

```go
// BAD - No comments inside function bodies
func (c *Chat) Update(msg tea.Msg) tea.Cmd {
    // Handle key press  <- FORBIDDEN
    switch m := msg.(type) {
        // ...
    }
}

// GOOD - Code should be self-documenting
func (c *Chat) Update(msg tea.Msg) tea.Cmd {
    switch m := msg.(type) {
        // ...
    }
}
```

### Allowed: Package and Type Documentation

```go
// Package intents provides workflow orchestrators for the TUI.
package intents

// ChatIntent manages the chat conversation workflow.
type ChatIntent struct {
    // ...
}
```

### Forbidden Markers

Never use: `TODO`, `FIXME`, `HACK`, `XXX`

Track work items in Dropbox `/FlowState/Metadata/attempts/`; read the current
`/FlowState/Metadata/ACTIVE.json` pointer. Never create repository-local task records.

## Error Handling

```go
// Return errors, don't panic
func (p *OllamaProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
    resp, err := p.client.Chat(ctx, req)
    if err != nil {
        return ChatResponse{}, fmt.Errorf("ollama chat: %w", err)
    }
    return resp, nil
}
```

## Function Length

- Prefer functions under 50 lines
- Extract helper functions for complex logic
- Each function should do one thing

## Struct Initialization

```go
// Use named fields
intent := &ChatIntent{
    provider: provider,
    session:  session,
    input:    textinput.New(),
}

// Not positional
intent := &ChatIntent{provider, session, textinput.New()} // BAD
```

## Type ownership and quality gates

Package-level production Go structs, interfaces, aliases and other named types live
in `types.go` or `*_types.go` within the package that owns them. Keep methods,
constructors, other functions and mutable variables in implementation files.
Related constants may stay with their types. Do not move domain types into a
generic shared package to satisfy a filename check. Test fixture types may stay
in `*_test.go`.

GitHub Actions checks every added or modified production Go file against its
committed source, using the actual PR base SHA. Generated-file comments do not
waive this rule. Untouched legacy files are not rewritten by this gate; when a
production file is touched, separate its types as part of that bounded change.

The existing full-project static checks remain required. New-code lint adds
checked errors, bounded function size/complexity and resource cleanup checks.
Security findings fail CI and block the required Build job. No paid AI reviewer
is needed to execute these gates; independent review is still required.

## Testing

- Test files: `*_test.go`
- Standard Go `testing` unit/integration/headless CLI tests only
- No Cucumber, Godog, Gherkin feature files or step-definition suites
- Use table-driven tests for unit tests

## Formatting

Run before committing:

```bash
make fmt   # gofmt + goimports
make lint  # golangci-lint
```
