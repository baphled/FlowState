# quorum-gate

Validates that all five analyst positions are present in the board-room swarm payload
and that the bull and bear analysts reached genuinely different conclusions.

## What it checks

1. **Completeness** — all five required positions (`bull`, `bear`, `market`, `financial`, `technical`) are present
2. **Adversarial divergence** — the `bull` and `bear` positions have different `decision` values

## Why divergence is required

The board-room protocol is adversarial by design. If bull and bear analysts both recommend
the same outcome, the committee has not genuinely stress-tested the pitch — it has performed
a consensus exercise. The gate enforces that at least one meaningful disagreement exists
before the Chair synthesises.

## Input format

The gate reads JSON from stdin:

```json
{
  "kind": "quorum-gate",
  "payload": {
    "positions": {
      "bull":      { "decision": "invest", "thesis": "..." },
      "bear":      { "decision": "pass",   "thesis": "..." },
      "market":    { "decision": "invest", "thesis": "..." },
      "financial": { "decision": "conditional", "thesis": "..." },
      "technical": { "decision": "invest", "thesis": "..." }
    }
  }
}
```

## Output format

**Pass:**
```json
{"pass": true}
```

**Fail — missing positions:**
```json
{"pass": false, "reason": "missing positions from: financial, technical"}
```

**Fail — collapsed adversarial review:**
```json
{"pass": false, "reason": "bull and bear both recommend 'invest' — adversarial review collapsed, re-run"}
```

**Fail — malformed payload:**
```json
{"pass": false, "reason": "payload is not valid JSON"}
```

## Smoke tests

```bash
# Test 1: missing analysts
echo '{"kind":"quorum-gate","payload":{"positions":{"bull":{"decision":"invest"}}}}' \
  | ./gate.py
# Expected: {"pass": false, "reason": "missing positions from: bear, market, financial, technical"}

# Test 2: collapsed adversarial review (bull and bear agree)
echo '{"kind":"quorum-gate","payload":{"positions":{"bull":{"decision":"invest"},"bear":{"decision":"invest"},"market":{"decision":"invest"},"financial":{"decision":"invest"},"technical":{"decision":"invest"}}}}' \
  | ./gate.py
# Expected: {"pass": false, "reason": "bull and bear both recommend 'invest' — adversarial review collapsed, re-run"}

# Test 3: proper divergence
echo '{"kind":"quorum-gate","payload":{"positions":{"bull":{"decision":"invest"},"bear":{"decision":"pass"},"market":{"decision":"invest"},"financial":{"decision":"conditional"},"technical":{"decision":"invest"}}}}' \
  | ./gate.py
# Expected: {"pass": true}

# Test 4: malformed payload string
echo '{"kind":"quorum-gate","payload":"not-json"}' \
  | ./gate.py
# Expected: {"pass": false, "reason": "payload is not valid JSON"}
```
