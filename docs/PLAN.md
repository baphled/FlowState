# FlowState - Project Plan

**A general-purpose AI assistant TUI for everyday tasks.**

FlowState is inspired by opencode but not limited to coding. It provides AI-assisted workflows for research, analysis, decision-making, task management, and any domain where AI can help.

## Vision

- **Ollama-first** - Local models are first-class citizens
- **Provider-agnostic** - Easy to plug in any model provider
- **MCP-first** - Connect to external memory, RAG, and tools via Model Context Protocol
- **Local-first** - Optional local memory server with user control
- **Domain-flexible** - Not locked to programming tasks
- **Go-native** - Lean, fast, single binary
- **BubbleTea TUI** - Rich terminal experience
- **Git Worktrees** - Parallel development from day one

---

## Core Features

### 1. Basic Chat
- Streaming responses from LLMs
- Multi-turn conversations with context
- Support for multiple providers (Ollama, OpenAI, Anthropic)

### 2. Session Management
- Persistent conversations (SQLite)
- Session browser with search
- Fork/continue sessions
- Auto-generated titles

### 3. Vim Navigation
- Full vim motions: `j/k`, `gg/G`, `Ctrl+u/d/f/b`
- Search with `/`, navigate with `n/N`
- Mode switching: Normal, Insert, Search
- `$EDITOR` integration (`Ctrl+e`)

### 4. Task Panel
- View current tasks and progress
- See recent changes
- Quick access to commands
- Model status display

### 5. MCP Integration
- Connect to external MCP servers (memory, RAG, filesystem, etc.)
- Dual transport support (stdio + SSE)
- Server discovery and management
- Optional local memory MCP server (SQLite-based)

### 6. Tool System
- Bash execution with permissions
- File read/write
- Web fetching
- Granular permission model (Allow/Ask/Deny)

### 7. Skills & Commands
- `/analyze` - Systems thinking analysis
- `/challenge` - Devil's advocate evaluation
- `/research` - Systematic investigation
- `/decide` - Structured decision making
- `/task` - Task creation
- `/models` - Model selection
- `/connect` - Provider configuration

---

## Technical Stack

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Language | Go 1.22+ | Fast, single binary, strong concurrency |
| TUI | BubbleTea | Elm architecture, excellent ecosystem |
| Database | SQLite | Simple, embedded, reliable |
| MCP | stdio + SSE | Model Context Protocol for external integrations |
| Markdown | goldmark | Standard, pure Go |
| Testing | Godog + Ginkgo | BDD-driven development |

---

## Git Worktree Workflow

FlowState uses git worktrees for parallel development:

```
~/Projects/
└── FlowState.git/           # Bare repository
    ├── main/                # Primary development worktree
    ├── feature-chat/        # Feature worktree (temporary)
    ├── bugfix-xxx/          # Bugfix worktree (temporary)
    └── hooks/               # Git hooks (shared)
```

### Working with Worktrees

```bash
# Create feature worktree (within project directory)
cd FlowState.git
git worktree add -b feature/chat feature-chat main

# Work on feature
cd feature-chat
# ... make changes ...

# Clean up when done
cd ../main
git merge feature/chat
git worktree remove ../feature-chat
git branch -d feature/chat
```

---

## Architecture Overview

### Layer Hierarchy

```
App -> Intents -> Screens/Modals -> UIKit -> Behaviors
```

### Key Patterns (from KaRiya)

1. **Intent Pattern** - Workflow orchestrators with modified TEA
2. **ScreenResult** - Type-safe communication from screens to intents
3. **ContentProvider** - `AsScreen`/`AsModal` flexible rendering
4. **Theme-Aware** - Components embed theme for consistent styling
5. **Modal Registry** - Prioritized modal management

### Directory Structure

```
flowstate/
├── cmd/flowstate/           # CLI entry point
├── internal/
│   ├── provider/            # LLM provider abstraction
│   ├── session/             # Session management
│   ├── tools/               # Built-in tools
│   ├── skills/              # Skill system
│   ├── mcp/                 # MCP integration
│   │   ├── client/          # MCP client (stdio + SSE transport)
│   │   ├── types/           # MCP types (Resource, Tool, etc.)
│   │   └── memory/          # Optional local memory MCP server
│   └── tui/                 # BubbleTea UI
├── docs/                    # Documentation
├── rules/                   # Development rules
├── tasks/                   # Task tracking
├── features/                # Cucumber features
└── .flowstate/              # User config
```

---

## Development Phases

### Phase 1: Foundation ✅ COMPLETE
- [x] Project scaffolding with git worktrees
- [x] Basic TUI shell with BubbleTea
- [x] Provider interface definition
- [x] Ollama provider with streaming
- [x] Basic chat with mode switching (Normal/Insert)
- [x] Message viewport with scrolling
- [x] BDD test harness with godog
- [x] First smoke test passing

### Phase 1.5: UI Polish ✅ COMPLETE
- [x] OpenCode-inspired layout and styling
- [x] Improved header/footer design
- [x] Better message formatting and colours (Glamour)
- [x] Status bar with context info (provider, model, tokens)
- [x] Proper theme system

