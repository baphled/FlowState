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

Track work items in `tasks/` directory instead.

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

## Testing

- Test files: `*_test.go`
- BDD features: `features/*.feature`
- Step definitions: `features/steps/*_steps.go`
- Use table-driven tests for unit tests

## Formatting

Run before committing:

```bash
make fmt   # gofmt + goimports
make lint  # golangci-lint
```
