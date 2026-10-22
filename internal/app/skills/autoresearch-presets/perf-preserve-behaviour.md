---
name: perf-preserve-behaviour
description: Autoresearch program preset for ratcheting source-file performance surfaces against a benchmark scalar under `--metric-direction max`; preserves all exported signatures, test pass-rate, and zero new dependencies
category: Performance
tier: domain
when_to_use: Pass as `--program perf-preserve-behaviour` (or its absolute path) when running `flowstate autoresearch run` with `--evaluator-script scripts/autoresearch-evaluators/bench.sh` and `--metric-direction max` against a Go source file
related_skills:
  - autoresearch
  - benchmarking
  - performance
---

# Preset: perf-preserve-behaviour

## What I do

I am a thin wrapper around the canonical `autoresearch` skill, pinned
to one concrete optimisation pass: drive a benchmark scalar upwards on
a single Go source file by editing only that file. The harness owns
the loop; I own the constraints that keep each candidate edit
behaviour-preserving.

## Goal

Maximise the integer ops-per-second scalar emitted by
`scripts/autoresearch-evaluators/bench.sh` (Slice 5) against the
surface source file. Higher is better. Equal scores do not improve.
The harness reads the metric direction; I do not estimate scores.

## Constraints (source-file)

These are additional to the off-limits set in
`skills/autoresearch/SKILL.md` § 4 (which addresses manifest surfaces;
the source-file analogues are listed below).

1. **Exported signatures preserved.** Every exported function,
   method, type, and field that exists in the surface before my edit
   MUST continue to exist with the same signature afterwards. Renaming
   an exported symbol is a surface violation regardless of the
   benchmark improvement.
2. **Test pass-rate preserved.** The package's existing tests must
   continue to pass. The harness does not run tests for me — the
   evaluator is the benchmark — but breaking a test that exercises an
   invariant the benchmark does not is the canonical N11 gaming
   vector for `--metric-direction max`. Edits that cannot be reasoned
   to preserve test invariants are off-limits.
3. **Zero new dependencies.** No new `import` statements introducing
   modules not already imported by the surface. Re-using a module
   already in the file is allowed; pulling in a new package is not.
4. **No new files.** The surface is one file. Splitting work across
   sibling files is out of scope per `skills/autoresearch/SKILL.md`
   §3.

## Score-gaming defence

Under `--metric-direction max` the canonical gaming vector is faking
the scalar — for example, by short-circuiting the benchmarked path
behind a flag the benchmark sets but real callers do not. The
benchmark IS the surface contract; if a candidate raises ops/sec by
making the function do less observable work for any caller, that is
a behaviour change and a surface violation.

## Editing surface

The Go source file is mine end-to-end (imports, declarations, function
bodies, comments). Anything outside the surface file — including
sibling tests, build tags, generated files, and module metadata — is
off-limits per the canonical skill's §3.

## Cross-links

- Canonical loop pattern: `skills/autoresearch/SKILL.md`.
- Reference evaluator: `scripts/autoresearch-evaluators/bench.sh`
  (Slice 5).
- Plan note: [[Autoresearch Loop Integration (April 2026)]] § 5.10.
- Score gaming: [[Autoresearch Loop Integration (April 2026)]] § 6.2 N11.
