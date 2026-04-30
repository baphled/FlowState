# Swarm Gates

Gates are quality checkpoints evaluated at defined lifecycle boundaries during
swarm execution. They validate member output, enforce policies, run external
commands, or check for PII before the final response leaves the swarm.

## Lifecycle Points

| When | Scope | Fires |
|------|-------|-------|
| `pre` | Swarm-level | Once, before the first member runs |
| `post` | Swarm-level | Once, after the last member returns |
| `pre-member` | Member-level | Before a specific member runs |
| `post-member` | Member-level | After a specific member's stream completes |

## Gate Kinds

### Built-in Gates

| Kind | Description |
|------|-------------|
| `builtin:result-schema` | Validates member output against a registered JSON Schema. Uses `schema_ref` to look up the schema by name. |

### External Gates

External gates use the `ext:` prefix and invoke executables on disk. The name
after `ext:` maps to a directory under `~/.config/flowstate/gates/`:

```
ext:ci-gate    →  ~/.config/flowstate/gates/ci-gate/
```

Each gate directory must contain:
- `manifest.yml` — gate metadata, timeout, and default policy
- `gate.py` (or other executable) — the gate logic

## Gate Request Format

When FlowState invokes a gate, it sends a JSON object via stdin:

```json
{
  "kind": "ci-gate",
  "member_id": "engineering-implementation",
  "when": "post-member",
  "payload": "{\"swarm_id\":\"engineering\",\"chain_id\":\"abc123\",\"output_key\":\"output\",\"member_output\":\"...\"}",
  "policy": {
    "test_cmd": "make test"
  }
}
```

Key details:
- `member_id` and `when` are top-level fields (not nested inside `payload`)
- `payload` is a raw JSON-encoded byte string containing the member output and context
- `policy` is merged from the gate's `manifest.yml` `policy:` block with per-request overrides

## Gate Response Format

The gate must write a JSON object to stdout. Only three fields are consumed
by the framework:

```json
{
  "pass": true,
  "reason": "optional explanation",
  "evidence": [{"source": "...", "snippet": "..."}]
}
```

Or on failure:

```json
{
  "pass": false,
  "reason": "unit tests failed"
}
```

**Note:** Gates may output additional fields (e.g. `score`, `output`), but
only `pass`, `reason`, and `evidence` are read by FlowState. Extra fields
are silently discarded.

Evidence items use the following shape:

```json
{
  "pass": false,
  "reason": "Schema validation failed",
  "evidence": [
    {"source": "field_name", "snippet": "expected type string, got integer"}
  ]
}
```

## Failure Policies

| Policy | Behaviour |
|--------|-----------|
| `halt` (default) | Swarm stops immediately. Error returned to user. |
| `continue` | Failure recorded in dispatch report. Swarm continues. |
| `warn` | Warning recorded. Swarm continues. |

## Authoring a Custom Gate

### Step 1: Create the Gate Directory

```bash
mkdir -p ~/.config/flowstate/gates/my-gate
```

### Step 2: Write the Manifest

`~/.config/flowstate/gates/my-gate/manifest.yml`:

```yaml
name: my-gate
description: Validates that output contains required sections
version: "0.1.0"
exec: ./gate.py
timeout: 10s
policy:
  required_sections:
    - summary
    - recommendations
```

### Step 3: Write the Gate Executable

`~/.config/flowstate/gates/my-gate/gate.py`:

```python
#!/usr/bin/env python3
import json
import sys

def main():
    request = json.load(sys.stdin)
    payload = request.get("payload") or {}
    if isinstance(payload, str):
        payload = json.loads(payload)
    policy = request.get("policy", {})

    member_output = payload.get("member_output", "")
    required = policy.get("required_sections", [])

    missing = [s for s in required if s not in member_output.lower()]

    if missing:
        json.dump({
            "pass": False,
            "reason": f"Missing required sections: {', '.join(missing)}"
        }, sys.stdout)
    else:
        json.dump({"pass": True}, sys.stdout)

if __name__ == "__main__":
    main()
```

Make it executable:

```bash
chmod +x ~/.config/flowstate/gates/my-gate/gate.py
```

### Step 4: Reference in Swarm Manifest

```yaml
harness:
  gates:
    - name: my-gate
      kind: ext:my-gate
      when: post-member
      target: writer
      output_key: output
```

## Policy-First with Env-Var Fallback

Gates should follow the pattern of reading configuration from:

1. `policy` field in the gate request (from manifest)
2. Environment variables
3. Hardcoded defaults

This makes gates immediately usable without configuration while remaining
fully override-able at runtime:

```python
test_cmd = (
    policy.get("test_cmd")
    or os.environ.get("TEST_CMD", "make test")
)
```

Override at runtime:

```bash
TEST_CMD="go test ./internal/..." flowstate run --agent engineering "fix the parser"
```

## Testing Gates Locally

Gates can be tested directly without running a full swarm. When piping
JSON manually, `payload` is a JSON object (FlowState sends it as a
JSON-encoded string at runtime; reference gates handle both forms):

```bash
echo '{"kind":"ci-gate","policy":{"test_cmd":"echo ok"}}' \
  | ~/.config/flowstate/gates/ci-gate/gate.py
```

Expected output (the `output` field is included by the gate but ignored
by the framework):

```json
{"pass": true, "output": "ok"}
```

Test failure path:

```bash
echo '{"kind":"relevance-gate","payload":{"task_plan":"kubernetes autoscaling","research":"The history of ancient Rome."}}' \
  | ~/.config/flowstate/gates/relevance-gate/gate.py
```

Expected output:

```json
{"pass": false, "reason": "research relevance score 0.00 below threshold 0.40", "missing_topics": [...], "redirect": "Research should cover: ..."}
```

## Built-in Gate Reference

### builtin:result-schema

Validates member output against a registered JSON Schema.

**Fields:**
- `schema_ref` — name of the registered schema to validate against

**Resolution:**
The schema name is resolved from:
1. `schema_ref` in the gate spec
2. Legacy convention: `<swarm-id>-<member-id>`
3. Default: the member's output key

## Common Gate Patterns

### Validate Output Structure

Use `builtin:result-schema` when you have a formal JSON Schema for expected
output. Best for: plan documents, bug reports, compliance letters.

### Run External Commands

Use an `ext:` gate when you need to execute shell commands (tests, linters,
coverage checks). Best for: CI gates, integration tests.

### Check for Sensitive Data

Use an `ext:` gate that scans output for PII patterns (email addresses, phone
numbers, national insurance numbers). Supports `strip` mode (removes PII) or
`block` mode (halts the swarm).

### Enforce Adversarial Divergence

Use a quorum gate that checks whether multiple analysts produced genuinely
divergent opinions rather than rubber-stamping each other.
