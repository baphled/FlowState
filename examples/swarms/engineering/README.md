# Engineering Swarm Example

A swarm-of-swarms that coordinates planning, implementation, and quality
assurance using only FlowState's built-in agents. No custom agent files
are required.

## What is a swarm-of-swarms?

A swarm-of-swarms is a top-level swarm whose `members` list contains other
swarm IDs instead of (or in addition to) individual agent IDs. The FlowState
runner resolves each member by checking the agent registry first, then the
swarm registry. This means you can compose complex multi-phase workflows from
smaller, reusable swarms.

The `engineering` swarm delegates to three sub-swarms in sequence:

```
planner (orchestrator lead)
  → engineering-planning
      planner → explorer → librarian → analyst → plan-writer
  → engineering-implementation
      Senior-Engineer → executor → plan-reviewer
      [ci-gate]
      [integration-gate]
  → engineering-quality
      QA-Engineer → Code-Reviewer → Security-Engineer
      [quality-gate]
```

## Install

```bash
cp -r examples/swarms/engineering/* ~/.config/flowstate/swarms/
cp -r examples/gates/ci-gate examples/gates/integration-gate examples/gates/quality-gate \
    ~/.config/flowstate/gates/
flowstate agents refresh
```

## Usage

```bash
flowstate swarm run engineering "Implement the user authentication feature"
```

## Configuration

All gates are configured via policy fields in `manifest.yml` with env-var
fallback. Override at runtime by setting the env var before running.

| Env var              | Default                 | Gate              |
|----------------------|-------------------------|-------------------|
| `TEST_CMD`           | `make test`             | ci-gate           |
| `INTEGRATION_CMD`    | `make test-integration` | integration-gate  |
| `BREAKING_CMD`       | (none — disabled)       | integration-gate  |
| `LINT_CMD`           | `make lint`             | quality-gate      |
| `COVERAGE_THRESHOLD` | `80`                    | quality-gate      |
| `COVERAGE_CMD`       | `make coverage-pct`     | quality-gate      |

Example — override test command:

```bash
TEST_CMD="go test ./..." flowstate swarm run engineering "Fix the parser bug"
```

## Gates

### ci-gate

Runs the project's unit test suite. Fires after `engineering-implementation`
completes. If the test command exits non-zero the swarm halts and returns the
full test output for diagnosis.

- Input: `{"policy": {"test_cmd": "..."}}` (optional — falls back to `TEST_CMD`)
- Output: `{"pass": true, "output": "..."}` or `{"pass": false, "reason": "unit tests failed", "output": "..."}`

### integration-gate

Runs integration tests and optionally checks for breaking API changes. Fires
after `engineering-implementation`, after ci-gate. The `breaking_cmd` check is
disabled by default — set `BREAKING_CMD` or `policy.breaking_cmd` to enable it.

- Input: `{"policy": {"integration_cmd": "...", "breaking_cmd": "..."}}`
- Output: `{"pass": true, "output": "..."}` or `{"pass": false, "reason": "...", "output": "..."}`

### quality-gate

Checks that test coverage meets the threshold and the linter is clean. Fires
after `engineering-quality`. The `coverage_cmd` should print a single integer
(the coverage percentage) to stdout — e.g. `echo 87` or `go tool cover -func=...
| tail -1 | awk '{print $3}' | tr -d %`.

- Input: `{"policy": {"coverage_threshold": 80, "coverage_cmd": "...", "lint_cmd": "..."}}`
- Output: `{"pass": true}` or `{"pass": false, "failures": [{"check": "coverage"|"lint", ...}]}`

## Prerequisites

- FlowState installed and `flowstate agents refresh` run to populate built-in agents
- Python 3.9+ available in `PATH` (gates use stdlib only — no `pip install`)
- `make test`, `make test-integration`, `make lint`, and `make coverage-pct`
  targets defined in your project's `Makefile`, or the corresponding env vars
  overridden to match your project's test commands

## Design Notes

**Zero custom agents.** All member IDs (`planner`, `explorer`, `Senior-Engineer`,
etc.) resolve from the built-in agent registry. The example ships with no
`agents/` directory.

**Gates live on the orchestrator.** `ci-gate` and `integration-gate` both
target `engineering-implementation`; `quality-gate` targets `engineering-quality`.
Sub-swarm manifests stay clean and the gate topology is visible in one place
(`engineering.yml`).

**Policy-first with env-var fallback.** Each gate reads its configuration from
the `policy` field in the gate request first, then env vars, then hardcoded
defaults. Gates are immediately usable without any configuration and fully
override-able at runtime.

**Sequential harness.** All swarms use `parallel: false`. The engineering
workflow is inherently sequential: plan before implementing, implement before
assuring quality.
