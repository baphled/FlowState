# quality-gate

Checks that test coverage meets a configurable threshold and the linter is
clean. Used by the `engineering` swarm after the `engineering-quality`
sub-swarm completes.

## Install

```bash
ln -s "$(pwd)/examples/gates/quality-gate" ~/.config/flowstate/gates/quality-gate
```

## Requirements

- Python 3.9+ (uses stdlib only — no `pip install`)
- A lint command available in `PATH` (default: `make lint`)
- A coverage command that prints a single integer (the coverage %) to stdout
  (default: `make coverage-pct`)

## Configuration

Per-environment via env vars:

- `COVERAGE_THRESHOLD` (default `80`) — minimum coverage percentage required
- `LINT_CMD` (default `make lint`) — linter command; non-zero exit = failure
- `COVERAGE_CMD` (default `make coverage-pct`) — command that emits the coverage % as a number

Per-dispatch via the swarm manifest's `policy:` block:

- `coverage_threshold` (int) — overrides `COVERAGE_THRESHOLD`
- `lint_cmd` (string) — overrides `LINT_CMD`
- `coverage_cmd` (string) — overrides `COVERAGE_CMD`

The `coverage_cmd` must print a single integer (or a number optionally followed
by `%`) as its last whitespace-separated token on stdout. Examples:
- `echo 87` — prints `87`
- `go tool cover -func=coverage.out | tail -1 | awk '{print $3}' | tr -d %`

## Input / output

Input (from stdin):

```json
{"kind": "quality-gate", "payload": "", "policy": {"coverage_threshold": 80, "coverage_cmd": "echo 87", "lint_cmd": "make lint"}}
```

Output on success:

```json
{"pass": true}
```

Output on failure (one or more checks failed):

```json
{
  "pass": false,
  "failures": [
    {
      "check": "coverage",
      "reason": "coverage 72% is below threshold 80% (shortfall: 8%)",
      "actual": 72,
      "threshold": 80
    },
    {
      "check": "lint",
      "reason": "linter reported issues",
      "output": "..."
    }
  ]
}
```

## Smoke tests

```bash
# Passing
echo '{"kind":"quality-gate","payload":"","policy":{"coverage_cmd":"echo 90","lint_cmd":"echo ok"}}' \
  | python3 examples/gates/quality-gate/gate.py

# Coverage below threshold
echo '{"kind":"quality-gate","payload":"","policy":{"coverage_cmd":"echo 75","lint_cmd":"echo ok","coverage_threshold":80}}' \
  | python3 examples/gates/quality-gate/gate.py

# Lint failure
echo '{"kind":"quality-gate","payload":"","policy":{"coverage_cmd":"echo 90","lint_cmd":"exit 1"}}' \
  | python3 examples/gates/quality-gate/gate.py
```
