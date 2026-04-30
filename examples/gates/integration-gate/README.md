# integration-gate

Runs integration tests and optionally checks for breaking API changes. Used by
the `engineering` swarm after the `engineering-implementation` sub-swarm, after
`ci-gate` has already passed.

## Install

```bash
ln -s "$(pwd)/examples/gates/integration-gate" ~/.config/flowstate/gates/integration-gate
```

## Requirements

- Python 3.9+ (uses stdlib only — no `pip install`)
- An integration test command available in `PATH` (default: `make test-integration`)

## Configuration

Per-environment via env vars:

- `INTEGRATION_CMD` (default `make test-integration`) — integration test command
- `BREAKING_CMD` (default empty — disabled) — optional command to check for breaking API changes

Per-dispatch via the swarm manifest's `policy:` block:

- `integration_cmd` (string) — overrides `INTEGRATION_CMD` and the default
- `breaking_cmd` (string) — overrides `BREAKING_CMD`; leave empty to skip the check

## Input / output

Input (from stdin):

```json
{"kind": "integration-gate", "payload": "", "policy": {"integration_cmd": "make test-integration", "breaking_cmd": ""}}
```

Output on success:

```json
{"pass": true, "output": "integration tests passed"}
```

Output on integration test failure:

```json
{"pass": false, "reason": "integration tests failed", "output": "FAIL ..."}
```

Output on breaking-change detection:

```json
{"pass": false, "reason": "breaking API changes detected", "output": "...diff output..."}
```

## Smoke tests

```bash
# Passing
echo '{"kind":"integration-gate","payload":"","policy":{"integration_cmd":"echo integration-ok"}}' \
  | python3 examples/gates/integration-gate/gate.py

# Failing integration tests
echo '{"kind":"integration-gate","payload":"","policy":{"integration_cmd":"exit 1"}}' \
  | python3 examples/gates/integration-gate/gate.py

# Breaking-change detected
echo '{"kind":"integration-gate","payload":"","policy":{"integration_cmd":"echo ok","breaking_cmd":"exit 1"}}' \
  | python3 examples/gates/integration-gate/gate.py
```
