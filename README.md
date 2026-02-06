# FlowState

A general-purpose AI assistant TUI for everyday tasks.

FlowState brings the power of AI-assisted workflows to your terminal - not just for coding, but for research, analysis, decision-making, and any domain where AI can help.

## Features

- **Ollama-first** - Local models as first-class citizens
- **Provider-agnostic** - Plug in any model provider (OpenAI, Anthropic, etc.)
- **Vim navigation** - Full vim motions for efficient interaction
- **Session management** - Persistent conversations with search
- **Tool system** - Bash, file operations, web fetching with granular permissions
- **Skills & commands** - `/analyze`, `/research`, `/challenge`, `/decide`, and more
- **Privacy-focused** - Local memory with user control

## Requirements

- Go 1.22+
- Ollama (for local models)

## Installation

```bash
go install github.com/baphled/flowstate/cmd/flowstate@latest
```

Or build from source:

```bash
git clone https://github.com/baphled/flowstate.git
cd flowstate
make build
```

## Usage

```bash
# Start FlowState
flowstate

# Start with a specific model
flowstate --model llama3.2

# Connect to a provider
flowstate --provider openai
```

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `i` | Enter insert mode (compose message) |
| `Esc` | Return to normal mode |
| `j/k` | Scroll up/down |
| `gg/G` | Jump to top/bottom |
| `Ctrl+u/d` | Half page scroll |
| `/` | Search |
| `Ctrl+e` | Open $EDITOR |
| `Ctrl+p` | Command palette |
| `q` | Quit |

## Commands

| Command | Description |
|---------|-------------|
| `/analyze <topic>` | Systems thinking analysis |
| `/challenge <idea>` | Devil's advocate evaluation |
| `/research <topic>` | Systematic investigation |
| `/decide <options>` | Structured decision making |
| `/models` | List and select models |
| `/help` | Show all commands |

## Development

FlowState uses git worktrees for parallel development:

```bash
# Clone with worktree setup
git clone --bare git@github.com:baphled/flowstate.git FlowState.git
cd FlowState.git
git worktree add main main

# Create a feature branch
make worktree-new NAME=my-feature
```

### Testing

```bash
make test        # Run all tests
make bdd         # Run BDD tests
make bdd-smoke   # Run smoke tests
make check       # Full check (fmt, lint, test)
```

See [AGENTS.md](AGENTS.md) for AI development instructions.

## Documentation

- [Project Plan](docs/PLAN.md)
- [Architecture Overview](docs/architecture/OVERVIEW.md)
- [Development Rules](rules/)

## License

MIT
