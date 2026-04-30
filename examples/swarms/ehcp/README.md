# EHCP Annual Review Swarm

An example FlowState swarm that demonstrates a real-world statutory workflow: the UK EHCP (Education, Health and Care Plan) Annual Review under the Children and Families Act 2014.

## What It Does

The swarm coordinates six specialist agents to complete a full Annual Review cycle:

1. **EHCP Coordinator** (lead) — initialises the case file and orchestrates the chain
2. **School Liaison** — synthesises the school's progress assessment and recommendations
3. **Family Advocate** — represents the family and child, actively challenging insufficient provisions
4. **Specialist Reviewer** — consolidates multi-agency health, social care, and EP input
5. **Drafter** — produces a complete draft EHCP (all sections A–K) and outcome letter
6. **Compliance Checker** — validates the draft against the SEND Code of Practice, flagging every non-compliant provision

A post-member gate (`ehcp-completeness`) blocks the compliance checker from running until the drafter has produced all 11 EHCP sections.

A post-swarm gate (`personal-data-security`) runs in BLOCK mode — EHCP data must never leave the local environment unredacted.

## Install

```bash
cp -r examples/swarms/ehcp/* ~/.config/flowstate/
cp -r examples/gates/ehcp-completeness ~/.config/flowstate/gates/
```

## Run

```bash
flowstate swarm run ehcp
```

The coordinator will ask for the child's details (name, school, review date). The full workflow runs end-to-end.

## Validate

```bash
flowstate swarm validate ehcp
```

## Test the Gate Directly

Fail case (missing sections):
```bash
echo '{"kind":"ehcp-completeness","payload":{"sections":{"A":"present","B":"present"}}}' \
  | ~/.config/flowstate/gates/ehcp-completeness/gate.py
```

Pass case (all sections):
```bash
echo '{"kind":"ehcp-completeness","payload":{"sections":{"A":"views","B":"needs","C":"health","D":"social","E":"outcomes","F":"ed provision","G":"health prov","H":"social prov","I":"placement","J":"budget","K":"appendices"}}}' \
  | ~/.config/flowstate/gates/ehcp-completeness/gate.py
```

## Local Model Configuration

EHCP data is sensitive personal data. Configure agents to use a local Ollama model to ensure data does not leave your infrastructure. Add the following to your `~/.config/flowstate/config.yml`:

```yaml
agent_models:
  ehcp-coordinator: qwen3:14b-32k
  ehcp-school-liaison: qwen3:14b-32k
  ehcp-family-advocate: qwen3:14b-32k
  ehcp-specialist-reviewer: qwen3:14b-32k
  ehcp-drafter: qwen3:14b-32k
  ehcp-compliance-checker: qwen3:14b-32k
```

The `personal-data-security` gate is configured in BLOCK mode and will halt the swarm if PII is detected in output leaving the local environment.

## Adversarial Agents

Two agents play explicitly adversarial roles:

- **Family Advocate** (`ehcp-family-advocate`): Challenges school assessments, flags vague provisions, and represents the child's interests. Always produces pushback signals — a clean pass from this agent indicates insufficient scrutiny.
- **Compliance Checker** (`ehcp-compliance-checker`): Validates the draft EHCP against the SEND Code of Practice with maximum scepticism. A zero-objection report on a first-pass draft almost always means the checker was not thorough enough.

## Domain Background

An EHCP is a UK statutory document for children with Special Educational Needs. The Annual Review is a legally mandated yearly process regulated by:
- Children and Families Act 2014
- SEND Code of Practice 2014 (updated 2015)
- Special Educational Needs and Disability Regulations 2014

Key statutory deadlines enforced by this swarm:
- Outcome letter issued within 2 weeks of review meeting
- 15-day family consultation window if amending
- Final amended plan issued within 8 weeks of meeting
- Next review date set 12 months from current meeting date

## EHCP Sections Validated by the Gate

| Section | Content |
|---------|---------|
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

## coordination_store Flow

```
ehcp-coordinator     →  ehcp/{chainID}/case-file          (AR case details)
ehcp-school-liaison  →  ehcp/{chainID}/school-view
ehcp-family-advocate →  ehcp/{chainID}/family-view        (+ pushback signals)
ehcp-specialist-rev  →  ehcp/{chainID}/specialist-input
ehcp-drafter         →  ehcp/{chainID}/draft-ehcp         (ehcp-completeness gate fires)
ehcp-compliance-chk  →  ehcp/{chainID}/compliance-report
ehcp-coordinator     →  ehcp/{chainID}/final-outcome-letter
                                                           (personal-data-security gate fires)
```
