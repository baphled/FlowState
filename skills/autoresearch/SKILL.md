---
name: autoresearch
description: Drive the autoresearch loop — propose a candidate edit to the surface, let the harness ratchet on a scalar metric, repeat until convergence; never mutate anything outside the surface and never weaken score-bearing manifest fields
category: Agent Coordination
tier: domain
when_to_use: Always-active for any agent invoked by `flowstate autoresearch run` as the program-of-record driver; opt in by declaring `autoresearch` (or a preset that wraps it) in `always_active_skills`
related_skills:
  - chain-id-resolution
  - discipline
  - pre-action
---

# Skill: autoresearch

## What I do

I am the program of record for a single autoresearch run. The harness (`flowstate autoresearch run`) cares only about scalar improvement against a fixed surface; I am the prose that turns a generic ratchet loop into a concrete optimisation pass. Every preset (`perf-preserve-behaviour`, `skill-prose-ratchet`, ...) is a thin wrapper that pins one or two of the sections below to a specific surface or scalar — the harness itself never reads those wrappers, only this skill body via the engine's existing skill loader.

## Goal

Minimise (or maximise) the scalar reported by the configured evaluator against the surface file at `--surface`. The harness drives the loop; my job per trial is to produce one candidate edit to the surface that has a credible chance of moving the scalar in the favoured direction without violating the off-limits rules in §4.

For the MVP shape — `--surface internal/app/agents/planner.md`, evaluator `scripts/validate-harness.sh --score`, `--metric-direction min` — the scalar counts harness warnings emitted across hypotheses H1–H5 in `scripts/validate-harness.sh`. Lower is better.

The harness is generic. The prose in this skill is what makes any individual run concrete. Do not hard-code surface paths, file lists, or hypothesis labels into preset wrappers; derive everything possible from the live surface at trial time.

## Scalar to optimise

The evaluator script the harness invokes prints exactly one non-negative integer to stdout and exits 0 on success. Anything else is an evaluator-contract violation and the harness records `reason=evaluator-contract-violation` for the trial.

`--metric-direction min` (the MVP default) means lower is better; the harness keeps a kept commit only when its score is strictly less than the running best. `--metric-direction max` flips the comparison; identical otherwise. Equal scores never improve.

The evaluator and the metric direction are read by the harness, not by me. I do not call the evaluator. I do not estimate scores. I propose an edit and let the harness decide.

## Mutable surface constraint

I MAY edit only the file at `--surface`. Editing any other file inside the worktree — including config files, sibling manifests, scripts, tests, or documentation — aborts the trial with `reason=surface-violation` and the candidate is reverted before scoring.

The surface is a single file. Slicing it across multiple files is out of scope for the MVP. If a preset later needs multi-file surfaces, that is a harness change, not a prose change.

## Off-limits surface fields (surface-derived)

For manifest surfaces (the MVP shape), the driver MUST NOT delete entries from any of the following frontmatter fields:

- `id`
- `schema_version`
- `capabilities.tools`
- `capabilities.always_active_skills` — every entry currently listed
- `delegation.delegation_allowlist`
- `delegation.can_delegate`
- `coordination_store`
- `harness`
- `harness_enabled` (when present)

Two precise rules govern this list:

1. **Surface-derived, not memorised.** At every trial, I re-read the surface manifest's frontmatter and derive the off-limits set from what is currently there. The list above is the schema of what to look for, not a hard-coded inventory of values. If a manifest currently lists `[discipline, chain-id-resolution]` under `always_active_skills`, both entries are off-limits for that trial; if a future manifest lists three, all three are off-limits.
2. **No prose memorisation.** I do not cache the off-limits list across trials. Memorisation is the entry vector for score-gaming — if a previous trial deleted a field and was reverted, the next trial sees the field present again and treats it as off-limits again.

The off-limits derivation runs in my head before every edit. Skipping it because "the manifest hasn't changed" is a contract violation.

## Trial protocol

Each trial follows the same six steps:

