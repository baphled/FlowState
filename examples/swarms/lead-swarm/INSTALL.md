# Lead Swarm Example

This directory contains a complete example of an adaptive orchestrator swarm using FlowState's swarm engine.

## What is the Lead Swarm?

The **Lead Swarm** is a flexible orchestrator that:
1. **Accepts any goal** — No rigid workflow constraints
2. **Deconstructs tasks** — Classifies by type, complexity, and required expertise
3. **Selects optimal agents** — Dynamically chooses which sub-swarms to deploy
4. **Coordinates parallel execution** — Multiple perspectives run simultaneously
5. **Synthesizes results** — Merges outputs into cohesive recommendations

## Quick Start

The `lead-swarm` references three sub-swarms (`a-team`, `board-room`,
`engineering-review`) and the `lead-coordinator` agent. You must install all
sub-swarm manifests, their member agents, and any supporting skills/gates
before the swarm registry will validate cleanly. The agents the engineering
sub-swarms reference (`planner`, `executor`, `Senior-Engineer`,
`plan-reviewer`, `QA-Engineer`, `Code-Reviewer`, `Security-Engineer`,
`explorer`, `librarian`, `analyst`, `plan-writer`) are bundled with FlowState
as embedded agents and require no copying.

### 1. Copy the lead coordinator agent

```bash
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/lead-swarm/lead-coordinator.md \
   ~/.config/flowstate/agents/lead-coordinator.md
```

### 2. Copy the a-team sub-swarm

```bash
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/a-team/swarms/a-team.yml \
   ~/.config/flowstate/swarms/a-team.yml

cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/a-team/agents/*.md \
   ~/.config/flowstate/agents/

mkdir -p ~/.config/flowstate/skills
cp -R /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/a-team/skills/* \
   ~/.config/flowstate/skills/
```

The a-team manifest declares members `coordinator`, `researcher`, `strategist`,
`critic`, `writer`, plus the embedded `executor` agent.

### 3. Copy the board-room sub-swarm

```bash
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/board-room/swarms/board-room.yml \
   ~/.config/flowstate/swarms/board-room.yml

cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/board-room/agents/*.md \
   ~/.config/flowstate/agents/

cp -R /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/board-room/skills/* \
   ~/.config/flowstate/skills/

mkdir -p ~/.config/flowstate/gates/board-room
cp -R /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/board-room/gates/* \
   ~/.config/flowstate/gates/board-room/
chmod +x ~/.config/flowstate/gates/board-room/*
```

The board-room manifest declares members `chair`, `bull-analyst`, `bear-analyst`,
`market-analyst`, `financial-analyst`, `technical-analyst`.

### 4. Copy the engineering sub-swarms

`engineering-review` composes three nested sub-swarms — `engineering-planning`,
`engineering-implementation`, `engineering-quality` — so all five engineering
manifests must be installed together. (`engineering.yml` is an alternative
top-level alias that composes the same three nested swarms.)

```bash
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/engineering/engineering-planning.yml \
   ~/.config/flowstate/swarms/engineering-planning.yml
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/engineering/engineering-implementation.yml \
   ~/.config/flowstate/swarms/engineering-implementation.yml
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/engineering/engineering-quality.yml \
   ~/.config/flowstate/swarms/engineering-quality.yml
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/engineering/engineering-review.yml \
   ~/.config/flowstate/swarms/engineering-review.yml
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/engineering/engineering.yml \
   ~/.config/flowstate/swarms/engineering.yml
```

### 5. Copy the lead swarm manifest

```bash
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/lead-swarm/lead-swarm.yml \
   ~/.config/flowstate/swarms/lead-swarm.yml
```

### 6. Register schemas

```bash
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/lead-swarm/schemas/*.json \
   ~/.config/flowstate/schemas/
```

### 7. Install lead-swarm gates (optional)

```bash
mkdir -p ~/.config/flowstate/gates/lead-swarm
cp /home/baphled/Projects/FlowState.git/agent-platform/examples/swarms/lead-swarm/gates/* \
   ~/.config/flowstate/gates/lead-swarm/
chmod +x ~/.config/flowstate/gates/lead-swarm/*
```

### 8. Refresh FlowState

```bash
flowstate agents refresh
flowstate swarms refresh
```

### 9. Test it

```bash
# Simple task
flowstate run --agent lead-coordinator "Fix the typo in README.md"

# Complex feature
flowstate run --agent lead-coordinator "Implement OAuth2 with security review"

# Strategic decision
flowstate run --agent lead-coordinator "Should we migrate to gRPC? Evaluate options."
```

## Architecture

```
lead-swarm (Top-level orchestrator)
├── lead-coordinator (Lead agent)
├── a-team (Sub-swarm: Tactical execution)
│   ├── coordinator
│   ├── researcher
│   ├── strategist
│   ├── critic
│   └── writer
├── board-room (Sub-swarm: Strategic review)
│   ├── chair
│   ├── bull-analyst
│   ├── bear-analyst
│   ├── market-analyst
│   ├── financial-analyst
│   └── technical-analyst
└── engineering-review (Sub-swarm: Full engineering lifecycle)
    ├── planner
    ├── engineering-planning
    ├── engineering-implementation
    ├── Senior-Engineer
    ├── executor
    ├── plan-reviewer
    ├── engineering-quality
    └── ...
```

## Execution Flow

