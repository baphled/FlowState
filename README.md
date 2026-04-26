# FlowState

A general-purpose AI assistant TUI for everyday tasks.

FlowState brings the power of AI-assisted workflows to your terminal - not just for coding, but for research, analysis, decision-making, and any domain where AI can help.

## Features

- **Ollama-first** - Local models as first-class citizens.
- **Provider-agnostic** - Plug in any model provider (OpenAI, Anthropic, etc.).
- **MCP integration** - Connect to external memory, RAG, and tools via Model Context Protocol.
- **Vector-backed Recall** - Optional Qdrant integration for semantic memory and learning.
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

## Context Compression

FlowState ships a three-layer compression pipeline that keeps long-running sessions inside provider token budgets without mutating the canonical transcript. Every layer is opt-in, and each can be enabled independently via the `compression:` block in `config.yaml`.

- **Layer 1 — Micro-compaction** (`micro_compaction`) replaces older tool-heavy units with short placeholders while keeping the recent "hot tail" verbatim. Spilled payloads land under `~/.flowstate/compacted/{session-id}/` and can be rehydrated on demand.
- **Layer 2 — Auto-compaction** (`auto_compaction`) fires when recent-message tokens exceed the configured fraction of the model context window. A summariser produces a structured `CompactionSummary` (intent, decisions, next steps, files-to-restore) that is injected as a single assistant message in place of the cold range. Compaction is strictly view-only: `session.Messages` is never mutated.
- **Layer 3 — Session memory** (`session_memory`) distils facts, conventions, and preferences from the transcript into a per-session knowledge store under `~/.flowstate/session-memory/{session-id}/`. Extraction runs asynchronously after each stream completes; retrieval surfaces the top-relevance entries as a prefix block in subsequent windows.

Compaction honours two ADRs:
- **View-Only Context Compaction** — artefacts are parallel state, never rewrites of the canonical transcript.
- **Tool-Call Atomicity in Context Compaction** — compaction operates on tool-use/tool-result pairs as atomic units, and summary output is scrubbed for raw provider identifiers (`toolu_…`, `call_…`) before injection.

### Example configuration

```yaml
compression:
  micro_compaction:
    enabled: true
    # Number of most-recent units kept verbatim; must be >= 1 when
    # micro-compaction is enabled.
    hot_tail_size: 5
    # Minimum token count for a message to become a micro-compaction
    # candidate. Below this threshold compaction never fires, so
    # trivial chat sessions are left untouched.
    token_threshold: 1000
    storage_dir: ~/.flowstate/compacted
    # Token budget assumed for the pointer placeholder that replaces
    # an offloaded message.
    placeholder_tokens: 50
  auto_compaction:
    enabled: true
    # Fraction of the model's context window at which compaction
    # fires. Must lie in the (0.0, 1.0] interval; values outside are
    # rejected at config load. A per-agent override is available via
    # the agent manifest's `context_management.compaction_threshold`
    # field: when non-zero it wins over this global. Precedence is
    # manifest > global > 0 (disabled). The same (0, 1] range is
    # enforced at manifest load.
    threshold: 0.75
  session_memory:
    enabled: true
    storage_dir: ~/.flowstate/session-memory
    # Mandatory when session_memory.enabled is true. The knowledge
    # extractor's chat request requires a model identifier; Ollama
    # and OpenAI-compatible backends reject an empty `model` with
    # HTTP 400, so config load fails loud if this key is missing.
    model: llama3.1
    # Bounds the pre-exit wait for the L3 knowledge-extraction
    # goroutine on `flowstate run`. Defaults to 35s (30s extractor
    # timeout + 5s atomic-write margin). Must be > 0 when
    # session_memory.enabled is true.
    wait_timeout: 35s
```

### Metrics and telemetry

Compression activity is observable through three channels:

- `slog.Info("compression metrics", ...)` emits `micro_compaction_count`, `auto_compaction_count`, `tokens_saved`, and `compression_overhead_tokens` for every assembled window.
- A successful L2 compaction publishes a `context.compacted` event on the engine bus with original/summary token counts and latency.
- Prometheus counters and gauges surface through the `flowstate serve` `/metrics` endpoint:
  - `flowstate_compression_tokens_saved_total` — cumulative tokens eliminated by L2, incremented only on net-saving compactions.
  - `flowstate_compression_overhead_tokens_total` — cumulative absolute tokens added by L2 when the summary scaffold exceeded the compacted range. Paired with the saved counter so a flat tokens-saved value can be disambiguated between "layer disabled" and "every pass produced overhead".
  - `flowstate_context_window_tokens` — gauge of the most recently assembled window size per agent.

The Prometheus counters are registered per-engine, so the `/metrics` endpoint reflects the `flowstate serve` engine only. Ephemeral `flowstate run` invocations are separate processes with their own Prometheus registry and do not feed these counters. For ad-hoc per-turn visibility on the CLI path use `flowstate run --stats`, which prints a one-line summary to stderr:

```
compression: micro=N auto=N tokens_saved=N overhead=N
```

`--stats` writes to stderr so it composes cleanly with `--json` on stdout. A compacted-view cache-hit counter is deliberately out of scope for this delivery; see ADR - View-Only Context Compaction §3 ("Caching Is a Permitted Extension") for the deferred design.

### When compression pays off

Compression savings are asymptotic. With the default `token_threshold: 1000`, micro-compaction never fires on trivial chat — candidate messages need to exceed roughly a screenful of text before the heuristic considers offloading them. The useful win is on long sessions with substantial per-message content (large tool outputs, file reads, retrieval payloads) where the hot tail stays small but the cold range grows without bound. Short conversations should expect zero observable savings.

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

The codebase uses Ginkgo v2 + Gomega for almost all tests, with one
`Describe` block per file. **Two exceptions** — these stay in
plain `*testing.T` form on purpose:

- `tools/analyzers/docblocks/analyzer_test.go`
- `tools/analyzers/gatingdrift/analyzer_test.go`

Both drive `analysistest.Run` from `golang.org/x/tools/go/analysis`,
the upstream-recommended way to test `go/analysis` analyzers. The
harness is `*testing.T`-shaped by design and emits per-fact diagnostic
positions that get lost when wrapped in `It(...)`. Do not convert
these to Ginkgo. See `DEFERRED.md` for the full rationale.

See [AGENTS.md](AGENTS.md) for AI development instructions.

## Documentation

- [Project Plan](docs/PLAN.md)
- [Architecture Overview](docs/architecture/OVERVIEW.md)
- [Demo Walkthrough](docs/DEMO.md)
- [Development Rules](rules/)

## License

MIT
