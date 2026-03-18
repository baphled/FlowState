# Architecture Overview

FlowState is built as a layered TUI application using BubbleTea.

## System Diagram

```
┌─────────────────────────────────────────────────────────────────┐
│                           FlowState                             │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                         App                              │   │
│  │  - Manages active intent                                 │   │
│  │  - Routes global keys (Ctrl+p, q, etc.)                 │   │
│  │  - Renders layout                                        │   │
│  └─────────────────────────────────────────────────────────┘   │
│                              │                                  │
│                              ▼                                  │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                       Intents                            │   │
│  │  ┌─────────┐  ┌───────────┐  ┌──────────┐              │   │
│  │  │  Chat   │  │  Sessions │  │ Settings │              │   │
│  │  └─────────┘  └───────────┘  └──────────┘              │   │
│  │  - Workflow orchestrators                                │   │
│  │  - Manage view lifecycle                                 │   │
│  │  - Coordinate providers/tools                            │   │
│  └─────────────────────────────────────────────────────────┘   │
│                              │                                  │
│                              ▼                                  │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                        UIKit                             │   │
│  │  ┌────────────┐  ┌─────────┐  ┌──────────┐             │   │
│  │  │ Containers │  │ Layout  │  │  Theme   │             │   │
│  │  └────────────┘  └─────────┘  └──────────┘             │   │
│  │  ┌────────────┐  ┌─────────┐  ┌──────────┐             │   │
│  │  │ Primitives │  │ Render  │  │ Feedback │             │   │
│  │  └────────────┘  └─────────┘  └──────────┘             │   │
│  └─────────────────────────────────────────────────────────┘   │
│                                                                 │
├─────────────────────────────────────────────────────────────────┤
│                        External Services                        │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                       Providers                          │   │
│  │  ┌────────┐  ┌─────────┐  ┌───────────┐                │   │
│  │  │ Ollama │  │ OpenAI  │  │ Anthropic │                │   │
│  │  └────────┘  └─────────┘  └───────────┘                │   │
│  └─────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                         Tools                            │   │
│  │  ┌──────┐  ┌──────────┐  ┌───────────┐                 │   │
│  │  │ Bash │  │   File   │  │ WebFetch  │                 │   │
│  │  └──────┘  └──────────┘  └───────────┘                 │   │
│  └─────────────────────────────────────────────────────────┘   │
│  ┌─────────────────────────────────────────────────────────┐   │
│  │                       Storage                            │   │
│  │  ┌──────────────┐  ┌─────────────┐                      │   │
│  │  │   Sessions   │  │   Memory    │                      │   │
│  │  │   (SQLite)   │  │ (sqlite-vec)│                      │   │
│  │  └──────────────┘  └─────────────┘                      │   │
│  └─────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Data Flow

### Chat Message Flow

```
User Input
    │
    ▼
┌─────────┐     ┌──────────────┐     ┌──────────┐
│  Input  │ ──► │  ChatIntent  │ ──► │ Provider │
│ Screen  │     │              │     │ (Ollama) │
└─────────┘     └──────────────┘     └──────────┘
                       │                   │
                       │                   │
                       ▼                   ▼
                ┌──────────────┐   ┌────────────┐
                │   Session    │   │  Streaming │
                │    Store     │   │  Response  │
                └──────────────┘   └────────────┘
                                         │
                                         ▼
                                  ┌────────────┐
                                  │  Viewport  │
                                  │  (tokens)  │
                                  └────────────┘
```

### Tool Execution Flow

```
LLM Response (with tool call)
    │
    ▼
┌─────────────────┐
│  Parse Tool     │
│  Request        │
└─────────────────┘
    │
    ▼
┌─────────────────┐     ┌───────────────┐
│   Permission    │ ──► │ Ask User?     │
│   Check         │     │ (if required) │
└─────────────────┘     └───────────────┘
    │
    ▼ (allowed)
┌─────────────────┐
│  Execute Tool   │
│  (sandboxed)    │
└─────────────────┘
    │
    ▼
┌─────────────────┐
│  Return Result  │
│  to LLM         │
└─────────────────┘
```

## Key Interfaces

### Provider

```go
type Provider interface {
    Name() string
    Models() ([]Model, error)
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    Embed(ctx context.Context, text string) ([]float32, error)
}
```

### Intent

```go
type Intent interface {
    Init() tea.Cmd
    Update(msg tea.Msg) tea.Cmd
    View() string
    Result() *IntentResult
}
```

### Tool

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() []Parameter
    Execute(ctx context.Context, args map[string]any) (Result, error)
}
```

## Component Patterns

### ContentProvider

Allows components to render as either screens or modals:

```go
type ContentProvider interface {
    Content(width, height int) string
    Title() string
    Footer() string
}
```

### ViewResult

Type-safe communication from views to intents:

```go
type ViewResult struct {
    Action  Action
    Payload any
}
```

### Theme-Aware

Components embed theme for consistent styling:

```go
type Component struct {
    theme theme.Theme
}
```

## See Also

- [ARCHITECTURE.md](../../rules/ARCHITECTURE.md) - Architecture rules
- [PLAN.md](../PLAN.md) - Full project plan
