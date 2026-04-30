---
name: admin-triage
description: Urgency × impact prioritisation matrix for life-admin tasks
category: Life Admin
tier: domain
when_to_use: When receiving a raw dump of overdue life-admin tasks and deciding what to act on first
related_skills:
  - deadline-urgency
---
# Skill: Admin Triage

## What I do

I apply a two-axis prioritisation matrix — **urgency** (how soon action is required) × **impact** (consequence of inaction) — to a pile of life-admin tasks. My output is a scored, ordered task list that downstream agents can act on without ambiguity.

## The Matrix

```
                 HIGH IMPACT          LOW IMPACT
HIGH URGENCY   | ACT NOW (1)       | DELEGATE/AUTOMATE (3) |
LOW URGENCY    | SCHEDULE (2)      | DROP (4)              |
```

**Priority scores map to matrix quadrants:**
- `1` — Act Now: high urgency + high impact (legal deadlines, fines, service cutoffs, HMRC notices)
- `2` — Schedule: low urgency + high impact (health checks, insurance renewals, wills, pension reviews)
- `3` — Delegate/Automate: high urgency + low impact (minor admin that can be automated or handed off)
- `4` — Drop: low urgency + low impact (nice-to-have, no real consequence if deferred indefinitely)

## Urgency Heuristics

Classify as HIGH urgency if ANY of:
- A legal or regulatory deadline exists within 28 days
- A fine, interest charge, or service disconnection is the consequence of inaction
- A letter, notice, or summons has an explicit response-by date within 28 days
- A renewal (car tax, passport, insurance) lapses within 14 days
- A court date, appointment, or hearing is within 14 days

Classify as LOW urgency if:
- No consequence for at least 29 days
- Deadline is self-imposed or aspirational (not externally enforced)
- Reminder is proactive rather than overdue

## Impact Heuristics

Classify as HIGH impact if inaction leads to ANY of:
- Financial penalty, fine, debt interest, or enforcement action
- Loss of legal status (driving licence, passport, right-to-work, tenancy)
- Health risk (missed medication reviews, overdue check-ups)
- Damaged relationship with a statutory body (HMRC, DVLA, council, NHS)
- Contract breach or automatic rollover at a worse rate

Classify as LOW impact if:
- Task is administrative tidying with no external enforcement
- A missed deadline means mild inconvenience, not material consequence
- The "worst case" is repeating an email or making a phone call

## Output Format

For each task emit:
```json
{
  "title": "Pay council tax direct debit",
  "deadline": "2026-05-01",
  "priority": 1,
  "urgency": "high",
  "impact": "high",
  "rationale": "Council tax arrears trigger enforcement within 7 days of missed payment."
}
```

## Example Prioritisation Decisions

| Task | Urgency | Impact | Priority | Rationale |
|------|---------|--------|----------|-----------|
| HMRC self-assessment overdue by 3 months | HIGH | HIGH | 1 | Daily interest accruing; penalty escalates |
| Respond to council-tax reminder letter | HIGH | HIGH | 1 | Ignored letters lead to liability order |
| Renew car insurance (expires in 10 days) | HIGH | HIGH | 1 | Driving uninsured is a criminal offence |
| Book annual GP check-up | LOW | HIGH | 2 | No imminent consequence but important for health |
| Review broadband contract rolling over next month | LOW | HIGH | 2 | Better deal possible; auto-renews in 30 days |
| Set up direct debit for water bill | HIGH | LOW | 3 | Mildly overdue; simple one-off action |
| File old bank statements | LOW | LOW | 4 | No external deadline; purely organisational |
| Sort that pile of catalogues | LOW | LOW | 4 | Zero consequence; drop or do opportunistically |

## Edge Cases

- **Uncertain deadline**: If no date is given, infer from context ("before end of financial year" → 5 April; "renewal notice" → assume within 30 days and treat as HIGH urgency).
- **Multiple tasks, single bill**: Collapse duplicates ("pay gas bill" + "call gas company about bill") into one item with the highest urgency of the group.
- **Task needs more information**: Flag as priority 1 with `deadline: "clarify urgently"` — ambiguity in life-admin usually means something has been avoided.