### Phase 1: Task Analysis
**Lead Coordinator** classifies the goal:
- Task type (bug, feature, decision, research)
- Complexity (simple, moderate, complex, major)
- Required expertise (security, testing, architecture)
- Scope (single file vs cross-system vs new project)

### Phase 2: Sub-Swarm Selection
Based on analysis, **Coordinator** selects:

| Task Type | Sub-Swarms Dispatched |
|-----------|----------------------|
| Simple bug fix | `a-team` only |
| Feature implementation | `a-team` OR `engineering-review` |
| Security-sensitive | `board-room` + `a-team` (parallel) |
| Architecture decision | `board-room` only |
| Major feature | All three (parallel) |
| Research/investigation | `board-room` only |
| Quality audit | `engineering-review` + `a-team` (parallel) |

### Phase 3: Parallel Execution
Selected sub-swarms run in parallel, each producing their output to the coordination store:
- `lead-swarm/a-team/output`
- `lead-swarm/board-room/output`
- `lead-swarm/engineering-review/output`

### Phase 4: Synthesis
**Coordinator** reads all outputs, harmonises conflicts, and produces:
- `lead-swarm/lead-coordinator/output` (final synthesis)

## Schemas

Two JSON schemas validate outputs:

### lead-plan-v1.json
Validates the coordinator's initial plan after task analysis:
- `task_type`: Classification (bug, feature, decision, etc.)
- `sub_swarms_selected`: List of chosen sub-swarms
- `rationale`: Why these sub-swarms were selected

### lead-synthesis-v1.json
Validates the final synthesis output:
- `summary`: Executive summary
- `recommendations`: Prioritised list from all sub-swarms
- `conflicts`: How disagreements were resolved
- `next_steps`: Actionable next steps

## Gates

### Swarm-Level Gates
- `goal-validation` (pre): Ensures goal is actionable
- `synthesis-quality` (post): Verifies synthesis is coherent

### Member-Level Gates
- `implementation-quality-gate`: Validates `a-team` output
- `strategic-alignment-gate`: Validates `board-room` output
- `ci-gate`: Validates `engineering-review` output (must exist in your config)

**Note:** The included gates are demo implementations that always pass. Replace them with real validation logic.

## Customization

### Adding New Sub-Swarms

1. Create a new swarm manifest
2. Add to `lead-swarm.yml` members list
3. Update `lead-coordinator.md` with decision rules

### Modifying Task Classification

Edit `lead-coordinator.md` section "Task Classification" to add new task types or modify selection rules.

### Replacing Demo Gates

1. Edit gate scripts in `gates/` directory
2. Add your validation logic (e.g., lint, security scan, test runner)
3. Reinstall gates to `~/.config/flowstate/gates/lead-swarm/`

## Examples

### Example 1: Simple Bug Fix

```bash
flowstate run --agent lead-coordinator "Fix the memory leak in the streaming module"
```

**Execution:**
1. Coordinator identifies: Simple bug fix + single file
2. Selects: `a-team` only
3. Dispatches: Tactical execution
4. Synthesizes: Fix recommendation

### Example 2: Security-Sensitive Feature

```bash
flowstate run --agent lead-coordinator "Implement OAuth2 authentication with proper security review"
```

**Execution:**
1. Coordinator identifies: Feature + security-sensitive
2. Selects: `board-room` + `a-team` (parallel)
3. Dispatches: Both simultaneously
4. Synthesizes: Implementation with security requirements integrated

### Example 3: Strategic Decision

```bash
flowstate run --agent lead-coordinator "Should we migrate to gRPC? Evaluate options."
```

**Execution:**
1. Coordinator identifies: Architecture decision + research
2. Selects: `board-room` only
3. Dispatches: Strategic review
4. Synthesizes: Recommendation with trade-off analysis

### Example 4: Major Feature

```bash
flowstate run --agent lead-coordinator "Build a new vault integration plugin from scratch"
```

**Execution:**
1. Coordinator identifies: Major feature + new system
2. Selects: All three (parallel)
3. Dispatches: Tactical + strategic + lifecycle
4. Synthesizes: Comprehensive plan with implementation guidance

## Troubleshooting

### Lead Coordinator Not Found

**Error:** `no agent or swarm named 'lead-coordinator'`

**Fix:** Copy the agent manifest to `~/.config/flowstate/agents/` and run `flowstate agents refresh`.

### Sub-Swarm Not Found

**Error:** `member 'board-room' does not resolve`

**Fix:** Ensure sub-swarm manifests are in `~/.config/flowstate/swarms/` and run `flowstate swarms refresh`.

### Gate Fails

**Error:** `gate 'implementation-quality-gate' failed`

**Fix:** Check gate script in `~/.config/flowstate/gates/lead-swarm/`. Ensure it's executable (`chmod +x`). For demo, these gates always pass — modify logic as needed.

### Synthesis Empty

**Error:** No output from sub-swarms

**Fix:** Check coordination store keys match pattern `lead-swarm/<sub-swarm-id>/output`. Sub-swarms must write to their designated keys.

## References

- **Swarm Engine Reference:** `/home/baphled/vaults/baphled/3. Resources/Knowledge Base/FlowState/Architecture/Swarm Engine Reference.md`
- **FlowState Agent Instructions:** `AGENTS.md`
- **Examples Directory:** `/home/baphled/Projects/FlowState.git/agent-platform/examples/`

## License

Part of FlowState — See LICENSE in parent repository.
