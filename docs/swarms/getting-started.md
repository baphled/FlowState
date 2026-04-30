# Getting Started with Swarms

This guide walks you through setting up, installing, and running your first
swarm in FlowState.

## Prerequisites

- FlowState built and installed (`make build && make install`)
- At least one provider configured in `~/.config/flowstate/config.yaml`
- Python 3.9+ available in `PATH` (required for external gates)
- Ollama running with `nomic-embed-text` model (optional — only needed for vector-backed recall)

## Directory Layout

FlowState uses XDG directories for configuration:

```
~/.config/flowstate/
├── config.yaml          # Provider credentials, skill_dir, qdrant URL
├── agents/              # Agent manifests (JSON or Markdown)
├── swarms/              # Swarm manifests (YAML)
├── schemas/             # JSON schemas for result-schema gates
├── gates/               # External gate executables
└── skills/              # Custom skill directories
```

## Installing Your First Swarm

The repo ships several example swarms under `examples/swarms/`. The simplest
is the **A-Team**, a versatile generalist swarm with five members and a
relevance gate.

```bash
# Copy swarm manifests
cp -r examples/swarms/a-team/swarms/* ~/.config/flowstate/swarms/

# Copy agent manifests
cp -r examples/swarms/a-team/agents/* ~/.config/flowstate/agents/

# Copy custom skills
cp -r examples/swarms/a-team/skills/* ~/.config/flowstate/skills/

# Copy the relevance gate
cp -r examples/gates/relevance-gate ~/.config/flowstate/gates/
```

No refresh command is needed — FlowState discovers agent and swarm manifests
from disk at startup.

## Validating the Swarm

Before running, validate that the swarm resolves correctly:

```bash
flowstate swarm validate a-team
```

A successful validation produces no output. If there are issues, you will see
specific error messages such as:

```
a-team: lead "coordinator" not found in agent or swarm registry
a-team: member "researcher" not found in agent or swarm registry
```

Common causes:
- Agent manifests were not copied to `~/.config/flowstate/agents/`
- Gate executable is not marked as executable (`chmod +x`)
- FlowState needs restarting after adding new manifests (discovery happens at startup)

## How to Trigger a Swarm

Swarms can be triggered from two entry points: the TUI chat and the CLI.

### From the TUI Chat (Recommended)

Start FlowState and mention the swarm with an `@` prefix in the chat input:

```
@a-team research the latest developments in quantum error correction
```

The chat intent's `@`-mention resolver identifies `a-team` as a registered swarm,
resolves it to the lead agent (`coordinator`), and starts the swarm engine. The
swarm activity timeline in the secondary pane shows delegation, tool-call, and
gate events in real-time.

Toggle the swarm activity pane with `Ctrl+T`. Press `Ctrl+E` for detailed event
information.

You can also switch to a swarm as your active agent mid-session using `Ctrl+A`
to open the agent picker, which lists both agents and swarms.

### From the CLI

Use the `flowstate run` command with `--agent`:

```bash
flowstate run --agent a-team "research the latest developments in quantum error correction"
```

The `--agent` flag accepts either an agent ID or a swarm ID. When given a swarm
ID, FlowState resolves it to the lead agent and starts the swarm engine with the
appropriate context.

### What NOT to Use

The `flowstate swarm run` command exists but is currently a stub. It will print
an error message indicating it is not yet implemented. Use `flowstate run --agent`
instead.

## Running Your First Swarm

## What Happens During Execution

1. **Lead starts** — The `coordinator` agent receives an augmented system
   prompt containing the swarm ID, member roster, and delegation instructions.

2. **Task plan written** — The coordinator classifies the task using the
   `dynamic-routing` skill and writes a routing plan to the coordination store
   at `a-team/{chainID}/task-plan`. This is an agent behaviour, not a
   framework action — the framework only provides the coordination store
   tool that makes it possible.

