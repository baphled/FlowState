# Gate: ehcp-completeness

Validates that all 11 EHCP sections A–K are present and non-empty in the drafter's output before the compliance checker runs.

## Purpose

This gate fires as a `post-member` check after the `ehcp-drafter` agent writes its output. It prevents the `ehcp-compliance-checker` from running against an incomplete draft — a partial EHCP cannot be meaningfully compliance-checked.

## Sections Validated

| Section | Name |
|---------|------|
| A | Child's/young person's views, interests and aspirations |
| B | Special educational needs |
| C | Health needs |
| D | Social care needs |
| E | Outcomes (SMART) |
| F | Educational provision |
| G | Health provision |
| H | Social care provision |
| I | Placement |
| J | Personal budget |
| K | Appendices checklist |

## Input Format

```json
{
  "kind": "ehcp-completeness",
  "payload": {
    "sections": {
      "A": "Child's views...",
      "B": "Special educational needs...",
      "C": "Health needs...",
      "D": "Social care needs...",
      "E": "Outcomes...",
      "F": "Educational provision...",
      "G": "Health provision...",
      "H": "Social care provision...",
      "I": "Placement...",
      "J": "Personal budget...",
      "K": "Appendices..."
    }
  },
  "policy": {}
}
```

## Output Format

Pass:
```json
{"pass": true}
```

Fail:
```json
{
  "pass": false,
  "reason": "Missing or empty EHCP sections: Section D (Social care needs), Section G (Health provision)",
  "missing": [
    {"section": "D", "name": "Social care needs"},
    {"section": "G", "name": "Health provision"}
  ]
}
```

## Usage

```bash
# Test directly
echo '{"kind":"ehcp-completeness","payload":{"sections":{"A":"views","B":"needs","C":"health","D":"social","E":"outcomes","F":"ed provision","G":"health prov","H":"social prov","I":"placement","J":"budget","K":"appendices"}}}' \
  | python3 gate.py
```

## Requirements

Python 3.9+ stdlib only — no third-party dependencies.
