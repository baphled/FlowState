# ci-gate

Runs the project's unit test suite and fails the pipeline if any test fails.
Used by the `engineering` swarm after the `engineering-implementation` sub-swarm
completes.

## Install

```bash
ln -s "$(pwd)/examples/gates/ci-gate" ~/.config/flowstate/gates/ci-gate
```

## Requirements

- Python 3.9+ (uses stdlib only — no `pip install`)
- A test command available in `PATH` (default: `make test`)

## Configuration

Per-environment via env var:

- `TEST_CMD` (default `make test`) — command to run the test suite

Per-dispatch via the swarm manifest's `policy:` block:

- `test_cmd` (string) — overrides `TEST_CMD` and the default

## Input / output

Input (from stdin):

```json
{"kind": "ci-gate", "payload": "", "policy": {"test_cmd": "go test ./..."}}
```

Output on success:

```json
{"pass": true, "output": "ok  github.com/example/project  0.123s"}
```

Output on failure:

```json
{"pass": false, "reason": "unit tests failed", "output": "FAIL github.com/example/project  0.456s"}
```

## Smoke test

```bash
# Passing
echo '{"kind":"ci-gate","payload":"","policy":{"test_cmd":"echo tests-pass"}}' \
  | python3 examples/gates/ci-gate/gate.py

# Failing
echo '{"kind":"ci-gate","payload":"","policy":{}}' \
  | TEST_CMD="exit 1" python3 examples/gates/ci-gate/gate.py
```
