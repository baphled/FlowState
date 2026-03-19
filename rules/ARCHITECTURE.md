# Architecture Rules

FlowState follows a strict layered architecture inspired by the KaRiya project.

## Layer Hierarchy

```
App -> Intents -> Views -> UIKit Components + Theme System
```

Each layer can only depend on layers to its right.

**Specifically:**
- **App** → manages active intent, routes global keys, renders layout
- **Intents** → workflow orchestrators, manage view lifecycle, coordinate providers/tools
- **Views** → render intent UI using UIKit components and theme system
- **UIKit** → reusable components (containers, layout, primitives, render utilities)
- **Theme** → MVP-scoped system for consistent styling across the UI

## Dependency Rules

| Layer | Can Import | CANNOT Import |
|-------|------------|---------------|
| `app/` | `intents/` | - |
| `intents/` | `uikit/` (via views) | `app/` |
| `views/` | `uikit/`, `theme/` | `intents/`, `app/` |
| `uikit/` | `theme/`, standard lib, external | `app/`, `intents/`, `views/` |
| `theme/` | Standard lib, external | `app/`, `intents/`, `uikit/` |

### Critical Rule

**`uikit/` NEVER imports `intents/`**

Communication from views to intents uses `IntentResult`:

```go
// In intents/chat/chat_intent.go
type IntentResult struct {
    Message string
    Action  Action
}

func (i *ChatIntent) Result() *IntentResult {
    return i.result
}

// Intent Update method
func (i *ChatIntent) Update(msg tea.Msg) tea.Cmd {
    // Handle user input and return commands
    // State is managed internally by the intent
    return nil
}
```

## Intent Pattern

Intents are workflow orchestrators with a modified TEA interface:

```go
type Intent interface {
    Init() tea.Cmd
    Update(msg tea.Msg) tea.Cmd  // Returns Cmd only, NOT (Model, Cmd)
    View() string
    Result() *IntentResult
}
```

### Why `Update` Returns Only `tea.Cmd`

- Intents manage their own state internally
- Reduces boilerplate
- Cleaner composition
- Intents communicate results via `IntentResult[T]` — never direct state mutation

## Theme System (MVP Scope)

The theme system provides consistent styling across all UI components and is part of the MVP.

```go
type Theme struct {
    // Color palette
    Primary, Secondary, Accent color.Color
    Background, Surface, Overlay color.Color
    Text, TextSecondary color.Color
    
    // Component styling
    BorderStyle string
    PaddingX, PaddingY int
}

// All UIKit components accept and embed a Theme
type Component struct {
    theme theme.Theme
    // ...
}

func New(t theme.Theme) *Component {
    return &Component{theme: t}
}
```

**Theme is used by:**
- All UIKit components (containers, layout, primitives, feedback)
- Intent views for consistent styling
- Modal overlays and feedback components

**Theme is NOT deferred** — it is required for MVP launch.

## ContentProvider Pattern

Components can render as views or modals:

```go
type ContentProvider interface {
    Content(width, height int) string
    Title() string
    Footer() string
}

// Flexible rendering
asView := render.AsView(component, layout)
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
├── mcp/               # MCP integration
│   ├── client/        # MCP client
│   │   ├── transport/ # stdio transport (SSE deferred)
│   │   ├── handler.go # Request/response handling
│   │   └── server.go  # Server connection management
│   ├── types/         # MCP types (Resource, Tool, etc.)
│   └── memory/        # Optional local memory MCP server
└── tui/               # BubbleTea UI
    ├── app/           # Main application
    ├── intents/       # Workflow orchestrators
    │   ├── chat/
    │   ├── sessions/
    │   └── settings/
    ├── views/         # View components
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

## MCP Integration

FlowState acts as an MCP client, connecting to external MCP servers for memory, RAG, and other capabilities.

### MCP Client Interface

```go
type MCPClient interface {
    Connect(ctx context.Context, server ServerConfig) error
    Disconnect(serverName string) error
    ListServers() []ServerStatus
    
    // Resources
    ListResources(serverName string) ([]Resource, error)
    ReadResource(serverName, uri string) (ResourceContent, error)
    
    // Tools
    ListTools(serverName string) ([]Tool, error)
    CallTool(ctx context.Context, serverName, toolName string, args map[string]any) (Result, error)
}
```

### Transport Support

```go
type Transport interface {
    Connect(ctx context.Context) error
    Send(msg Message) error
    Receive() <-chan Message
    Close() error
}

// Implementations:
// - StdioTransport: Connect via stdin/stdout to subprocess
// - SSETransport: Deferred (HTTP SSE not in MVP)
```

### Permission Integration

MCP tools use the existing `Allow/Ask/Deny` permission system:

```go
type MCPToolPermission struct {
    ServerName string
    ToolName   string
    Permission Permission
}
```

### Optional Local Memory Server

When enabled via `--memory-server` flag:

```go
type MemoryServer struct {
    db *sql.DB  // SQLite storage
}

func (s *MemoryServer) ListResources() []Resource
func (s *MemoryServer) ReadResource(uri string) ResourceContent
func (s *MemoryServer) WriteResource(uri string, content ResourceContent) error
```

## Deferred to v2

The following features are **NOT** part of the MVP scope and are planned for future releases:

- **Vim navigation keybindings** — Currently supports basic arrow keys and Ctrl+p; Vim mode (hjkl, modes) planned for v2
- **Command palette** — Quick command invocation will be added in v2
- **Memory MCP server** — Optional local memory server is deferred; external MCP memory servers supported in MVP
- **SQLite session persistence** — Session storage will move to structured database in v2; MVP uses file-based sessions
- **Advanced modal state management** — Current modal priority system is v1; enhanced composition planned for v2
