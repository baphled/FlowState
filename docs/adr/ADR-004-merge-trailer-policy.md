# ADR-004: Merge Trailer Policy

## Status

Accepted (18 November 2026). Enforced by `make merge-pr` (scripts/merge_pr.sh, PR #283), the Project Metadata Guard (CI), and `check-admission-commits` (pre-push). The `(#N)` subject suffix convention was restored in PR #289.

## Context

Every squash-merged commit on `next` must carry exactly one `Project-Metadata: dropbox:/FlowState/Metadata/attempts/<attempt>/<record>.json` trailer. This trailer is the neutral external reference that binds the commit to its admission evidence (signed review, execution receipt, dropbox read-back records) without leaking run-specific data into the repository.

The repo squash defaults are PR_TITLE + BLANK (set to eliminate multi-trailer concatenation from multi-commit PRs, incident run 43116444053). A merge without explicit `--subject`/`--body` produces a trailer-less tip. The Project Metadata Guard and Build check on the next-push CI run both fail. Five consecutive squash tips landed trailer-less before the root cause was addressed with dedicated tooling.

## Decision

`make merge-pr PR=<n>` is the single tool for squash-merging admitted PRs onto `next`. It enforces the PR state (OPEN, not draft, mergeable, base=next), the flowstate/merge-admission status (SUCCESS on the exact head SHA), the head commit carries exactly one Project-Metadata trailer matching the dropbox attempts pattern, the trailer is extracted and injected as `--body` to `gh pr merge --squash --subject "<title> (#<n>)" --body "<trailer>"`, and the subject passes a Conventional-Commit check. All aborts exit non-zero before any merge call.

The `(#<n>)` suffix carries the PR number for traceability. The squash commit body is the single Project-Metadata trailer; the detailed commit message lives on the PR.

## Alternatives considered

Direct push to next: blocked by branch protection. Rebase merge: preserves each PR commit but produces multiple commits per PR, each requiring its own parent-bound trailer. Merge commit (no squash): the guard doesn't handle merge commits. Git hooks for pre-commit enforcement: the trailer references a Dropbox record that doesn't exist until after the commit.

## Consequences

Positive: every squash-merged commit carries a verifiable admission evidence reference. The tooling fails closed. The (#N) subject makes every next commit traceable to its PR.

Negative: the merge procedure requires the tool invocation. The admission record must exist in Dropbox before the merge. The gate is file-based and doesn't verify semantic content.

Risks: if the Dropbox record is deleted, the trailer becomes dangling — mitigated by version history. If merge_pr.sh has a validation bug, the evidence chain breaks — mitigated by the independent CI gate.

## References

rules/MERGE_ADMISSION.md, PR #283 (merge_pr tool), PR #289 (#N suffix).
