---
id: adr-merge-trailer-policy
title: ADR - Merge Trailer Policy
type: adr
status: accepted
adr_group: Go CLI Governance
created: 2026-11-18
modified: 2026-11-18T21:40
deciders:
  - Repository Owner
consulted:
  - Architecture Review Board
  - Admission Runner Service
informed:
  - FlowState Contributors
tags:
  - type/adr
  - project/flowstate
  - topic/architecture
  - topic/adr
  - topic/governance
  - topic/ci
  - topic/merge-admission
  - flowstate/governance
  - flowstate/adr
  - timeline/november-2026
aliases:
  - Merge Trailer Policy
  - Project-Metadata Trailer
  - Squash Merge Convention
---

# ADR - Merge Trailer Policy

## Status

**Accepted** — 2026-11-18. Enforced by `make merge-pr` (`scripts/merge_pr.sh`, PR #283), the Project Metadata Guard
  (CI), and `check-admission-commits` (pre-push hook). The `(#N)` subject-suffix convention was restored in PR #289
  after the initial tooling dropped it. The admission infrastructure this policy binds to is operated by the
  FlowState-owned GitHub App described in [[ADR - FlowState-Owned GitHub OAuth App]].

## Context

Every squash-merged commit on `next` must carry exactly one trailer:

```text
Project-Metadata: dropbox:/FlowState/Metadata/attempts/<attempt>/<record>.json
```

This trailer is the neutral external reference that binds the commit to its admission evidence — signed agent review,
  signed review-execution receipt, and the Dropbox read-back records — without leaking run-specific data (session IDs,
  actor identities, model attributions) into the repository. The repository carries only the reference; the evidence
  lives in Dropbox where it is versioned, access-controlled, and readable by the admission runner.

### The incident record

Before PR #283, squash merges were fragile. The repository squash defaults are PR_TITLE + BLANK — set deliberately to
  eliminate multi-trailer concatenation from multi-commit PRs (incident run 43116444053). A merge performed without
  explicit `--subject`/`--body` flags produced a trailer-less tip. The Project Metadata Guard and the Build check on
  the resulting `next`-push CI run both failed.

Five consecutive squash tips landed trailer-less before the root cause was addressed with dedicated tooling:

| Tip | Symptom |
|---|---|
| `00df6a15` | no trailer |
| `962be493` | no trailer |
| `16f97c95` | no trailer |
| `347411d0` | no trailer |
| (43116444053) | multi-trailer concatenation, the original incident |

Each failure was repaired by an evidence-seeded follow-up tip (`f7195dd1` among them) whose own admission record
  parent-bound to the damaged tip. The repetition is the lesson: manual `gh pr merge --squash` invocations with
  hand-crafted bodies fail under time pressure, and the failure is only detected after the push.

### Why the trailer matters

The trailer is not decorative. The admission runner and pre-push hooks extract it to:

1. **Locate** the Dropbox admission record for the commit.
2. **Parent-bind** — the record's `base_sha` must equal the commit's parent SHA, proving the evidence was generated
  for exactly this position in history.
3. **Authenticate** the signed agent review and review-execution receipt bound to the same head.
4. **Manifest-verify** the candidate source set (per-path SHA-256 + byte counts) against the actual commit diff —
  every changed path must be attested, and every attested path must match byte-for-byte.

Without the trailer, the commit has no verifiable admission chain; the Project Metadata Guard fails the push.

## Problem

1. **Manual merges fail silently.** `gh pr merge --squash` with defaults produces trailer-less tips; the failure
  surfaces only on the next-push CI run, after the damage is in history.
2. **Evidence and commit can drift.** A trailer pointing at a record parent-bound to a different base is worse than no
  trailer — it asserts evidence that does not apply.
3. **Traceability.** A squash tip's PR association is otherwise discoverable only via the GitHub API; history readers
  cannot see which PR produced a tip.
4. **Multi-trailer concatenation.** Squash bodies assembled from PR bodies with their own trailers produce multiple
  `Project-Metadata:` lines; the guard requires exactly one.

## Decision

**`make merge-pr PR=<n>` (`scripts/merge_pr.sh`) is the single tool for squash-merging admitted PRs onto `next`. It
  enforces the full admission contract and fails closed.**

### Enforcement sequence

1. PR is OPEN, not draft, MERGEABLE, base is `next`.
2. The `flowstate/merge-admission` status is SUCCESS on the **exact head SHA** — not on an ancestor, not on a rebase.
3. The head commit carries **exactly one** `Project-Metadata:` trailer matching the Dropbox attempts pattern.
4. The trailer is extracted and injected as `--body` to `gh pr merge --squash`.
5. The subject is the PR title (must be Conventional-Commit-compatible) with the `(#<PR number>)` suffix appended.
6. Every abort condition (zero/multiple trailers, admission not SUCCESS, non-mergeable, malformed trailer) exits
  non-zero **before** any merge call is made.

### The `(#N)` suffix

The squash subject carries `(#<PR number>)` so every merged tip on `next` is traceable to its PR without an API call.
  This matches the convention established by earlier packets (e.g. `feat(app): crash-recovery detection and recovery
  for session run (#296)`). The convention was briefly lost when the merge tooling first shipped (the tool set the
  subject from the PR title without the suffix) and was restored in PR #289.

### The merge-commit body

The squash commit body is the single `Project-Metadata` trailer — nothing else. Detailed commit messages (scope,
  rationale, test notes) live on the PR, not on the tip. This keeps tips parseable by the guard and the admission
  runner, and keeps the git log a clean index of admission-bound changes.

### The trusted review-execution reference

The signed review record must reference its review-execution receipt **by Dropbox path**
  (`dropbox:/FlowState/Metadata/attempts/.../review-execution-<sha>.json`). A local-filesystem path is not a trusted
  reference; the admission runner resolves execution receipts only inside the Metadata tree. This was learned
  empirically: an execution receipt signed and uploaded but referenced by local path fails admission with "missing
  trusted review execution reference".

## Consequences

### Positive

1. **Every `next` tip carries verifiable, parent-bound evidence.** The chain commit → trailer → Dropbox record →
  signed review → exact head is mechanically checkable end to end.
2. **The tooling fails closed.** Every abort fires before the merge; no partial state exists. The five-incident streak
  cannot recur while `make merge-pr` is the merge path.
3. **Traceability without API calls.** `git log` answers "which PR?" for every tip.
4. **Drift is detectable.** A rebase or amend invalidates the exact-head admission check by construction — the runner
  compares the status's head SHA to the PR's current head.

### Negative

1. **Merging is a tool invocation, not a one-liner.** Contributors must use `make merge-pr`; raw `gh pr merge` remains
  possible but produces tips the guard rejects.
2. **The Dropbox record must exist before merge.** The merge tool cannot mint evidence; the attestation flow (review →
  execution receipt → upload) is a prerequisite sequence.
3. **The trailer gate is structural, not semantic.** The guard verifies the reference shape and parent binding; it
  does not judge whether the underlying review was *good* — that is the reviewer session's job, corroborated by
  transcript checks at attestation time.
4. **Squash-only.** The policy admits no rebase or merge-commit path for PRs; contributors preferring linear
  multi-commit history lose it on `next`.

### Neutral

1. Release merges (`next` → `main`) are explicit merge commits, outside this policy — release history is preserved for
  changelog generation, and `main` is not admission-policed.
2. The `attempts/<attempt>/` directory naming is free-form (date-prefixed convention in practice); the guard validates
  the path pattern, not the naming scheme.
3. The trailer adds one line to each squash commit — negligible log noise for the guarantee it buys.

## Alternatives considered

### A. Direct push to `next` (no squash, no PR)

**Rejected.** Branch protection blocks it; even if allowed it bypasses the admission chain entirely. Every direct push
  would be an unevidenced change to the protected branch.

### B. Rebase merge

**Rejected.** Rebase preserves each PR commit in `next`'s history but produces N commits per PR, each requiring its
  own parent-bound trailer and its own admission record. The guard counts trailers per commit; a rebase-merged PR
  would need N evidence sets, one per commit, each parent-bound to its predecessor — significantly more machinery than
  one squash trailer, for no compensating benefit at this repo's scale.

### C. Merge commit per PR (no squash)

**Rejected.** The merge commit itself carries no trailer (it is not a content change), and the guard would need
  special-casing for merge commits versus content commits. It also breaks the linear-chain assumption in the admission
  runner's commit-walk verification. Reserved for releases only, where the history-preservation requirement dominates.

### D. Pre-commit hook enforcing the trailer

**Rejected as impractical.** The trailer references a Dropbox record keyed by the commit's SHA and parent — neither
  exists until the commit is created. A pre-commit hook would have to mint evidence for a SHA that will change with
  any amend. The enforcement point is correctly at merge time, when the final SHA is known.

### E. Trust CI status alone (no trailer; the check run is the evidence)

**Rejected.** CI runs are mutable, re-runnable, and their association to a tip is by head SHA only — a force-push that
  reuses a passing run's association is not detectable. The Dropbox record is immutable, versioned, and parent-bound,
  which is what makes the chain verifiable rather than merely green.

## Cross-links

### Prototype ADRs

- [[ADR - FlowState-Owned GitHub OAuth App]] — the admission runner publishes check statuses through the
  FlowState-owned GitHub App; the trailer is the pointer that lets the runner find the evidence behind each status.
- [[ADR - Dual-Scope Gate Runner]] — the gate-runner pattern (contract gates running against the exact PR diff) that
  the Lint/Build/Policy checks follow.
- [[ADR - Testing Strategy]] — CI integration requirements; the guard and admission checks are part of the mandatory
  Build prerequisite chain.

### Sibling ADRs (Go CLI Governance group)

- [[ADR - Schema Validation of JSON Output]] — the same fail-closed, contract-as-code philosophy applied to wire
  output rather than merge evidence.

## Sources

- `scripts/merge_pr.sh:10,35-82` — trailer regex, PR-state checks, admission SUCCESS check on exact head, subject
  suffix construction, abort paths.
- `.github/workflows/*` — Project Metadata Guard and merge-admission workflow definitions.
- Incident run IDs in history: `43116444053` (commit `ae3be195`, `rules/MERGE_ADMISSION.md`), `43947674928`,
  `43952081506`, `43954209411`, `44016713649` (commit `411530b6` message).
- Trailer-less tips verified in history: `00df6a15`, `962be493`, `16f97c95`, `347411d0`; repair tip `f7195dd1`.
- PR #283 (merge_pr tool), PR #289 (`(#N)` suffix restoration).
- Dropbox `rules/MERGE_ADMISSION.md` — the policy statement this ADR elaborates.

## Related Plans

- *Completed:* PR #283 (tool), PR #289 (suffix), the incident-repair sequence.
- *Ongoing:* every PR merges through `make merge-pr`; deviation requires an owner decision recorded in the Dropbox
  governance notes.
