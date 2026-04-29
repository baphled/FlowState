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

The harness reads the surface once at run start (April 2026 In-Memory Default — the default content substrate) and the candidate flows as a STRING per trial. Each trial follows the same six steps:

1. **Read history.** The synthesised prompt's `# HISTORY` section carries the last N trial outcomes (score, kept flag, reason, candidate SHA). I do not query the coord-store directly; the harness has already rendered everything I need.
2. **Read the current substrate.** The synthesised prompt's `# SURFACE` section embeds the surface bytes verbatim. In default (content) mode this is the immutable surface read at run start; in `--commit-trials` mode it is the worktree's mirror at trial start. Either way, the prompt is canonical.
3. **Derive off-limits.** Per §4, build the off-limits set from the current frontmatter.
4. **Edit.** Produce one candidate edit. The edit may be small or large; what matters is that it does not violate §3 or §4 and has a plausible argument for moving the scalar in the favoured direction.
5. **Signal harness.** Reply with the full updated surface contents in a single fenced ` ```surface ` block. The harness extracts the block body, scores it, and ratchets. In default (content) mode the candidate is held entirely in memory; the surface file on disk is never touched. In `--commit-trials` mode the harness writes the candidate to the worktree's surface file, commits, and reverts on regression.
6. **Repeat.** The harness invokes me again with a fresh synthesised prompt; my history grows by one record.

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

## Writing a driver (default — content)

The harness invokes the driver script once per trial. The April 2026 In-Memory Default content substrate exchanges candidate strings via stdin/stdout; the driver does not write to the surface file on disk.

**Contract:**

1. **Stdin** — the synthesised per-trial prompt (4 sections — `# PROGRAM` / `# SURFACE` / `# HISTORY` / `# INSTRUCTION`). The same body is also written to `FLOWSTATE_AUTORESEARCH_PROMPT_FILE`; either channel is fine.
2. **Stdout** — the candidate, verbatim. The full string emitted to stdout is the candidate the harness scores. No fenced-block wrapping on the way out.
3. **Stderr** — free for diagnostics; captured by the harness for its log.
4. **Exit 0** — candidate produced. Non-zero is mapped to `validator-io-error` per plan § 4.5.
5. **Working directory** — the operator's invocation cwd. The harness no longer creates a worktree in default content mode; the surface env var is read-only by contract.
6. **Environment** — `FLOWSTATE_AUTORESEARCH_RUN_ID`, `FLOWSTATE_AUTORESEARCH_TRIAL`, `FLOWSTATE_AUTORESEARCH_SURFACE` (relative path; read-only), `FLOWSTATE_AUTORESEARCH_DRIVER_MAX_TURNS`, `FLOWSTATE_AUTORESEARCH_PROMPT_FILE`.
7. **Time budget** — `--driver-timeout` (default 3m) caps wall-clock. A timeout collapses onto `validator-io-error`.

**Reference example.** `scripts/autoresearch-drivers/default-assistant-driver.sh` wraps `flowstate run --agent default-assistant`, parses the agent's fenced ` ```surface ` block, and writes the candidate to stdout. Operators wanting a different driver shape (a research model, a local llama, a scripted heuristic edit) copy this script and edit the body — the env-var contract and the stdin/stdout convention are the only load-bearing parts.

## Writing an evaluator (default — content)

The harness invokes the evaluator script once per trial (and once at run start to score the baseline). Operators wire one in via `--evaluator-script <path>`. The contract is small but strict; deviations are recorded as `evaluator-contract-violation` and three consecutive violations terminate the run with `evaluator-contract-failure-rate`.

**Contract:**

1. **Stdin** — the candidate string (full). The same bytes are also written to `FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE`; either channel is fine.
2. **Stdout** — exactly one line, a non-negative integer in decimal. Trailing newline allowed; nothing else on stdout. Multi-line, empty, non-integer, or negative scalars are contract violations. Comparison logic for `--metric-direction max` is inverted by the harness (kept when `score > baseline`); the evaluator does NOT emit negative scalars to signal max-direction.
3. **Exit code** — `0` on successful scoring. Non-zero is a contract violation.
4. **Stderr** — free for diagnostic output. Captured but not parsed.
5. **Working directory** — the operator's invocation cwd (no worktree in default content mode).
6. **Environment** — `FLOWSTATE_AUTORESEARCH_RUN_ID`, `FLOWSTATE_AUTORESEARCH_CANDIDATE_FILE`. **No** `FLOWSTATE_AUTORESEARCH_SURFACE` in default content mode (the surface IS the candidate; reading the on-disk surface would defeat the substrate swap). Evaluator-specific env vars are fair game alongside the documented set.
7. **Time budget** — `--evaluator-timeout` (default 5m) caps wall-clock. SIGTERM at deadline, SIGKILL 30s later. A timeout records `evaluator_timeout_ms` on the trial outcome.

**Validation up front.** The harness checks `--evaluator-script` exists, is a regular file, and is executable before any work begins. Mis-typed paths fail fast.

**Reference examples.**
- `scripts/autoresearch-evaluators/planner-validate.sh` — wraps `validate-harness.sh --score --all` against the candidate manifest. Reads the candidate from stdin/env, stages it in a tempfile, runs the validator, returns the integer warning count. Pairs with `--metric-direction min`.
- `scripts/autoresearch-evaluators/bench.sh` — Go benchmark wrapper. Reads the candidate from stdin (drains it; the bench keys off compiled binaries), invokes `go test -bench=<name>`, parses `ns/op`, emits `1_000_000_000 / ns_per_op` so the score pairs with `--metric-direction max`. Knobs: `FLOWSTATE_AUTORESEARCH_BENCH_PKG`, `FLOWSTATE_AUTORESEARCH_BENCH_NAME`, `FLOWSTATE_AUTORESEARCH_BENCH_METRIC`.

**To pair with `--metric-direction min`** — emit a metric where lower is better (e.g. `planner-validate.sh` counts warnings; `bench.sh` can flip to `BENCH_METRIC=ns_per_op`).

## Opt in to git substrate (`--commit-trials`)

The legacy git-mediated substrate is preserved verbatim behind `--commit-trials`. Operators who want a kept-commit cherry-pick workflow (`flowstate autoresearch promote --apply`), or a git-native audit trail of every trial, add `--commit-trials` to the run command. The reference scripts for that mode live under the `-commit.sh` siblings:

- `scripts/autoresearch-drivers/default-assistant-driver-commit.sh` — driver writes the candidate to the surface file inside a worktree; harness commits per trial.
- `scripts/autoresearch-evaluators/bench-commit.sh` and `planner-validate-commit.sh` — evaluators read the on-disk surface from inside the trial worktree (today's cwd contract).

`--commit-trials` mode requires a clean parent tree (or `--allow-dirty` to stash); without `--commit-trials`, `--allow-dirty`, `--keep-worktree`, and `--worktree-base` hard-error at flag-parse time.

## Related skills

- `chain-id-resolution` — Coordination store key handling for any agent that reads autoresearch records directly (most do not; the harness handles it).
- `discipline` — Step-execution discipline that forbids silent shortcuts, including silently shrinking the off-limits set or "deferring" an edit that would otherwise violate §4.
- `pre-action` — Stop-and-clarify before the first edit of every trial: re-derive off-limits, then propose.

## KB Reference

`~/vaults/baphled/1. Projects/FlowState/Plans/Autoresearch Loop Integration (April 2026).md`
