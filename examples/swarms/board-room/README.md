# Board Room Swarm Example

A pitch committee swarm that puts your business idea or investment proposal through
a structured 3-round adversarial debate. Five specialist analysts evaluate
independently, critique each other anonymously, and the Chair synthesises
an investment memo with preserved dissent.

## What is this example demonstrating?

- Custom agents (6 files, all in `agents/`)
- Custom skills (4 files, all in `skills/`)
- `coordination_store` as a message bus for multi-round agent communication
- A quorum gate enforcing genuine adversarial divergence between bull and bear analysts
- The `dissent-protocol` skill ensuring minority positions survive to the final decision

## Install

```bash
cp -r examples/swarms/board-room/* ~/.config/flowstate/
```

## Prerequisites

No `flowstate agents refresh` required — all agents are custom and bundled in this example.
The quorum gate requires Python 3.9+ (stdlib only, no external dependencies).

## Usage

```bash
flowstate swarm run board-room "Your pitch text here — the more detail the better"
```

Provide as much context as possible: the problem, solution, market size, traction, team, and any financials you have.

## How it works

### Round 1 — Independent Analysis (parallel)

All five analysts read the pitch and write independent positions to the coordination store.
The quorum gate fires after the last analyst and validates that:
1. All five positions are present
2. The bull and bear analysts reached different conclusions (enforcing genuine adversarial review)

### Round 2 — Anonymous Peer Review

The Chair anonymises all positions — stripping analyst names and roles — and distributes
the anonymised bundle to all analysts. Each analyst reads the anonymous positions and
writes a critique that must engage with at least 2 other positions, citing specific claims.

### Round 3 — Synthesis

The Chair reads all positions and critiques, determines the majority verdict, and writes:
- `board-room/{chainID}/investment-memo` — full memo with dissent entries
- `board-room/{chainID}/decision` — structured decision JSON

## Coordination store key map

| Key | Written By | When |
|-----|-----------|------|
| `board-room/{chainID}/pitch` | Chair | Round 0 |
| `board-room/{chainID}/positions/bull` | bull-analyst | Round 1 |
| `board-room/{chainID}/positions/bear` | bear-analyst | Round 1 |
| `board-room/{chainID}/positions/market` | market-analyst | Round 1 |
| `board-room/{chainID}/positions/financial` | financial-analyst | Round 1 |
| `board-room/{chainID}/positions/technical` | technical-analyst | Round 1 |
| `board-room/{chainID}/positions-anon` | Chair | Between Round 1 and 2 |
| `board-room/{chainID}/critiques/bull` | bull-analyst | Round 2 |
| `board-room/{chainID}/critiques/bear` | bear-analyst | Round 2 |
| `board-room/{chainID}/critiques/market` | market-analyst | Round 2 |
| `board-room/{chainID}/critiques/financial` | financial-analyst | Round 2 |
| `board-room/{chainID}/critiques/technical` | technical-analyst | Round 2 |
| `board-room/{chainID}/investment-memo` | Chair | Round 3 |
| `board-room/{chainID}/decision` | Chair | Round 3 |

## Reading the output

The investment memo is in `board-room/{chainID}/investment-memo`. The decision
JSON at `board-room/{chainID}/decision` shows:

- `decision`: invest / pass / conditional
- `confidence`: 1–5 (committee consensus strength; 5 = near-unanimous)
- `dissents`: minority positions with their key reasons and most compelling evidence
- `conditions`: required if decision is conditional (specific and verifiable)
- `dealbreaker_risks`: any risk any analyst classified as DEALBREAKER

## The quorum gate

If bull and bear analysts both recommend the same outcome, the gate returns
`pass: false` and the swarm halts. This enforces genuine adversarial debate —
a committee where all analysts agree is not stress-testing the pitch.

The gate also halts if any of the five analyst positions are missing, preventing
a synthesised memo from resting on incomplete input.

## Skills

| Skill | Purpose | Used by |
|---|---|---|
| `investment-thesis` | Thesis format, evidence hierarchy, and conviction scoring | bull-analyst, financial-analyst |
| `pitch-evaluation` | 7-dimension rubric: team, market, product, traction, model, defensibility, capital efficiency | All analysts |
| `adversarial-review` | Structured critique protocol: cite specific claims, provide counter-evidence, classify risk severity | bear-analyst, all analysts in Round 2 |
| `dissent-protocol` | Chair's rules for preserving minority positions, DEALBREAKER surfacing, and decision block format | chair |

## Extending this example

- **Add a new analyst:** Create a new agent in `agents/`, add it to the `members` list in `swarms/board-room.yml`, and update the Chair's delegation instructions.
- **Change the quorum rule:** Edit `gates/quorum-gate/gate.py` — the logic is straightforward Python stdlib.
- **Add a legal analyst:** Copy `technical-analyst.md`, rename, change the scope section to cover IP, regulatory, and compliance dimensions.
