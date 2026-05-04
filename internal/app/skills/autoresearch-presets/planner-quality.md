---
name: planner-quality
description: Autoresearch program preset for ratcheting planner-class manifests against the harness validate warning count; minimises warnings without weakening the always_active_skills baseline, the coordination_store wiring, or canonical chain-id resolution
category: Agent Coordination
tier: domain
when_to_use: Pass as `--program planner-quality` (or its absolute path) when running `flowstate autoresearch run` against a planner-class manifest surface; defaults assume `--metric-direction min` and the planner.md MVP shape
related_skills:
  - autoresearch
  - chain-id-resolution
  - discipline
---

# Preset: planner-quality

## What I do

I am a thin wrapper around the canonical `autoresearch` skill. I pin the
generic ratchet-loop prose to one concrete optimisation pass: drive the
`scripts/validate-harness.sh --score` count downwards on a planner-class
manifest by editing only the manifest's frontmatter and immediate prose.
The harness owns the loop; I own the constraints that keep each
candidate edit honest.

## Goal

Minimise the integer scalar emitted by `scripts/validate-harness.sh
--score` against the surface manifest. Lower is better. Equal scores
do not improve. The harness reads the metric direction; I do not
estimate scores.

## Constraints (planner-class)

These are additional to the off-limits set in
`skills/autoresearch/SKILL.md` § 4. Treat them as a strict superset.

1. **`always_active_skills` baseline preserved.** Every entry currently
   present in `capabilities.always_active_skills` MUST remain after my
   edit. Adding entries is allowed; reordering is allowed; renaming or
   removing is a surface violation.
2. **`coordination_store` wiring preserved.** The `coordination_store`
   block — including `chain_prefix`, `enabled`, and any nested
   `result_keys` — is structurally identical before and after every
   edit. Tightening prose around the block is fine; mutating the YAML
   keys or values is not.
3. **Canonical chain-id resolution preserved.** The chain-id derivation
   the planner relies on (per [[Chain ID Resolution]]) MUST continue
   to round-trip. If the manifest references a chain-id helper or
   convention, my edit MUST not alter that reference.
4. **No new dependencies.** Do not introduce new `tools`, new
   `mcp_servers`, or new always-active skills that were not already in
   the always_active_skills baseline.

## Score-gaming defence

The `--metric-direction min` setting makes deletions tempting: a
shorter manifest emits fewer warnings. Per N11 in plan v3.1 § 6.2,
deletion of fields from the off-limits set is the canonical gaming
vector. I derive the off-limits set from the live frontmatter at
every trial (per `skills/autoresearch/SKILL.md` § 4) and never cache
it across trials.

## Editing surface

Manifest frontmatter (YAML) and the immediate prose body are mine.
Code blocks inside the prose body are mine. Anything outside the
surface file is off-limits per the canonical skill's §3.

## Cross-links

- Canonical loop pattern: `skills/autoresearch/SKILL.md`.
- Plan note: [[Autoresearch Loop Integration (April 2026)]] § 5.10.
- Score gaming: [[Autoresearch Loop Integration (April 2026)]] § 6.2 N11.
