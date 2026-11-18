# ADR-004: Merge Trailer Policy

## Status

Accepted (18 November 2026). Enforced by `make merge-pr` (scripts/merge_pr.sh, PR #283), the Project Metadata Guard (CI), and `check-admission-commits` (pre-push). The `(#N)` subject suffix convention was restored in PR #289 after the initial implementation dropped it.

## Context

Every squash-merged commit on `next` must carry exactly one `Project-Metadata: dropbox:/FlowState/Metadata/attempts/<attempt>/<record>.json` trailer. This trailer is the neutral external reference that binds the commit to its admission evidence (signed review, execution receipt, dropbox read-back records) without leaking run-specific data into the repository.

Before PR #283, squash merges were fragile. The repo squash defaults are PR_TITLE + BLANK (set to eliminate multi-trailer concatenation from multi-commit PRs, incident run 43116444053). A merge without explicit `--subject`/`--body` produced a trailer-less tip. The Project Metadata Guard and Build check on the next-push CI run both failed. Five consecutive squash tips (00df6a15, 962be493, 16f97c95, 347411d0, plus the original 43116444053 incident) landed trailer-less before the root cause was addressed with dedicated tooling.

### Why the trailer matters

The trailer is not decorative. The admission runner and pre-push hooks extract it to:

1. Locate the Dropbox admission record for the commit
2. Validate the record's parent binding (base_sha must match the commit's parent)
3. Verify the signed agent review and execution receipt bound to the same head
4. Check the candidate source manifest (per-path SHA-256 + byte counts)

Without the trailer, the commit has no verifiable admission evidence chain.

## Decision

`make merge-pr PR=<n>` (scripts/merge_pr.sh) is the single tool for squash-merging admitted PRs onto `next`. It enforces:

1. The PR is OPEN, not draft, mergeable, base=next
2. flowstate/merge-admission status is SUCCESS on the exact head SHA
3. The head commit carries exactly one Project-Metadata trailer matching the dropbox attempts pattern
4. The trailer is extracted and injected as `--body` to `gh pr merge --squash`
5. The subject is the PR title (must be Conventional-Commit-compatible) with the `(#<PR number>)` suffix for traceability
6. All aborts (zero/multiple trailers, admission not SUCCESS, non-mergeable, malformed trailer) exit non-zero before any merge call

### The (#N) suffix

The squash subject carries `(#<PR number>)` so every final merged commit on next is traceable to its PR. This matches the established convention from #278, #282, etc. Without the suffix, the commit's PR association is only discoverable via the GitHub API.

### The merge commit body

The squash commit body is the single Project-Metadata trailer. The detailed commit message (scope, changes, rationale) lives on the PR, not on the squash tip. This keeps the squash tip clean and parseable.

## Alternatives considered

**Direct push to next** is blocked by branch protection and would bypass the admission evidence chain anyway.

**Rebase merge** preserves each PR commit in next's history but produces N commits per PR, each requiring its own parent-bound trailer — significantly more complex than a single squash trailer and not attempted.

**Merge commit (no squash)** preserves branch topology but the Project Metadata Guard would need special handling (the merge commit itself has no trailer; the PR commits have trailers), which the guard's current implementation does not support.

**Pre-commit hook enforcement** is impractical: the trailer references a Dropbox record that doesn't exist until after the commit is created (the record references the commit's SHA) — a chicken-and-egg problem.

## Consequences

### Positive

Every squash-merged commit on next carries a verifiable, parent-bound admission evidence reference. The tooling fails closed: any missing or malformed evidence blocks the merge. The (#N) subject suffix makes every next commit traceable to its PR.

### Negative

The merge procedure requires explicit flags and a tool invocation (not a plain `gh pr merge`). The admission record must exist in Dropbox before the merge. The trailer-checking gate is file-based and doesn't verify semantic content of the record.

### Risks

If the Dropbox record is deleted after the merge, the trailer becomes a dangling reference — mitigated by Dropbox's version history and the admission runner's read-back verification. If the merge_pr.sh script has a bug that allows merges without proper validation, the evidence chain breaks — mitigated by the independent CI gate (Project Metadata Guard) that checks every next tip.

## References

- rules/MERGE_ADMISSION.md — rebase and squash trailer policy
- PR #283 (merge_pr tool), PR #289 (#N suffix)
- Incident runs: 43116444053, 43947674928, 43952081506, 43954209411, 44016713649
