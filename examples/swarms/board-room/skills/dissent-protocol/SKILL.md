---
name: dissent-protocol
description: How to preserve minority positions in the final investment memo
category: Domain — Investment
tier: domain
when_to_use: During Round 3 synthesis when the Chair writes the investment memo; ensures minority views are not erased by majority vote
related_skills:
  - adversarial-review
  - pitch-evaluation
---
# Dissent Protocol Skill

## Purpose

This skill governs how the Chair preserves minority positions in the final investment memo. The adversarial committee process only has value if dissenting views survive to the decision-maker. Majority-washing — presenting only the winning position — defeats the purpose of the board room protocol.

---

## Rule 1: Mandatory Dissent Entries

Any analyst whose final decision (after Round 2 revision) **differs from the majority verdict** MUST receive a DISSENT entry in the investment memo.

Majority verdict is determined by simple plurality across the five analysts' revised decisions (from `critiques/*.revised_decision`).

If three analysts vote `invest`, one votes `conditional`, and one votes `pass`:
- Majority verdict: `invest`
- Dissent 1: the analyst who voted `conditional`
- Dissent 2: the analyst who voted `pass`

---

## Rule 2: Dissent Entry Format

Each DISSENT entry in the investment memo MUST include:

```json
{
  "analyst_role": "string — e.g. 'Bear Analyst', 'Financial Analyst'",
  "decision": "invest|pass|conditional",
  "key_reasons": ["string — 2-3 specific reasons, not generic statements"],
  "most_compelling_evidence": "string — the single piece of evidence they found most persuasive"
}
```

**Rules for `key_reasons`:**
- MUST be specific claims, not generic risk categories
- BAD: "The market is risky"
- GOOD: "The TAM calculation assumes 100% addressability of UK SMEs, which is not credible — realistic TAM is 6-8x smaller than stated"

---

## Rule 3: Majority Must Address Dissent

The synthesis section of the investment memo MUST explicitly state **why the majority decision prevails over each dissent**. This is not a formality — it is a substantive argument.

Format: "The majority does not find [dissent reason] decisive because [specific counter-argument with evidence reference]."

If the Chair cannot articulate a substantive counter-argument to a dissent, this is a signal that the dissent may be correct, and the decision should be `conditional` rather than `invest`.

---

## Rule 4: DEALBREAKER Surfacing

If **any analyst** classifies any risk as **DEALBREAKER** (in either Round 1 or Round 2), it MUST appear in the Decision Summary of the investment memo regardless of the majority vote.

This applies even if the majority votes to invest. The Decision Summary must include:

```
DEALBREAKER RISKS RAISED (must be addressed before close):
- [risk name]: [description] — raised by [analyst role]
```

The final decision may still be `invest` if the majority believes the DEALBREAKER risk is addressable, but it MUST be visible.

---

## Rule 5: Decision Block Format

The final decision written to `board-room/{chainID}/decision` MUST use this exact JSON structure:

```json
{
  "decision": "invest|pass|conditional",
  "confidence": 1,
  "dissents": [
    {
      "analyst_role": "string",
      "decision": "string",
      "key_reasons": ["string"],
      "most_compelling_evidence": "string"
    }
  ],
  "conditions": ["string — only if decision is conditional; specific, actionable conditions"],
  "dealbreaker_risks": ["string — only if any analyst raised a DEALBREAKER; one entry per risk"]
}
```

**`confidence`** is the Chair's assessment of consensus strength, not a repetition of individual conviction scores:

| Score | Definition |
|---|---|
| 1 | Deeply divided committee — split vote, strong dissents, significant DEALBREAKER risks |
| 2 | Majority with significant dissent — one or two strong dissenting positions |
| 3 | Majority with manageable dissent — dissents are specific and addressable |
| 4 | Strong majority — dissents are minor or no DEALBREAKER risks |
| 5 | Near-unanimous — all five analysts agree on decision and direction |

---

## Anti-patterns

- **Averaging out dissent:** "Most analysts were positive, with some concerns noted." This erases the dissent. Use the DISSENT entry format above.
- **Unnamed dissents:** "One analyst was sceptical." Name the role.
- **Ignoring DEALBREAKER surfacing:** If the bear-analyst raised a DEALBREAKER and the Chair omits it from the memo because the majority voted to invest, this is a protocol violation.
- **Conditions without specificity:** `conditions: ["needs more diligence"]` is not a valid condition. Conditions must be specific and verifiable: "Requires audited financial statements covering last 2 fiscal years" or "Requires reference calls with 3 named enterprise customers."
