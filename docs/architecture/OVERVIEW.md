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
│  │                  Views + UIKit                           │   │
│  │  Render intent UI using reusable UIKit components       │   │
│  │  with consistent Theme system styling                    │   │
│  │  ┌────────────┐  ┌─────────┐  ┌──────────┐             │   │
│  │  │ Containers │  │ Layout  │  │  Theme   │  (MVP)      │   │
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

## TUI Architecture (MVP)

### Layer Hierarchy

The FlowState TUI is structured in clear layers:

```
App → Intents → Views → UIKit Components + Theme System
```

**Each layer only depends on layers to its right:**
- **App** — Coordinates the active intent and routes global keyboard shortcuts
- **Intents** — Workflow orchestrators that manage view lifecycle and coordinate with providers/tools
- **Views** — Intent-specific rendering using UIKit components styled with the Theme system
- **UIKit** — Reusable components: layout (grid, flex, stack), containers (box, overlay, modal), primitives (button, input, list), and render utilities
- **Theme** — Consistent styling system used by all UIKit components (MVP scope, not deferred)

### UIKit Scope (MVP)

UIKit provides a complete set of reusable components for building intent views:

| Component Category | Examples | Purpose |
|-------------------|----------|---------|
| **Layout** | Grid, Flex, Stack | Responsive component arrangement |
| **Containers** | Box, Overlay, Modal | Content wrapper and positioning |
| **Primitives** | Button, Input, List | Basic interactive elements |
| **Render** | AsScreen, AsModal | Flexible rendering as view or modal |
| **Feedback** | Toast, Alert, Spinner | User feedback and loading states |
| **Theme** | Colors, Spacing, Styles | Consistent styling across all components |

All UIKit components accept a **Theme** parameter for consistent, customizable styling.

### Intent/View → UIKit Flow

```
Intent (Workflow Orchestrator)
  ├─ Init() → Initialize state, subscribe to events
  ├─ Update(msg) → Handle user input, update state
  ├─ View() → Render using UIKit components
  │   └─ UIKit components styled with Theme
  └─ Result() → Return workflow result

Views use:
  - UIKit containers (Box, Modal) for layout
  - UIKit components (Button, Input, List) for interaction
  - Theme system for consistent colors and spacing
```

## Data Flow

### Chat Message Flow

```
User Input
    │
    ▼
┌─────────────┐     ┌──────────────┐     ┌──────────┐
│ ChatIntent  │ ──► │  ChatIntent  │ ──► │ Provider │
│(Input View) │     │              │     │ (Ollama) │
└─────────────┘     └──────────────┘     └──────────┘
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

Allows components to render as either intents with views or modals:

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

## Deferred to v2

The following features are **explicitly deferred** and not part of the MVP scope:

- **Vim navigation keybindings** — MVP supports basic arrow keys and `Ctrl+p`; Vim mode (hjkl, modeful navigation) planned for v2
- **Command palette** — Quick command invocation will be added in v2; MVP uses intent-based navigation
- **Memory MCP server** — Optional local memory server is deferred; external MCP servers supported in MVP
- **Structured session persistence** — MVP uses file-based sessions; SQLite migration planned for v2
- **Advanced modal composition** — Current modal priority system meets MVP needs; enhanced composition patterns in v2

**These deferral decisions allow us to:**
- Launch MVP with core chat and session functionality
- Keep codebase focused and testable
- Iterate on user feedback before building optional features
- Plan v2 with clearer requirements from MVP usage

## See Also

- [ARCHITECTURE.md](../../rules/ARCHITECTURE.md) - Architecture rules and dependency constraints
- [PLAN.md](../PLAN.md) - Full project plan
