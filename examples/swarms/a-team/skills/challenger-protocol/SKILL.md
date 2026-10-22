---
id: challenger-protocol
name: Challenger Protocol
version: "1.0.0"
description: >
  A structured adversarial review framework for the critic agent. Wraps the
  built-in critical-thinking skill with A-Team specific rules about what to
  challenge, how to rate objections, and how to avoid rubber-stamping.
---

# Skill: Challenger Protocol

This skill is always active for the critic agent. It governs when and how to challenge the strategist's output to prevent groupthink and ensure the writer receives substantive, actionable critique.

## Core Principle

A critique that finds nothing wrong is almost certainly wrong itself. Every strategy has at least one assumption that could be challenged. The protocol exists to ensure you find it.

---

## What to Challenge

Challenge in this order of priority:

### 1. Core Assumptions (required)
The strategist lists their assumptions explicitly. Your first obligation is to challenge at least one core assumption — an assumption that, if wrong, would materially change one or more recommendations.

Ask: *"What would have to be true for this assumption to be wrong? How likely is that? What happens to the recommendation if it is wrong?"*

### 2. Evidence Gaps
Is there a relevant angle the researcher did not cover? Is the strategist's interpretation of the research the only reasonable one? Is there contradicting evidence in the research that the strategist glossed over?

Ask: *"What evidence would change this recommendation, and did the researcher look for it?"*

### 3. Failure Modes
For each recommendation, what does failure look like? Is the primary risk identified by the strategist actually the most dangerous one?

Ask: *"In what scenario does this recommendation make things worse rather than better?"*

### 4. Scope and Framing
Did the strategist solve the right problem? Is there a more important version of this question that the task-plan framing missed?

Ask: *"Is the coordinator's framing of this task the right one, or does it lead to solving the wrong problem well?"*

---

## Conviction Rating

Rate each objection 1-5:

| Rating | Meaning |
|---|---|
| 1 | Minor concern — may not matter |
| 2 | Worth noting — small chance of significant impact |
| 3 | Material risk — real chance of significant impact if ignored |
| 4 | High concern — likely to cause problems; recommend the writer address it |
| 5 | Strategy-breaking — if this assumption is wrong, the recommendation collapses |

---

## Classification

Each objection must be classified as one of:

- **breaks-strategy**: If this objection is correct, the recommendation as stated should not be followed. The writer must revise, not just caveat.
- **material-risk**: The recommendation can stand, but the writer must acknowledge this risk prominently and address mitigation.
- **worth-noting**: The objection is real but doesn't change the recommendation. The writer may add a footnote or caveat.

---

## The Red Flag Check

Before submitting your critique, check: *Do I have at least one objection rated `breaks-strategy` or `material-risk`?*

If NO — this is a red flag. Do not immediately conclude the strategy is robust. Instead:
1. Re-read the assumptions. Did you challenge the most important one?
2. Re-read the research. Is there contradicting evidence the strategist didn't engage with?
3. Consider scope: is the strategy solving the wrong problem?

If after a genuine second pass you still have nothing above `worth-noting`, you may submit — but you MUST include a `## Red-Flag Check` section explaining specifically why the strategy is robust on each axis that matters. "I couldn't find a problem" is not sufficient. Name what you tried to break.

---

## Rubber-Stamp Patterns to Avoid

The following patterns indicate the critic did not engage:

1. **"Overall this looks good"** — aggregate approval without specific engagement.
2. **"Minor concerns only"** — if every concern is minor, you haven't looked hard enough.
3. **Critiquing formatting or style** — the critic's job is logical and evidential, not editorial.
4. **Objections without specificity** — "this could be wrong" is not an objection. "This assumes X, which is contradicted by the research finding Y, which would mean Z" is an objection.
5. **Conviction inflation** — rating everything 4-5 makes the signal useless. Save 4-5 for objections where you genuinely believe the strategy is wrong.
6. **Agreeing with the strategist's own caveats** — finding risks the strategist already flagged is not a critique. Find what they missed.

---

## Writer's Rights

The writer is permitted to disagree with your objections. This is expected and healthy. What the writer may NOT do is ignore your objections without explanation. If the writer rebuts your critique, they must:
- State which objection they are rebutting
- Provide specific evidence from the research
- Explain why the rebuttal changes the classification

A writer who rebuts a `breaks-strategy` objection with evidence has done their job. A writer who ignores it has not.
