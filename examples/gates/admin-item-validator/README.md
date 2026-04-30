# Gate: admin-item-validator

An external gate for the `adulting` swarm. Fires `post-member` on the `life-admin-lead`'s triage output to ensure every task has a `deadline` and a `priority` score (integer 1–4) before the specialist agents run.

## Validation Rules

- `deadline`: non-empty string (ISO date or descriptive text such as "before end of tax year")
- `priority`: integer in the range 1–4

If any task fails validation, the gate returns `pass: false` with a list of incomplete task titles and the specific missing fields. The swarm halts and the specialists do not run, preventing downstream agents from operating on malformed data.

## Input

```json
{
  "kind": "admin-item-validator",
  "payload": {
    "tasks": [
      {
        "title": "Pay council tax",
        "deadline": "2026-05-01",
        "priority": 1,
        "urgency": "high",
        "impact": "high",
        "rationale": "Council tax arrears trigger enforcement within 7 days."
      }
    ]
  },
  "policy": {}
}
```

## Output

Pass:
```json
{"pass": true}
```

Fail:
```json
{
  "pass": false,
  "reason": "1 task(s) failed validation: Book dentist (missing: deadline, priority)",
  "incomplete": ["Book dentist (missing: deadline, priority)"]
}
```

## Testing

```bash
# Missing deadline and priority — expect pass:false
echo '{"kind":"admin-item-validator","payload":{"tasks":[{"title":"Pay council tax"}]},"policy":{}}' \
  | python3 gate.py

# Valid task — expect pass:true
echo '{"kind":"admin-item-validator","payload":{"tasks":[{"title":"Pay council tax","deadline":"2026-05-01","priority":1}]},"policy":{}}' \
  | python3 gate.py
```