### Phase 2: Navigation & Input ✅ COMPLETE
- [x] Vim navigation (j/k, gg/G, Ctrl+u/d)
- [x] Mode state machine (Normal/Insert)
- [x] Search mode with `/`, `n`, `N`
- [x] $EDITOR integration
- [x] Message viewport with scrolling

### Phase 3: Sessions ✅ COMPLETE
- [x] SQLite session store
- [x] Session CRUD operations
- [x] Session browser intent
- [x] Auto-generated titles

### Phase 4: Task Panel & UI ✅ COMPLETE
- [x] Task panel component
- [x] Model selector
- [x] Command palette
- [x] Help system
- [x] Theme system

### Phase 5: Tool System ✅ COMPLETE
- [x] Tool interface
- [x] Permission system
- [x] Bash tool
- [x] File read/write tools
- [x] Web fetch tool

### Phase 6: Skills & Commands ✅ COMPLETE
- [x] Skill loader
- [x] Command loader
- [x] Built-in skills (analyze, challenge, research)
- [x] Custom command support

### Phase 7: MCP Integration ✅ COMPLETE
- [x] MCP client with stdio transport
- [x] SSE transport support
- [x] Server discovery (config file + env var)
- [x] MCP resource access (documents, memory)
- [x] MCP tool integration with permission prompts (Allow/Ask/Deny)
- [x] Optional local memory MCP server (SQLite, `--memory-server` flag)

### Phase 8: MVP Finalisation ✅ COMPLETE
- [x] Documentation update (README, Demo)
- [x] `go install` support
- [x] Final smoke tests and performance check

---

## Vim Navigation Specification

### Motions

| Motion | Key(s) | Action |
|--------|--------|--------|
| Line up | `k`, `↑` | Scroll up one line |
| Line down | `j`, `↓` | Scroll down one line |
| Half page up | `Ctrl+u` | Scroll up half page |
| Half page down | `Ctrl+d` | Scroll down half page |
| Full page up | `Ctrl+b` | Scroll up full page |
| Full page down | `Ctrl+f` | Scroll down full page |
| Go to top | `gg` | Jump to first line |
| Go to bottom | `G` | Jump to last line |
| Search forward | `/` | Enter search mode |
| Next match | `n` | Jump to next match |
| Previous match | `N` | Jump to previous match |

### Modes

| Mode | Enter | Exit | Purpose |
|------|-------|------|---------|
| Normal | Default, `Esc` | `i`, `/` | Navigation |
| Insert | `i`, `a` | `Esc`, `Ctrl+Enter` | Compose message |
| Search | `/` | `Enter`, `Esc` | Search content |

---

## Task Panel Specification

```
┌─ Tasks ─────────────────────────────────────────┐
│ [*] Researching vacation destinations           │
│ [ ] Compare hotel options                       │
│ [ ] Book flights                                │
├─ Recent ────────────────────────────────────────┤
│ + Created task: Book flights                    │
│ ~ Modified: vacation-notes.md                   │
│ > Ran: ls -la ~/Documents                       │
├─ Model ─────────────────────────────────────────┤
│ llama3.2 (Ollama) ▼                            │
├─ Commands ──────────────────────────────────────┤
│ /analyze /challenge /research /decide /task    │
└─────────────────────────────────────────────────┘
```

---

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

---

## Intent Interface

```go
type Intent interface {
    Init() tea.Cmd
    Update(msg tea.Msg) tea.Cmd  // Note: returns Cmd only, not (Model, Cmd)
    View() string
    Result() *IntentResult
}
```

---

## Skills to Include

### Core Skills (Generalized from KaRiya)

| Skill | Purpose |
|-------|---------|
| `analyze` | Systems thinking, impact analysis |
| `challenge` | Devil's advocate, assumption testing |
| `research` | Systematic investigation |
| `complete` | Task completion verification |
| `plan` | Task breakdown and planning |
| `checklist` | Track progress with discipline |
| `efficient` | Token-efficient communication |

### Commands

| Command | Purpose |
|---------|---------|
| `/analyze <topic>` | Systems analysis workflow |
| `/challenge <idea>` | Devil's advocate evaluation |
| `/decide <options>` | Structured decision making |
| `/research <topic>` | Systematic research |
| `/complete <task>` | Task completion check |
| `/continue` | Resume previous work |
| `/task <description>` | Create new task |
| `/models` | List/select models |
| `/connect <provider>` | Add API credentials |
| `/mcp add <server>` | Add MCP server connection |
| `/mcp list` | List configured MCP servers |
| `/mcp remove <name>` | Remove MCP server |
| `/mcp status` | Show MCP connection status |
| `/help` | Show available commands |

---

## Next Steps

1. Complete basic TUI shell
2. Implement Ollama provider
3. Create chat intent with streaming
4. Add vim navigation
5. Implement session persistence
6. Build task panel

See `tasks/` for current work items.
