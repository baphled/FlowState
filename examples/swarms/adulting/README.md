# Adulting Swarm

A life-admin triage swarm for FlowState. Dump the pile of tasks you've been avoiding — bills, appointments, renewals, council letters, tax deadlines — and the swarm will:

1. **Triage** every task by urgency × impact into four quadrants (Act Now / Schedule / Delegate / Drop)
2. **Track bills** — extract financial obligations, due dates, and overdue status
3. **Scan deadlines** — map all commitments to a timeline, flag critical path items
4. **Draft correspondence** — produce ready-to-send letters to HMRC, councils, utilities, and financial institutions, tone-calibrated to deadline proximity

## Installation

```bash
cp -r examples/swarms/adulting/* ~/.config/flowstate/
```

This copies the swarm manifest, four agents, two skills, and the validation gate into your FlowState config directory.

## Usage

```bash
flowstate swarm adulting
```

When prompted, paste or type your task dump. For example:

```
- HMRC sent a letter about underpaid tax for 2023/24 — I've ignored it for two months
- Council tax direct debit bounced in March, got a reminder letter
- Car insurance renews on 15 May, haven't shopped around yet
- GP appointment overdue by 6 months
- Broadband contract rolling over at full price next week
- Box of old bank statements to sort out
```

The swarm will triage, analyse, and produce a full action plan with draft letters.

## Validation

```bash
flowstate swarm validate adulting
```

## Gate Testing

```bash
# Invalid input — expect pass:false
echo '{"kind":"admin-item-validator","payload":{"tasks":[{"title":"Pay council tax"}]},"policy":{}}' \
  | ~/.config/flowstate/gates/admin-item-validator/gate.py

# Valid input — expect pass:true
echo '{"kind":"admin-item-validator","payload":{"tasks":[{"title":"Pay council tax","deadline":"2026-05-01","priority":1}]},"policy":{}}' \
  | ~/.config/flowstate/gates/admin-item-validator/gate.py
```

## Files

| File | Purpose |
|---|---|
| `adulting.yml` | Swarm manifest — lead, members, gate wiring |
| `agents/life-admin-lead.md` | Orchestrator: triages tasks, coordinates specialists |
| `agents/bill-tracker.md` | Extracts and structures bill obligations |
| `agents/deadline-scanner.md` | Builds chronological deadline timeline |
| `agents/letter-drafter.md` | Drafts formal correspondence per urgency tier |
| `skills/admin-triage/SKILL.md` | Urgency × impact matrix for life-admin |
| `skills/deadline-urgency/SKILL.md` | Tone calibration for bureaucratic correspondence |
| `gates/admin-item-validator/gate.py` | Validates triage output before specialists run |
| `gates/admin-item-validator/manifest.yml` | Gate registration manifest |

## Coordination Store Flow

```
life-admin-lead  →  adulting/{chainID}/tasks        (prioritised task list)
bill-tracker     →  adulting/{chainID}/bills         (financial obligations)
deadline-scanner →  adulting/{chainID}/deadlines     (timeline + critical path)
letter-drafter   →  adulting/{chainID}/letters       (draft correspondence)
life-admin-lead reads all → final action plan presented to user
```