3. **Members execute** — The coordinator delegates to members in sequence:
   - `researcher` gathers information (validated by `relevance-gate`)
   - `strategist` develops a strategy from findings
   - `critic` adversarially reviews the strategy
   - `writer` produces the final output
   - `executor` acts on the plan (for `action-required` tasks)

4. **Output returned** — After the final member completes, the coordinator
   reads the final output from the coordination store and delivers it.

## Customising the A-Team

### Changing the Routing

The coordinator uses the `dynamic-routing` skill to classify tasks. Edit
`~/.config/flowstate/skills/dynamic-routing/SKILL.md` to adjust the heuristics
that determine which pipeline runs.

### Adding a Member

1. Create an agent manifest in `~/.config/flowstate/agents/`
2. Add the agent ID to the `members` list in `~/.config/flowstate/swarms/a-team.yml`
3. Add the agent ID to the `delegation_allowlist` in the coordinator's manifest
4. Restart FlowState so the new agent is discovered

### Adding a Gate

Create a gate executable (Python, bash, or any language) that reads JSON from
stdin and writes `{pass: bool, reason: string}` to stdout. See the
[Gates guide](gates.md) for details.

## Installing Other Example Swarms

### Engineering (Swarm-of-Swarms)

```bash
cp -r examples/swarms/engineering/* ~/.config/flowstate/swarms/
cp -r examples/gates/ci-gate examples/gates/integration-gate examples/gates/quality-gate \
    ~/.config/flowstate/gates/
flowstate swarm validate engineering
```

The engineering swarm uses only built-in FlowState agents — no custom agent
files required. It coordinates three sub-swarms: planning, implementation, and
quality assurance.

### Adulting (Life-Admin)

```bash
cp -r examples/swarms/adulting/adulting.yml ~/.config/flowstate/swarms/
cp -r examples/swarms/adulting/agents/* ~/.config/flowstate/agents/
cp -r examples/gates/admin-item-validator examples/gates/personal-data-security \
    ~/.config/flowstate/gates/
```

### Board Room (Pitch Committee)

```bash
cp -r examples/swarms/board-room/swarms/* ~/.config/flowstate/swarms/
cp -r examples/swarms/board-room/agents/* ~/.config/flowstate/agents/
cp -r examples/swarms/board-room/skills/* ~/.config/flowstate/skills/
cp -r examples/gates/quorum-gate ~/.config/flowstate/gates/
```

### EHCP Annual Review

```bash
cp -r examples/swarms/ehcp/swarms/* ~/.config/flowstate/swarms/
cp -r examples/swarms/ehcp/agents/* ~/.config/flowstate/agents/
cp -r examples/swarms/ehcp/skills/* ~/.config/flowstate/skills/
cp -r examples/gates/ehcp-completeness examples/gates/personal-data-security \
    ~/.config/flowstate/gates/
```

## Listing Available Swarms

```bash
flowstate swarm list
```

This shows all registered swarms with their ID, lead, member count, and
gate count in tabular form.

## Troubleshooting

### "no agent or swarm named 'X'"

The `@`-mention resolver could not find the swarm. Check:

```bash
# Verify manifest is on disk
ls ~/.config/flowstate/swarms/*.yml

# Validate the specific swarm
flowstate swarm validate <id>

# Check for cross-registry collisions
flowstate swarm list
```

### Gate Not Found

If a gate fails with `ext:<name>` not found:

```bash
# Verify gate directory exists
ls ~/.config/flowstate/gates/<name>/

# Verify gate executable is present and executable
ls -la ~/.config/flowstate/gates/<name>/gate.py
chmod +x ~/.config/flowstate/gates/<name>/gate.py

# Test the gate directly
echo '{"kind":"<name>","payload":{}}' | ~/.config/flowstate/gates/<name>/gate.py
```

### Swarm Hangs

If a swarm appears stuck:

- Check that the provider is configured and reachable
- Ensure the lead agent has `can_delegate: true` in its manifest (this wires the `delegate` tool)
- Verify the coordination store is wired (check `flowstate` logs for coord-store warnings)
- For parallel swarms, check that `max_parallel` does not exceed provider limits
