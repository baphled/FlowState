# Architecture Rules

FlowState follows a strict layered architecture inspired by the KaRiya project.

## Layer Hierarchy

```
App -> Intents -> Screens/Modals -> UIKit -> Behaviors
```

Each layer can only depend on layers to its right.

## Dependency Rules

| Layer | Can Import | CANNOT Import |
|-------|------------|---------------|
| `app/` | `intents/`, `screens/`, `uikit/` | - |
| `intents/` | `screens/`, `uikit/` | `app/` |
| `screens/` | `uikit/` | `app/`, `intents/` |
| `uikit/` | Standard lib, external | `app/`, `intents/`, `screens/` |

### Critical Rule

**`screens/` NEVER imports `intents/`**

Communication from screens to intents uses `ScreenResult`:

```go
// In screens/chat/chat_screen.go
type Result struct {
    Message string
    Action  Action
}

func (s *Screen) Result() *Result {
    return s.result
}

// In intents/chat/chat_intent.go
func (i *ChatIntent) Update(msg tea.Msg) tea.Cmd {
    if result := i.screen.Result(); result != nil {
        switch result.Action {
        case screens.ActionSend:
            return i.sendMessage(result.Message)
        }
    }
    return nil
}
```

## Intent Pattern

Intents are workflow orchestrators with a modified TEA interface:

```go
type Intent interface {
    Init() tea.Cmd
    Update(msg tea.Msg) tea.Cmd  // Note: Cmd only, NOT (Model, Cmd)
    View() string
    Result() *IntentResult
}
```

### Why `Update` Returns Only `tea.Cmd`

- Intents manage their own state internally
- Reduces boilerplate
- Cleaner composition

## ContentProvider Pattern

Components can render as screens or modals:

```go
type ContentProvider interface {
    Content(width, height int) string
    Title() string
    Footer() string
}

// Flexible rendering
asScreen := render.AsScreen(component, layout)
asModal := render.AsModal(component, background, w, h, theme)
```

## Theme-Aware Components

Components embed theme for consistent styling:

```go
type Component struct {
    theme theme.Theme
    // ...
}

func New(t theme.Theme) *Component {
    return &Component{theme: t}
}
```

## Modal Registry

Modals are managed through a priority-based registry:

```go
type ModalPriority int

const (
    PriorityLow ModalPriority = iota
    PriorityNormal
    PriorityHigh
    PriorityCritical
)
```

## Directory Structure

```
internal/
├── provider/          # LLM provider abstraction
│   ├── provider.go    # Interface definition
│   ├── ollama/        # Ollama implementation
│   ├── openai/        # OpenAI implementation
│   └── anthropic/     # Anthropic implementation
├── session/           # Session management
├── tools/             # Built-in tools
├── skills/            # Skill system
├── memory/            # Memory system (future)
├── rag/               # RAG system (future)
└── tui/               # BubbleTea UI
    ├── app/           # Main application
    ├── intents/       # Workflow orchestrators
    │   ├── chat/
    │   ├── sessions/
    │   └── settings/
    ├── screens/       # Screen components
    │   └── base/
    └── uikit/         # Reusable UI components
        ├── containers/
        ├── feedback/
        ├── layout/
        ├── primitives/
        ├── render/
        └── theme/
```

## Provider Interface

```go
type Provider interface {
    Name() string
    Models() ([]Model, error)
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    Stream(ctx context.Context, req ChatRequest) (<-chan StreamChunk, error)
    Embed(ctx context.Context, text string) ([]float32, error)
}
```

## Tool Interface

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() []Parameter
    Execute(ctx context.Context, args map[string]any) (Result, error)
}

type Permission int

const (
    PermissionAllow Permission = iota
    PermissionAsk
    PermissionDeny
)
```
