# Creating Custom Agents

This guide explains how to create a custom agent for FlowState. An agent is defined by a JSON manifest file placed in the configured agent directory. The runtime discovers agents automatically on startup.

## Quick Start

Copy an existing manifest and adapt it. The Explorer agent is a good starting point for read-only investigative agents.

## Manifest Structure

A complete manifest has the following top-level fields:

```json
{
  "schema_version": "1.0.0",
  "id": "my-agent",
  "name": "My Agent",
  "complexity": "medium",
  "model_preferences": { ... },
  "capabilities": { ... },
  "context_management": { ... },
  "delegation": { ... },
  "hooks": { ... },
  "instructions": { ... },
  "metadata": { ... }
}
```

### Field reference

| Field                | Type   | Required | Description                                                   |
|----------------------|--------|----------|---------------------------------------------------------------|
| `schema_version`     | string | yes      | Must be `"1.0.0"`                                             |
| `id`                 | string | yes      | Unique identifier used in delegation tables and routing       |
| `name`               | string | yes      | Human-readable display name                                   |
| `complexity`         | string | yes      | `"light"`, `"medium"`, or `"deep"` — controls context budget |
| `model_preferences`  | object | yes      | Per-provider model selection (see below)                      |
| `capabilities`       | object | yes      | Tools, skills, and capability description (see below)         |
| `context_management` | object | no       | Sliding window, compaction, and embedding settings            |
| `harness_enabled`    | bool   | no       | Set `true` to enable the Plan Writer harness                  |
| `delegation`         | object | yes      | Whether this agent may delegate and to whom                   |
| `hooks`              | object | no       | Before/after hook lists (usually empty arrays)                |
| `instructions`       | object | yes      | System prompt and structured prompt file reference            |
| `metadata`           | object | no       | Role, goal, and when-to-use hints for discovery               |

## Model Preferences

Each key is a provider name. The runtime tries providers in the order: `anthropic → ollama → openai`.

```json
"model_preferences": {
  "anthropic": [
    { "provider": "anthropic", "model": "claude-sonnet-4-6" }
  ],
  "ollama": [
    { "provider": "ollama", "model": "llama3.2" }
  ],
  "openai": [
    { "provider": "openai", "model": "gpt-4o" }
  ]
}
```

If a provider is unavailable or the model is not found, the runtime automatically falls back to the next provider in the chain.

## Capabilities

```json
"capabilities": {
  "tools": ["bash", "file", "coordination_store"],
  "skills": ["research", "critical-thinking"],
  "always_active_skills": ["pre-action", "memory-keeper"],
  "mcp_servers": [],
  "capability_description": "One sentence describing what this agent does"
}
```

### Available tools

| Tool name           | Description                                                   |
|---------------------|---------------------------------------------------------------|
| `bash`              | Execute shell commands                                        |
| `file`              | Read and write files                                          |
| `web`               | Fetch URLs and search the web                                 |
| `skill_load`        | Load a skill's SKILL.md content at runtime                    |
| `delegate`          | Delegate sub-tasks to other agents (coordinator only)         |
| `coordination_store`| Read and write entries in the shared coordination store       |

### capability_description

This field is shown to the coordinator when it selects which agent to delegate to. Write a single, precise sentence. Compare the existing agents:

- Explorer: `"Explores codebase to find patterns, structures, conventions, and understand existing code organisation"`
- Librarian: `"Searches official documentation, library best practices, and external references for accurate technical information"`
- Analyst: `"Synthesises research findings into structured evidence dossiers with critical analysis and system-level thinking"`

## Delegation

Agents that should not delegate tasks set `can_delegate` to `false` with an empty table:

```json
"delegation": {
  "can_delegate": false,
  "delegation_table": {}
}
```

A coordinator that orchestrates other agents sets `can_delegate` to `true` and lists its targets:

