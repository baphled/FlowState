# FlowState

A general-purpose AI assistant TUI for everyday tasks.

FlowState brings the power of AI-assisted workflows to your terminal - not just for coding, but for research, analysis, decision-making, and any domain where AI can help.

## Features

- **Ollama-first** - Local models as first-class citizens.
- **Provider-agnostic** - Plug in any model provider (OpenAI, Anthropic, etc.).
- **MCP integration** - Connect to external memory, RAG, and tools via Model Context Protocol.
- **Session management** - Persistent conversations with search.
- **Tool system** - Bash, file operations, web fetching with granular permissions.
- **Extensible skill and command system** - Add custom commands and integrate with your workflows.
- **Local-first** - Optional local memory server with user control.

## Installation

Install the latest version using Go:

```bash
go install github.com/baphled/flowstate/cmd/flowstate@latest
```

Or build from source:

```bash
git clone https://github.com/baphled/flowstate.git
cd flowstate
make build
```

## Configuration

FlowState follows the XDG Base Directory Specification. It searches for configuration in:
1. `$XDG_CONFIG_HOME/flowstate/config.yaml`
2. `~/.config/flowstate/config.yaml` (default fallback)

### Example `config.yaml`

```yaml
providers:
  default: "ollama"
  ollama:
    host: "http://localhost:11434"
    model: "llama3.2"
  openai:
    api_key: "your-api-key"
    model: "gpt-4o"
  anthropic:
    api_key: "your-api-key"
    model: "claude-3-5-sonnet-20240620"

mcp_servers:
  - name: "filesystem"
    command: "npx"
    args: ["-y", "@modelcontextprotocol/server-filesystem", "/path/to/allowed/dir"]
    enabled: true
  - name: "memory"
    command: "flowstate-memory-server"
    enabled: false

always_active_skills:
  - "pre-action"
  - "memory-keeper"
```

## Quick Start

1. **Install FlowState** as described above.
2. **Configure your provider** (Ollama is the default if running locally).
3. **Launch the TUI**:
   ```bash
   flowstate chat
   ```
4. **Interact**:
   - Type your message and press `Enter` to send.
   - Use arrow keys or `PgUp`/`PgDn` to scroll through chat history.
   - Press `Ctrl+C` to quit.

For a full walkthrough, see the [Demo Guide](docs/DEMO.md).

## MCP Integration

FlowState natively supports the [Model Context Protocol (MCP)](https://modelcontextprotocol.io). This allows the AI to use external tools, access resources, and interact with your filesystem or other services.

Configure MCP servers in your `config.yaml` under the `mcp_servers` section. Each server requires a `name` and a `command`. FlowState currently supports the `stdio` transport.

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `↑/↓`, `PgUp/PgDn` | Scroll through chat history |
| `Ctrl+C` | Quit |

## Commands

| Command | Description |
|---------|-------------|
| `flowstate analyze` | Systems thinking analysis |
| `flowstate challenge` | Devil's advocate evaluation |
| `flowstate research` | Systematic investigation |
| `flowstate decide` | Structured decision making |
| `flowstate models` | List available models from all configured providers |
| `flowstate help` | Show all available commands |

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
- [Demo Walkthrough](docs/DEMO.md)
- [Development Rules](rules/)

## License

MIT
