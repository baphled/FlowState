# Getting Started with Agents

This guide covers how to configure, customise, and extend FlowState's agent
registry.

## Agent Discovery

FlowState discovers agents at startup by scanning `~/.config/flowstate/agents/`
for `.json` and `.md` files. Each file is parsed as an agent manifest. Invalid
manifests are skipped with a warning.

### Discovery Order

1. Embedded agent manifests (shipped in the binary) are seeded to disk
2. On-disk manifests in `~/.config/flowstate/agents/` are loaded
3. When both `.md` and `.json` exist for the same ID, `.md` takes precedence
4. Additional agent directories from `config.yaml` (`agent_dirs`) are merged

### Where Agents Live

```
~/.config/flowstate/
├── agents/
│   ├── planner.md              # Built-in planner agent
│   ├── explorer.md             # Built-in codebase explorer
│   ├── Senior-Engineer.md      # Built-in senior engineer
│   └── my-custom-agent.md      # Your custom agent
├── swarms/                     # Swarm manifests
├── skills/                     # Custom skills
├── gates/                      # Custom gates
└── config.yaml                 # Application config
```

## The Agent Picker

In the TUI, press `Ctrl+A` to open the agent picker. It lists all registered
agents and swarms, resolved by ID, name, and aliases. Select an agent to make
it the active agent for subsequent messages.

### Agent Resolution

When you select or mention an agent, FlowState resolves it in this order:

1. Exact ID match (case-sensitive)
2. Case-insensitive ID match
3. Case-insensitive alias match (against `Aliases` in the manifest)

This means you can refer to an agent by any of its aliases. For example, the
`planner` agent has aliases `["planning", "orchestration", "coordinator"]`, so
all of these resolve to it.

## Built-in Agents

FlowState ships with ~30 built-in agents covering roles from general assistant
to specialist engineers. The key agents include:

| Agent | Role | Delegation |
|-------|------|------------|
| `default-assistant` | General-purpose assistant | No |
| `planner` | Orchestrates the deterministic planning loop | Yes |
| `explorer` | Codebase investigation and pattern discovery | No |
| `librarian` | External documentation and reference research | No |
| `analyst` | Synthesises evidence into strategic analysis | No |
| `plan-writer` | Produces structured implementation plans | No |
| `plan-reviewer` | Evaluates plans against requirements | No |
| `executor` | Implements plans and executes tasks | No |
| `Senior-Engineer` | Senior-level code implementation | No |
| `Security-Engineer` | Security-focused code review | No |
| `QA-Engineer` | Quality assurance and test generation | No |
| `Code-Reviewer` | Code review and style enforcement | No |

Full list: see `internal/app/agents/` in the repository.

## Creating a Custom Agent

### Step 1: Create the Manifest

Create a Markdown file in `~/.config/flowstate/agents/`:

```bash
touch ~/.config/flowstate/agents/my-agent.md
```

The filename (without `.md`) becomes the agent ID if you omit `id` in the
frontmatter.

### Step 2: Write the Frontmatter

Every agent manifest must have:
- `schema_version`, `id`, `name`, `complexity`
- `metadata` block with `role`, `goal`, `when_to_use`
- `capabilities` block with at least `tools` (otherwise the agent will be stuck)
- `context_management` block (defaults are applied for missing fields)
- `delegation` block
- `orchestrator_meta` block

See the [Manifest Reference](manifest-reference.md) for the complete schema.

### Step 3: Write the System Prompt

Everything after the closing `---` becomes the agent's system prompt. This is
where you define the agent's behaviour, constraints, and output format.

### Step 4: Restart FlowState

Agent discovery happens at startup. Restart FlowState to pick up new agents.

## Agent Capabilities

### Tools

Tools are the agent's interface with the outside world. Available tools include:

| Tool | Description |
|------|-------------|
| `bash` | Execute shell commands |
| `file` | Read, write, and search files |
| `delegate` | Delegate tasks to other agents |
| `coordination_store` | Read/write from the coordination store |
| `skill_load` | Load skill documentation at runtime |
| `todowrite` | Create and manage task lists |
| `plan_list` | List saved plans |
| `plan_read` | Read a saved plan by ID |

### Skills

Skills are structured knowledge files (SKILL.md) that provide the agent with
domain-specific instructions. Skills are loaded via `skill_load()` or
automatically injected when listed in `always_active_skills`.

Common skills include:
- `pre-action` — Think before acting
- `memory-keeper` — Track conversation context
- `discipline` — Maintain output quality standards
- `research` — Research methodology
- `critical-thinking` — Analytical reasoning

### Always-Active Skills

Skills in `always_active_skills` are:
1. Loaded at startup via `skill_load()`
2. Their instructions prepended to the agent's system prompt
3. Available throughout the conversation without explicit loading

## Agent Complexity

The `complexity` field influences provider model selection:

| Complexity | Model Tier | Use Case |
|------------|-----------|----------|
| `low` | Fast, cheaper models | Simple tasks, search, classification |
| `standard` | Mid-range models | General assistance, code review |
| `deep` | Premium models | Planning, analysis, multi-step reasoning |

## Delegation

Agents with `can_delegate: true` can delegate tasks to other agents. The
`delegation_allowlist` restricts which agents they can delegate to.

Example:

```yaml
delegation:
  can_delegate: true
  delegation_allowlist:
    - explorer
    - librarian
    - analyst
```

When delegating, the engine:
1. Creates a sub-engine with the target agent's manifest
2. Injects the target's system prompt
3. Streams responses back to the delegating agent
4. Enforces tool capability gates from the delegate's manifest

## Troubleshooting

### Agent Not Appearing in Picker

- Verify the manifest file is in `~/.config/flowstate/agents/`
- Check the file extension is `.md` or `.json`
- Restart FlowState
- Check logs for "skipping agent manifest" warnings

### Agent Has No Tools

A warning is logged at startup:

```
WARN agent manifest has no capabilities.tools; agent will have no tools available beyond suggest_delegate path=... agent_id=...
```

Add at least one tool to `capabilities.tools`.

### Frontmatter Parse Error

Common causes:
- Missing closing `---` delimiter
- Invalid YAML indentation
- Using tabs instead of spaces