```json
"delegation": {
  "can_delegate": true,
  "delegation_table": {
    "explorer":     "explorer",
    "plan-writer":  "plan-writer"
  }
}
```

The keys are logical role names used in prompts; the values are agent IDs that match the `id` field of the target manifest.

## Context Management

```json
"context_management": {
  "max_recursion_depth": 2,
  "summary_tier": "medium",
  "sliding_window_size": 10,
  "compaction_threshold": 0.75,
  "embedding_model": "nomic-embed-text"
}
```

| Field                  | Recommended value | Notes                                             |
|------------------------|-------------------|---------------------------------------------------|
| `max_recursion_depth`  | `2`               | `3` for coordinators; lower to reduce token usage |
| `summary_tier`         | `"medium"`        | Match `complexity`; `"deep"` for plan-generating  |
| `sliding_window_size`  | `10`              | Number of messages to keep in context             |
| `compaction_threshold` | `0.75`            | Compact when context reaches 75% capacity         |
| `embedding_model`      | `"nomic-embed-text"` | Used for semantic similarity in discovery      |

## Complete Example: Custom Research Agent

This manifest follows the Explorer pattern and adds web search capability.

```json
{
  "schema_version": "1.0.0",
  "id": "security-researcher",
  "name": "Security Researcher",
  "complexity": "medium",
  "model_preferences": {
    "anthropic": [
      { "provider": "anthropic", "model": "claude-sonnet-4-6" }
    ],
    "ollama": [
      { "provider": "ollama", "model": "llama3.2" }
    ]
  },
  "capabilities": {
    "tools": [
      "bash",
      "file",
      "web",
      "coordination_store"
    ],
    "skills": [
      "research",
      "critical-thinking",
      "cyber-security"
    ],
    "always_active_skills": [
      "pre-action",
      "memory-keeper"
    ],
    "mcp_servers": [],
    "capability_description": "Investigates security vulnerabilities, CVEs, and dependency risks in codebases and external packages"
  },
  "context_management": {
    "max_recursion_depth": 2,
    "summary_tier": "medium",
    "sliding_window_size": 10,
    "compaction_threshold": 0.75,
    "embedding_model": "nomic-embed-text"
  },
  "delegation": {
    "can_delegate": false,
    "delegation_table": {}
  },
  "hooks": {
    "before": [],
    "after": []
  },
  "instructions": {
    "system_prompt": "You are the FlowState Security Researcher.",
    "structured_prompt_file": "security-researcher"
  },
  "metadata": {
    "role": "Security Investigator",
    "goal": "Identify security risks, CVEs, and insecure patterns in the target codebase or dependency tree",
    "when_to_use": "When investigating the security posture of a codebase or evaluating a dependency"
  }
}
```

## Adding the Agent to the Coordinator

To make the Planning Coordinator delegate to your new agent, add it to the coordinator's `delegation_table` in `agents/planning-coordinator.json`:

```json
"delegation_table": {
  "explorer":            "explorer",
  "librarian":           "librarian",
  "analyst":             "analyst",
  "plan-writer":         "plan-writer",
  "plan-reviewer":       "plan-reviewer",
  "security-researcher": "security-researcher"
}
```

The key you choose here is the logical name the coordinator uses in its prompt when deciding who to call.

## Agent Discovery

The runtime calls `agent.Registry.Discover(agentDir)` on startup, which reads all `.json` files in the configured agent directory. Place your manifest in that directory and restart the server — no code changes are required.

To verify discovery:

```bash
curl http://localhost:8080/api/agents | jq '.[].id'
```

## Related Documents

- [PLANNING_LOOP.md](./PLANNING_LOOP.md) — how coordinators, writers, and reviewers interact
- [DELEGATION.md](./DELEGATION.md) — the Handoff struct and async delegation runtime
- [RESEARCH_AGENTS.md](./RESEARCH_AGENTS.md) — Explorer, Librarian, and Analyst in detail
