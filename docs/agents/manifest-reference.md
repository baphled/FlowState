# Agent Manifest Reference

An agent manifest defines a FlowState agent's identity, capabilities, tools,
skills, context management, and system prompt. Manifests are discovered at
startup from `~/.config/flowstate/agents/` and support both JSON (`.json`) and
Markdown with YAML frontmatter (`.md`) formats.

## Format Choice

**Markdown (recommended):** The frontmatter contains the manifest fields as YAML;
the body after the closing `---` becomes the agent's system prompt. This is the
preferred format for all agents that ship with FlowState.

**JSON:** All fields including the system prompt are in a single JSON object.
The `instructions.system_prompt` field holds the agent's instructions.

## Discovery and Precedence

At startup, FlowState scans `~/.config/flowstate/agents/` for `.json` and `.md`
files. When both formats exist for the same agent ID, the Markdown definition
takes precedence. Invalid manifests are skipped with a warning logged.

Additional agent directories can be merged into the registry via
`DiscoverMerge()`, with the later directory having higher precedence for
duplicate IDs.

## Minimal Markdown Manifest

```markdown
---
schema_version: "1.0.0"
id: my-agent
name: My Agent
complexity: standard
metadata:
  role: My role description
  goal: What this agent does
  when_to_use: When to use it
capabilities:
  tools:
    - bash
    - file
  skills: []
  always_active_skills:
    - pre-action
  mcp_servers: []
context_management:
  max_recursion_depth: 2
  summary_tier: quick
  sliding_window_size: 10
  compaction_threshold: 0.75
  embedding_model: nomic-embed-text
delegation:
  can_delegate: false
---

# Role: My Agent

Your system prompt goes here. This becomes the agent's instructions.
```

## Complete Schema

### Top-Level Fields

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `schema_version` | `string` | No | Manifest schema version. Must not be blank if present. |
| `id` | `string` | Yes | Globally unique agent identifier. Derived from filename if omitted (e.g. `planner.md` → `planner`). |
| `name` | `string` | Yes | Human-readable display name. Derived from filename if omitted. |
| `color` | `string` | No | Hex colour code for UI rendering (e.g. `#FF5733`). Must be empty or a valid `#RRGGBB` format. |
| `complexity` | `string` | Yes | One of: `low`, `standard`, `deep`. Influences provider model selection and context budget. |
| `aliases` | `[]string` | No | Alternative names and keywords for agent routing. Defaults to `[]`. |
| `uses_recall` | `bool` | No | When `true`, the agent queries the RecallBroker during context assembly. Defaults to `false` — recall is opt-in. |
| `metadata` | `Metadata` | Yes | Descriptive information about the agent. |
| `capabilities` | `Capabilities` | Yes | Tools, skills, and MCP servers available to the agent. |
| `context_management` | `ContextManagement` | Yes (auto-filled) | Context window and compaction settings. Zero-valued fields inherit defaults. |
| `delegation` | `Delegation` | Yes | Delegation configuration for orchestrator agents. |
| `hooks` | `Hooks` | Yes | Pre and post execution hooks. |
| `instructions` | `Instructions` | No | System prompt configuration. For Markdown manifests, the body becomes `system_prompt`. |
| `harness_enabled` | `bool` | No | Legacy boolean. Superseded by `harness.enabled`. |
| `harness` | `*HarnessConfig` | No | Fine-grained output validation and quality layers. |
| `mode` | `string` | No | Harness loop type: `plan` (default) or `execution`. |
| `loop` | `*LoopConfig` | No | Delegation loop for coordinator agents (review-cycle mode). |
| `orchestrator_meta` | `OrchestratorMetadata` | Yes | How orchestrators should reference and invoke this agent. |

### Metadata

| Field | Type | Description |
|-------|------|-------------|
| `role` | `string` | Short role label (e.g. "Codebase Investigator"). |
| `goal` | `string` | What the agent does in one sentence. |
| `when_to_use` | `string` | When to delegate to this agent. |

### Capabilities

| Field | Type | Description |
|-------|------|-------------|
| `tools` | `[]string` | Tool names available to the agent (e.g. `bash`, `file`, `delegate`, `coordination_store`). **Agents with no tools will be silently stuck** — a warning is logged at load time. |
| `skills` | `[]string` | Skills available via `skill_load()`. |
| `always_active_skills` | `[]string` | Skills always injected into the system prompt. Loaded at startup via `skill_load()` before the agent begins work. |
| `mcp_servers` | `[]string` | MCP server names. Gates tools through the delegate engine by the manifest's MCP server list. |
| `capability_description` | `string` | One-line summary shown to orchestrators in delegation tables. |

### ContextManagement

| Field | Type | Default | Validation | Description |
|-------|------|---------|------------|-------------|
| `max_recursion_depth` | `int` | `2` | — | Maximum delegation nesting depth. |
| `summary_tier` | `string` | `"quick"` | — | Context summarisation level: `quick`, `medium`, `deep`. |
| `sliding_window_size` | `int` | `10` | — | Number of recent turns kept in the active window. |
| `compaction_threshold` | `float` | `0.75` | `(0.0, 1.0]` | Context utilisation threshold before compaction triggers. Zero means "inherit global". |
| `embedding_model` | `string` | `"nomic-embed-text"` | — | Embedding model for context summarisation. Must be consistent across shared Qdrant clusters. |

### Delegation

| Field | Type | Description |
|-------|------|-------------|
| `can_delegate` | `bool` | Whether the agent can delegate to other agents. |
| `delegation_allowlist` | `[]string` | Agent IDs this agent is permitted to delegate to. |

