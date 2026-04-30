# Testing Swarms

This guide covers how to validate, test, and debug swarm configurations
before and during use.

## Validation Commands

### Validate a Swarm

```bash
flowstate swarm validate [<swarm-id>]
```

With an ID, validates that specific swarm. Without an ID, validates all
registered swarms.

Checks:
- Schema version is `"1.0.0"`
- `id` and `lead` are non-empty
- `lead` resolves to a registered agent or swarm
- All members resolve to registered agents or swarms
- No self-references (member equals swarm ID)
- No ID collisions with agent registry
- No cycles in the membership graph (checked via depth-bounded DFS, max depth 64, implemented in `internal/swarm/manifest.go`)
- All gate kinds have valid prefixes (`builtin:` or `ext:`)
- Gate lifecycle-target pairing is correct

### List Registered Swarms

```bash
flowstate swarm list
```

Lists all registered swarms with their ID, lead agent, member count,
and gate count.

## Testing Gates in Isolation

### Direct Execution

All gates read JSON from stdin and write JSON to stdout. Test them directly:

```bash
# ci-gate — success (ci-gate reads policy, not payload)
echo '{"kind":"ci-gate","policy":{"test_cmd":"echo ok"}}' \
  | ~/.config/flowstate/gates/ci-gate/gate.py

# ci-gate — failure
echo '{"kind":"ci-gate","policy":{"test_cmd":"exit 1"}}' \
  | ~/.config/flowstate/gates/ci-gate/gate.py
```

### Testing with Real Member Output

Simulate a gate invocation with realistic member output:

```bash
cat <<'EOF' | ~/.config/flowstate/gates/relevance-gate/gate.py
{
  "kind": "relevance-gate",
  "payload": {
    "task_plan": "kubernetes horizontal pod autoscaling",
    "member_output": "Kubernetes HPA scales deployment replicas based on CPU and memory metrics. The target CPU utilisation threshold determines when new pods are created."
  }
}
EOF
```

### Gate Timeout Testing

Verify that gates respect their configured timeout:

```yaml
# In manifest.yml
harness:
  gates:
    - name: slow-gate
      kind: ext:my-slow-gate
      timeout: 5s
```

Create a gate that sleeps longer than the timeout to verify the behaviour:

```python
#!/usr/bin/env python3
import time
import json
import sys

time.sleep(10)  # Should be killed by 5s timeout
json.dump({"pass": True}, sys.stdout)
```

## Testing Swarm Composition

### Sub-Swarm Resolution

Test that sub-swarms resolve correctly:

```bash
# Verify parent swarm
flowstate swarm validate engineering

# Verify each sub-swarm independently
flowstate swarm validate engineering-planning
flowstate swarm validate engineering-implementation
flowstate swarm validate engineering-quality
```

### Cycle Detection

The validator catches cycles. Test by creating a cyclic reference:

```yaml
# swarm-a.yml
schema_version: "1.0.0"
id: swarm-a
lead: planner
members:
  - swarm-b

# swarm-b.yml
schema_version: "1.0.0"
id: swarm-b
lead: planner
members:
  - swarm-a
```

```bash
flowstate swarm validate swarm-a
# Expected: "cycle detected: swarm-a -> swarm-b -> swarm-a"
```

## Running Swarm Tests

### Smoke Test: Minimal Swarm

Create a minimal swarm that uses only built-in agents:

```yaml
# ~/.config/flowstate/swarms/smoke-test.yml
schema_version: "1.0.0"
id: smoke-test
description: Minimal smoke test swarm.
lead: planner
members:
  - explorer
harness:
  parallel: false
```

Run it from the TUI chat with `@smoke-test list all files in the internal/app
directory` or from the CLI:

```bash
flowstate run --agent smoke-test "list all files in the internal/app directory"
```

Expected: The planner delegates to explorer, explorer lists files, output is
returned. No gates fire.

### Smoke Test: Single Gate

```yaml
schema_version: "1.0.0"
id: gate-test
description: Swarm with a single post-member gate.
lead: planner
members:
  - explorer
harness:
  parallel: false
  gates:
    - name: ci-gate
      kind: ext:ci-gate
      when: post-member
      target: explorer
```

Run it and verify the gate fires after explorer completes.

### Integration Test: Full Pipeline

Use the engineering swarm as an integration test. It exercises:
- Swarm-of-swarms resolution
- Sequential sub-swarm dispatch
- External gates (ci, integration, quality)
- Coordination store across nesting levels

```bash
cd /path/to/your/go/project
flowstate run --agent engineering "Add a new endpoint to the REST API"
```

## Debugging

### Swarm Activity Timeline

In the TUI, the secondary pane shows real-time swarm events:

- **Delegation events** — when the lead delegates to a member
- **Tool-call events** — tool invocations during member execution
- **Gate events** — when gates fire and their pass/fail status
- **Plan events** — task plans written to the coordination store

Press `Ctrl+T` to toggle the swarm activity pane.

### Event Details

Press `Ctrl+E` to view details of the most recent swarm event. This shows
the full event payload including timestamps, member IDs, and gate results.

### Session Tree

Press `Ctrl+G` to view the session tree, which shows the full delegation
hierarchy for the current session including sub-swarms.

### Log Output

FlowState uses structured logging via `slog`. Swarm-related log entries
include key-value pairs such as:

```
WARN  ext gate registration failed  err="exec not found: ci-gate"
INFO  swarm schema dir absent         dir="/home/user/.config/flowstate/swarms"
```

Watch for `WARN` entries mentioning swarm validation, gate registration, or
coordination store issues at startup.

### Coordination Store Inspection

The coordination store is a single JSON file at
`~/.local/share/flowstate/coordination.json`. It stores all coordination
data including swarm task plans and member output:

```bash
cat ~/.local/share/flowstate/coordination.json | python3 -m json.tool
```

Look for keys matching your chain prefix (e.g. `a-team/`, `engineering/`).

## Common Failure Modes

### Lead Cannot Delegate

**Symptom:** Swarm starts but only the lead produces output.

**Cause:** Lead agent does not have `can_delegate: true` in its manifest.

**Fix:** Set `can_delegate: true` in the agent's manifest under `delegation`:

```yaml
delegation:
  can_delegate: true
```

### Member Not Found

**Symptom:** `swarm validate` fails with "member 'X' not found".

**Cause:** Agent manifest not installed or FlowState not restarted after
adding new manifests.

**Fix:**
```bash
cp agents/*.md ~/.config/flowstate/agents/
```
Then restart FlowState. Agent and swarm discovery happens at startup from the
files on disk in `~/.config/flowstate/agents/` and `~/.config/flowstate/swarms/`.

### Gate Executable Not Found

**Symptom:** Swarm fails with "ext:my-gate not found in gate registry".

**Cause:** Gate directory missing or `manifest.yml` absent.

**Fix:**
```bash
mkdir -p ~/.config/flowstate/gates/my-gate
# Add manifest.yml and gate.py
chmod +x ~/.config/flowstate/gates/my-gate/gate.py
```

### Cycle Detected

**Symptom:** "cycle detected: swarm-a -> swarm-b -> swarm-a".

**Cause:** Circular membership references.

**Fix:** Break the cycle by removing one of the circular references. Use a
sequential pattern instead: swarm-a delegates to swarm-b, but swarm-b does
not reference swarm-a.

### Cross-Registry Collision

**Symptom:** "swarm ID 'explorer' collides with agent ID 'explorer'".

**Cause:** A swarm and an agent share the same ID.

**Fix:** Rename either the swarm or the agent so IDs are unique across both
registries.