1. **Read history.** Look at the last N entries from `autoresearch/<runID>/seen-candidates` (the SHA ring) and any `autoresearch/<runID>/trial-*` records I can reach. The harness writes these; I never need to invent the keys.
2. **Read current surface.** The surface file is the canonical state. Whatever is on disk in the worktree at trial start is what I edit from.
3. **Derive off-limits.** Per §4, build the off-limits set from the current frontmatter.
4. **Edit.** Produce one candidate edit. The edit may be small or large; what matters is that it does not violate §3 or §4 and has a plausible argument for moving the scalar in the favoured direction.
5. **Signal harness.** Exit cleanly. The harness commits, scores, and ratchets.
6. **Repeat.** The harness invokes me again with the same protocol; my history grows by one record.

I never invoke `git`, the evaluator, or the coordination store directly. The harness owns those operations.

## Convergence rule

The harness terminates when any of the following fires; I do not need to track this myself, but I should understand the matrix because it shapes how aggressive an edit to attempt:

- `max-trials` — the operator's hard cap.
- `time-budget` — wall-clock cap.
- `converged` — N consecutive trials with `reason=no-improve` (default N=5). After three consecutive no-improves, prefer a more exploratory edit; the loop will close itself out if I cannot find improvement.
- `fixed-point-saturated` — the SHA ring is pinned (default K=10). If I keep proposing the same edit, the harness will tell me with a stream of `fixed-point-skipped` reasons; vary the edit.
- `manifest-gate-failure-rate` — three consecutive `manifest-validate-failed` trials. I am breaking the manifest schema; revisit §4.
- `evaluator-contract-failure-rate` — three consecutive evaluator violations. Not my problem; the operator's evaluator is broken.

## Score-gaming prohibition

The scalar exists to surface real warnings. I MAY NOT delete or weaken manifest fields whose purpose is to surface those warnings. The MVP evaluator is `validate-harness.sh`; its hypotheses correspond to manifest features as follows:

- **H1** — tool-JSON leak. Fires when an assistant turn has empty `ToolCalls` and Content matching tool-call JSON fragments. Manifest correlate: the agent's `capabilities.tools` block must list the tools the body references; deleting a tool entry to "silence" H1 is forbidden.
- **H2** — manifest declares tools but produces zero `tool_use` invocations. Manifest correlate: the same `capabilities.tools` block. Deleting tool entries to make H2 stop firing is forbidden.
- **H3** — `always_active_skills` declared in the manifest but missing from the session's loaded skills. Manifest correlate: `capabilities.always_active_skills`. Removing entries to "match" what loaded is forbidden; the fix is to make the loader honour the declaration.
- **H4** — empty assistant turns (no Content, no ToolCalls). Manifest correlate: `harness` / `harness_enabled` and the prompt body. The fix is the prompt, not the manifest gate.
- **H5** — single-turn termination for orchestration agents. Manifest correlate: `delegation.can_delegate` and `delegation.delegation_allowlist`. Removing delegation declarations to dodge H5 is forbidden.

If an edit's only mechanism for moving the scalar is deleting one of these fields, the edit is a score-gaming violation and the manifest gate will catch it. Don't.

## What you cannot edit

A single restatement, in priority order:

1. The surface-derived off-limits set from §4 — every field listed there, every entry currently present, re-derived per trial.
2. The evaluator script. It is the source of truth; my job is not to renegotiate the metric.
3. This skill body. The program of record is the prose the harness already loaded; I do not edit it during a run.
4. Anything outside the file at `--surface`. The worktree contains many tempting files; none are mine to touch.

## Related skills

- `chain-id-resolution` — Coordination store key handling for any agent that reads autoresearch records directly (most do not; the harness handles it).
- `discipline` — Step-execution discipline that forbids silent shortcuts, including silently shrinking the off-limits set or "deferring" an edit that would otherwise violate §4.
- `pre-action` — Stop-and-clarify before the first edit of every trial: re-derive off-limits, then propose.

## KB Reference

`~/vaults/baphled/1. Projects/FlowState/Plans/Autoresearch Loop Integration (April 2026).md`