### Hooks

| Field | Type | Description |
|-------|------|-------------|
| `before` | `[]string` | Hook names to run before the agent executes. |
| `after` | `[]string` | Hook names to run after the agent completes. |

### Instructions

| Field | Type | Description |
|-------|------|-------------|
| `system_prompt` | `string` | The agent's system prompt. For Markdown manifests, populated from the body after frontmatter. |
| `structured_prompt_file` | `string` | Path to an external structured prompt file. |

### HarnessConfig

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | Whether harness validation is active for this agent. |
| `validators` | `[]string` | Validator names to apply. |
| `critic_enabled` | `bool` | Whether the adversarial critic runs on this agent's output. |
| `voting_enabled` | `bool` | Whether multi-agent voting is enabled. |
| `max_attempts` | `int` | Maximum harness retry attempts. |
| `waves` | `[]WaveStage` | Fan-in barrier stages for deterministic loop enforcement. |

### WaveStage

| Field | Type | Description |
|-------|------|-------------|
| `name` | `string` | Stage identifier (e.g. "evidence", "analysis"). |
| `description` | `string` | Human-readable stage description. |
| `expected_keys` | `[]string` | Coordination store keys that must be present before advancing. May contain `{chainID}` placeholders. |

### LoopConfig

| Field | Type | Description |
|-------|------|-------------|
| `enabled` | `bool` | Whether the delegation loop is active. |
| `writer` | `string` | Agent ID of the writer in the review cycle. |
| `reviewer` | `string` | Agent ID of the reviewer in the review cycle. |
| `max_attempts` | `int` | Maximum review-retry cycles. |
| `roles` | `map[string]string` | Role assignments for loop participants. |

### OrchestratorMetadata

| Field | Type | Description |
|-------|------|-------------|
| `cost` | `string` | Cost indicator: `FREE`, `CHEAP`, `MODERATE`, `EXPENSIVE`. |
| `category` | `string` | Agent category (e.g. `advisor`, `exploration`, `domain`). |
| `prompt_alias` | `string` | Short name used in orchestrator prompts. |
| `key_trigger` | `string` | One-line trigger heuristic for orchestrators. |
| `use_when` | `[]string` | Conditions under which to delegate to this agent. |
| `avoid_when` | `[]string` | Conditions under which NOT to delegate. |
| `triggers` | `[]DelegationTrigger` | Domain/trigger pairs for dynamic delegation table generation. |

### DelegationTrigger

| Field | Type | Description |
|-------|------|-------------|
| `domain` | `string` | Domain label (e.g. "Explore", "Plan"). |
| `trigger` | `string` | One-line description of when to fire. |

## Markdown-Specific Behaviour

When loading a Markdown manifest:

1. YAML frontmatter is extracted between the first pair of `---` delimiters
2. Frontmatter is unmarshalled into the `Manifest` struct
3. If `id` is empty, it is derived from the filename (without extension)
4. If `name` is empty, it is derived from the filename
5. The body text after frontmatter becomes `instructions.system_prompt`
6. Context management defaults are applied for zero-valued fields

A legacy Markdown format (with `description`, `default_skills`, `mode` fields
instead of the full `Manifest` schema) is also supported as a fallback.

## Defaults Applied at Load Time

The following defaults are populated when fields are zero-valued:

| Field | Default |
|-------|---------|
| `context_management.max_recursion_depth` | `2` |
| `context_management.summary_tier` | `"quick"` |
| `context_management.sliding_window_size` | `10` |
| `context_management.compaction_threshold` | `0.75` |
| `context_management.embedding_model` | `"nomic-embed-text"` |

## Validation Rules

- `id` must be non-empty (or derivable from filename for Markdown)
- `name` must be non-empty (or derivable from filename for Markdown)
- `schema_version` must not be blank if present
- `color` must be empty or a valid `#RRGGBB` hex colour
- `context_management.compaction_threshold` must be in `(0.0, 1.0]` or zero

## Adding a Custom Agent

1. Create a Markdown manifest in `~/.config/flowstate/agents/`:

```markdown
---
schema_version: "1.0.0"
id: data-analyst
name: Data Analyst
complexity: standard
metadata:
  role: Data Analyst
  goal: Analyse datasets and produce statistical insights
  when_to_use: When statistical analysis or data visualisation is needed
capabilities:
  tools:
    - bash
    - file
  skills: []
  always_active_skills:
    - pre-action
    - discipline
  mcp_servers: []
context_management:
  max_recursion_depth: 2
  summary_tier: medium
  sliding_window_size: 10
  compaction_threshold: 0.75
  embedding_model: nomic-embed-text
delegation:
  can_delegate: false
hooks:
  before: []
  after: []
orchestrator_meta:
  cost: MODERATE
  category: domain
  prompt_alias: Data Analyst
  key_trigger: "Statistical analysis needed → fire data-analyst"
  use_when:
    - Dataset analysis required
    - Statistical summaries needed
  avoid_when:
    - Simple arithmetic
    - Data entry tasks
  triggers:
    - domain: Analyse
      trigger: Perform statistical analysis on structured data
---

# Role: Data Analyst

You are a data analyst specialising in statistical analysis...
```

2. Restart FlowState. The agent will be auto-discovered.

3. Verify with:

```bash
# In chat, select the agent via Ctrl+A
# Or check the agent list in the TUI
```

## Embedding Model Consistency

If you share a Qdrant collection with other FlowState instances, all agents
must use the same `embedding_model`. The historical default is
`nomic-embed-text` (768-dim, Cosine distance). Override at the application
level via `SetDefaultEmbeddingModel()` at startup.
