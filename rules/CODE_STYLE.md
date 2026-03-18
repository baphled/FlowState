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

## Structured Docblocks (MANDATORY)

All exported symbols require structured documentation blocks. This is enforced by `make check-docblocks`.

### Format Specification

The standard docblock follows a 4-section format:

```go
// SymbolName does what it does, in one concise sentence.
//
// Expected:
//   - param1 is a valid non-empty string.
//   - param2 is a positive integer.
//
// Returns:
//   - The resulting value on success.
//   - An error wrapping the underlying failure.
//
// Side effects:
//   - None. (or describe any state mutation or I/O)
```

### Section Rules

| Section | Required When |
|---------|---------------|
| **Summary** (first line) | Always — must start with the symbol's own name |
| **Expected:** | Function or method takes any parameters |
| **Returns:** | Function or method has any return values |
| **Side effects:** | Always on exported functions and methods (use `None.` if there are none) |

### Function with Parameters

```go
// New creates a new ChatIntent with the given provider.
//
// Expected:
//   - provider is a non-nil Provider implementation.
//
// Returns:
//   - A fully initialised ChatIntent ready for use.
//
// Side effects:
//   - None.
func New(provider Provider) *ChatIntent {
    return &ChatIntent{provider: provider}
}
```

### Method with Return Values

```go
// Send transmits a message to the AI provider and returns the response.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - message is a non-empty user input string.
//
// Returns:
//   - The provider's response text on success.
//   - An error wrapping the underlying failure.
//
// Side effects:
//   - Performs network I/O to the configured AI provider.
func (c *ChatIntent) Send(ctx context.Context, message string) (string, error) {
    return c.provider.Chat(ctx, message)
}
```

### Type Documentation

Types require a one-line comment starting with the type name:

```go
// ChatOptions holds the configuration flags for the chat subcommand.
type ChatOptions struct {
    Model    string
    Provider string
    Timeout  time.Duration
}

// Provider defines the contract for AI model backends.
type Provider interface {
    Chat(ctx context.Context, req Request) (Response, error)
}
```

### Package Documentation

Every package must have a `doc.go` file:

```go
// Package cli provides the command-line interface for FlowState.
//
// The package organises commands into subcommands and handles
// configuration parsing, flag binding, and output formatting.
package cli
```

### British English (MANDATORY)

All doc comments use British English throughout:

- **behaviour** (not behavior)
- **initialise** (not initialize)
- **recognise** (not recognize)
- **colour** (not color)
- **organise** (not organize)
- **-ise suffix** throughout (not -ize)

### Exclusions

- **Test files** (`_test.go`) do not require structured docblocks
- **`main` and `init` functions** are excluded from checks
- **Unexported symbols** are not checked

### Forbidden

Bare one-line comments that do not satisfy the structured format are not acceptable for exported symbols.

```go
// BAD — missing sections
// New creates a new thing.
func New(provider Provider) *ChatIntent { ... }

// GOOD — includes all required sections
// New creates a new ChatIntent with the given provider.
//
// Expected:
//   - provider is a non-nil Provider implementation.
//
// Returns:
//   - A fully initialised ChatIntent ready for use.
//
// Side effects:
//   - None.
func New(provider Provider) *ChatIntent { ... }
```

A bare comment like `// New creates a new thing.` is only valid if the function has no parameters and no return values; otherwise the relevant sections are required.

### Enforcement

```bash
make check-docblocks    # Validate docblock compliance
make check              # Run all checks including docblocks
```

Run `make check-docblocks` before committing to ensure compliance.

## Comments Inside Functions

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

## Struct Initialisation

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
