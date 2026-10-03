# Task: Persistence & Completeness Pre-Check Gate for Swarm Members

**Raised:** 2026-09-03
**Source:** n-vyro.io DD swarm engagement (chain `dd-swarm`), post-V11 diagnosis
**Priority:** Medium
**Area:** `internal/swarm/` (FlowState agent-platform)

## Problem

Two classes of run bug occurred during a gated due-diligence swarm run and were only caught by manual coordinator audit after delivery:

1. **Missing persisted output.** Tech-Lead had a registered HIGH-severity gate (`dd-tech-assessment`) but produced no coordination-store key at all. The run completed and delivered a report with the assessment section absent — no gate fired.
2. **Gameable keyword-coverage gate.** Security-Engineer drifted off-target (generic output, not target-specific) yet passed its gate because the gate checks keyword coverage / word counts, not target-specificity.
3. **Coordinator-mediated persistence workaround.** The Tech-Lead re-run session lacked the `coordination_store` tool entirely; the Coordinator had to persist its output on its behalf. Gated members without coordination-store write access silently fail the persistence contract.

Known context: "Swarm Determinism Gaps Review" (2026-08-21) already flagged gameable keyword-coverage gates.

## Proposed Fix

Add a **persistence/completeness pre-check gate** that runs before Phase 5 synthesis (or before any swarm's final-output gate):

- For every member with a registered gate, assert a coordination-store key exists at the member's contracted key path (e.g. `dd-swarm/tech-lead/assessment`) before synthesis is allowed. Missing key = hard block.
- Validate the key is non-empty and written by the member's own session (or explicitly flagged as Coordinator-mediated with a reason), not just present.
- Consider a target-specificity check: gate payload must reference target identifiers (repo names, file paths) supplied in the swarm brief, not generic boilerplate.

## Acceptance Criteria

- [ ] BDD scenario: swarm with a gated member whose key is absent fails synthesis with a clear error naming the member and key.
- [ ] BDD scenario: gate payload that is generic (no target references from the brief) is rejected or flagged.
- [ ] BDD scenario: Coordinator-mediated persistence is permitted only with an explicit reason annotation.
- [ ] `make check` green; worktree workflow per AGENTS.md.
