# Lead Swarm Architecture

## Overview

The **Lead Swarm** is a flexible orchestrator that accepts any goal, deconstructs it, identifies optimal agent compositions, coordinates execution, and synthesizes results. Unlike rigid task-specific swarms, the lead swarm adapts dynamically to the user's request.

## Design Philosophy

1. **Goal-First**: Start with "what", not "who" — the swarm figures out the right experts
2. **Adaptive Composition**: Dynamically selects agents/sub-swarms based on task needs
3. **Parallel Exploration**: Multiple sub-teams work in parallel when appropriate
4. **Synthesis-Focused**: Coordinator synthesizes diverse inputs into cohesive output

## Swarm Composition

```
lead-swarm (Top-level orchestrator)
├── coordinator (Lead agent)
├── a-team (Sub-swarm: Tactical execution)
│   ├── Senior-Engineer
│   ├── Code-Reviewer
│   └── QA-Engineer
├── board-room (Sub-swarm: Strategic review)
│   ├── Tech-Lead
│   ├── Security-Engineer
│   └── Researcher
└── engineering-review (Sub-swarm: Full engineering lifecycle)
    ├── planning-loop
    ├── implementation
    └── quality-assurance
```

## Execution Flow

### Phase 1: Decomposition
**Coordinator analyzes the goal:**
- Parse task type (feature, bug, research, documentation)
- Identify complexity (simple, moderate, complex)
- Determine required expertise (security, testing, architecture)
- Select optimal sub-swarm composition

### Phase 2: Parallel Exploration
**Coordinator dispatches to selected sub-swarms:**
- `a-team`: Tactical implementation focus
- `board-room`: Strategic review and validation
- `engineering-review`: Full lifecycle if needed

Sub-swarms work in parallel, each producing their perspective.

### Phase 3: Synthesis
**Coordinator receives outputs and synthesizes:**
- Harmonize conflicting recommendations
- Prioritise based on user context
- Produce coherent action plan or result
- Present with clear next steps

## Example Invocations

### Simple Task
```
@lead-swarm Fix the memory leak in the streaming module
```

**Execution:**
1. Coordinator identifies as bug fix
2. Dispatches to `a-team` only (Senior-Engineer + QA-Engineer)
3. Synthesizes single path solution

### Complex Feature
```
@lead-swarm Implement OAuth2 authentication with proper security review
```

**Execution:**
1. Coordinator identifies as feature + security concern
2. Dispatches to `a-team` (implementation) AND `board-room` (security review)
3. Synthesizes merged recommendations

### Strategic Decision
```
@lead-swarm Should we migrate to a new streaming protocol? Evaluate options.
```

**Execution:**
1. Coordinator identifies as research + architecture decision
2. Dispatches to `board-room` only (Tech-Lead + Researcher)
3. Synthesizes recommendation with analysis

### Full Engineering Lifecycle
```
@lead-swarm Build a new vault integration plugin from scratch
```

**Execution:**
1. Coordinator identifies as major new feature
2. Dispatches to all three sub-swarms in parallel
3. Synthesizes comprehensive plan with implementation guidance

## Coordinator Agent Manifest

The coordinator agent needs these capabilities:

```yaml
can_delegate: true
tools:
  - delegate
  - knowledge-base-query
  - memory-search

behaviour:
  - Always decompose before delegating
  - Select sub-swarms based on task analysis
  - Synthesize outputs into cohesive response
  - Provide clear next steps to user
```

## Gate Strategy

### Swarm-Level Gates
- `pre:goal-validation` — Ensure goal is actionable
- `post:synthesis-quality` — Verify synthesis is coherent and actionable

### Member-Level Gates
- `a-team`: `post-member:implementation-quality` — Check code quality
- `board-room`: `post-member:strategic-alignment` — Verify alignment with goals
- `engineering-review`: `post-member:ci-gate` — Ensure tests pass

## Configuration

### Customizing Sub-Swarms

You can modify sub-swarm composition by editing their manifests:

```yaml
# Add more specialists to a-team
members:
  - Senior-Engineer
  - Code-Reviewer
  - QA-Engineer
  - Performance-Engineer  # New addition

# Or remove sub-swarm entirely from lead-swarm.yml
members:
  - a-team
  # - board-room  # Commented out = disabled
```

### Adding New Sub-Swarms

1. Create new swarm manifest in `examples/swarms/lead-swarm/`
2. Add to `lead-swarm.yml` members list
3. Update coordinator logic to recognise use cases

## Extensions

### Custom Gates
Add project-specific gates:

```yaml
gates:
  - name: security-scan
    kind: ext:security-gate
    when: post-member
    target: a-team
    output_key: output
```

### Project-Specific Context
Embed project knowledge:

```yaml
context:
  chain_prefix: my-project/lead-swarm
  project_root: /path/to/project
```

## Testing

### Manual Testing

```bash
# Simple task
flowstate run --agent lead-swarm "Fix typo in README.md"

# Complex feature
flowstate run --agent lead-swarm "Implement OAuth2 with security review"

# Strategic
flowstate run --agent lead-swarm "Evaluate streaming protocol options"
```

### BDD Scenarios

See `features/lead-swarm/` for test scenarios:

- `simple-task.feature` — Single sub-swarm dispatch
- `parallel-exploration.feature` — Multiple sub-swarms in parallel
- `synthesis.feature` — Coordinator synthesizes outputs
- `gate-failure.feature` — Gate blocks on quality issues

## Troubleshooting

### Coordinator Not Selecting Right Sub-Swarm
**Cause:** Task classification logic needs tuning
**Fix:** Review coordinator agent manifest, adjust `behaviour` section

### Synthesis Not Coherent
**Cause:** Sub-swarm outputs conflicting or incomplete
**Fix:** Add gates to sub-swarms to validate outputs before synthesis

### All Sub-Swarms Running Unnecessarily
**Cause:** Coordinator dispatching too aggressively
**Fix:** Add pre-delegation analysis step to filter by complexity

## Performance Considerations

- **Parallel dispatch**: 3 sub-swarms × average 30s = ~30s wall clock (not 90s)
- **Gate overhead**: ~1-2s per gate
- **Synthesis time**: ~10s for coordinator to merge outputs

**Total typical time**: 40-60s for complex tasks vs 90s+ sequentially

## Future Enhancements

1. **Learning**: Coordinator learns from past which sub-swarm combos work best
2. **Dynamic composition**: Sub-swarms adjust their own member lists based on task
3. **Bidirectional gates**: Sub-swarms can request additional sub-swarm support
4. **User feedback**: User can approve/reject synthesis iterations

## Related Documentation

- `/home/baphled/vaults/baphled/3. Resources/Knowledge Base/FlowState/Architecture/Swarm Engine Reference.md`
- `/home/baphled/Projects/FlowState.git/agent-platform/internal/swarm/doc.go`
- `/home/baphled/.config/flowstate/swarms/planning-loop.yml`
