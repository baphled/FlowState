---
name: adversarial-review
description: Structured critique protocol for bear-analyst and peer review rounds
category: Domain — Investment
tier: domain
when_to_use: When critiquing any investment claim; in peer review rounds; when the bear-analyst constructs their position
related_skills:
  - pitch-evaluation
  - critical-thinking
  - dissent-protocol
---
# Adversarial Review Skill

## Purpose

This skill defines the structured critique protocol for the bear-analyst and for all analysts during the Round 2 peer review. Every critique must meet the specificity and actionability standards below. Vague objections are not useful; they waste committee time and obscure real risks.

---

## Rule 1: Cite the Specific Claim

Every critique MUST identify the **specific claim** being challenged:

- BAD: "I'm not convinced by the market analysis."
- GOOD: "The market analyst claims a £4.2B TAM derived from bottom-up calculation, but the stated ACV of £35k applied to 120k UK SMEs assumes 100% addressability — the realistic subset of SMEs with procurement digitisation budgets is closer to 15%, reducing the realistic TAM to ~£630M."

Name the source position (by anonymised label in peer review: "Analyst A claims..."), the specific figure or assertion, and why it fails.

---

## Rule 2: Provide Counter-Evidence or Reason for Doubt

Every objection MUST include one of:

1. **Counter-evidence** — a specific data point, comparable company outcome, or cited methodology that contradicts the claim
2. **Logical refutation** — a step-by-step argument showing the claim does not follow from its premises
3. **Missing evidence** — an explicit statement of what evidence would be required to make the claim credible, and why its absence is material

Generic scepticism ("I just don't believe this") is not an adversarial review. It is noise.

---

## Rule 3: Conviction Threshold

Rate conviction for each critique 1–5 using the `investment-thesis` scale.

**Do not include critiques with conviction < 2.** Including low-conviction objections dilutes the signal and wastes committee attention.

---

## Rule 4: Risk Classification

Every objection MUST be classified into one of three tiers:

| Classification | Definition | Required Action |
|---|---|---|
| **DEALBREAKER** | This risk, if accurate, would prevent investment regardless of other factors | Must appear in the final investment memo even if the majority votes to invest |
| **MATERIAL RISK** | This risk must be addressed or mitigated before investment can proceed | Must appear in the conditions list if decision is `conditional` |
| **MANAGEABLE** | This risk is real but can be monitored and does not prevent investment | May appear in the memo at the Chair's discretion |

---

## Rule 5: Avoid Vague Negatives

These phrases are BANNED in adversarial reviews:

- "I'm not sure about..."
- "This seems risky..."
- "I have concerns about..."
- "The numbers don't look right..."

Replace each with a specific, testable claim. If you cannot make the objection specific, do not include it.

---

## Rule 6: Peer Review Engagement Requirements

In Round 2 (anonymous peer review), each analyst MUST:

1. Engage with **at least 2** other analysts' positions
2. For each engagement:
   - State the anonymised analyst label (Analyst A, Analyst B, etc.)
   - Quote or closely paraphrase the specific claim being engaged
   - State clearly: AGREE / DISAGREE / PARTIAL AGREEMENT
   - Provide specific reasoning — why do you agree or disagree?
   - If disagreeing: classify the disagreement (DEALBREAKER / MATERIAL RISK / MANAGEABLE)

**Minimum engagement:** 2 positions, each with specific reasoning and classification.
**Optional:** Engage with more positions if you have substantive views.

---

## Anti-patterns

- **Kitchen-sink critique:** Listing 15 vague risks hoping some stick. Include only risks with conviction >= 2 and specific evidence.
- **Circular scepticism:** "We can't be sure the market will grow" when the analyst has already presented growth data — address the data, not the general uncertainty.
- **Ad hominem on team:** "The founders look young" is not a substantive risk without specific evidence of capability gaps.
- **Asymmetric standards:** Demanding primary data evidence from bull positions while accepting speculative reasoning in bear positions. Apply the same evidence hierarchy to all claims.
