# CI and merge-admission contract

CI status is evidence for the **exact candidate commit**, not an M1/T019
qualification, agent identity, independent review or permission to merge.
Current owner and agent policy lives in Dropbox `/FlowState/Dev`; attempt,
reviewer and acceptance records live in `/FlowState/Metadata`.

## Protected next branch

- Start only from current `origin/next` on a scoped feature worktree; no
  direct writes to `next` or `main`. Target `next` in every product PR.
- Enforce the committed 500-line, 10-path, 128 KiB review budget including
  documentation, tests and generated files. No waivers in the changed files.
- The protected `Build` status is a **fail-closed aggregate**, not only
  binary compilation. It needs success from scope/metadata guards,
  source/lint, all supported test platforms, security, conventional
  commits and PR branch/title policy checks.
- Fail Build on missing, skipped, cancelled, neutral or failed
  prerequisites. For a push to `next`, commitlint still checks commits;
  PR-only fields are not applicable and the PR Policy job succeeds
  without faking a PR event.
- The independent `PR Validation` workflow supplies early feedback,
  but its status is not guaranteed by branch protection unless included
  in a protected aggregate or configured as a required check. Do not
  infer enforcement from a workflow merely existing.
- Inject untrusted PR titles, commit messages, branch names and tool
  output only as data via environment variables or validated files,
  never via inline Bash or JavaScript source interpolation.
- Security workflows must check the actual `next` branch and release
  branch, and scanner/toolchain versions should be pinned where
  feasible. A scanner regression pass is not a vulnerability clearance.

## Approval and evidence

Before any merge, refetch the PR's exact head SHA, base, real required
check results and unresolved threads; demand an independent authorised
reviewer record attached to that exact candidate and source set.
No agent can approve itself, invent native GitHub approvals or substitute
a summary for executed evidence. A SHA drift invalidates affected checks.

Persist observed identity, model/provider, command outputs, reviewer verdict,
budget and immutable acceptance records in Dropbox Metadata. Git commits
contain only a neutral `Project-Metadata` reference. Changes to the
CI validators themselves require negative controls that exercise failures
and skips. Do not weaken checks to resolve a blocked PR.

## Verification

```sh
make check-size-live
make check
make check-pr-size BASE_SHA=<actual-next-base>
make check-code-quality BASE_SHA=<actual-next-base>
make check-documentation BASE_SHA=<actual-next-base>
python3 -B -m unittest discover -s scripts/tests -p test_required_build.py -v
```

This rule cannot change server-side protection settings. Verify the
effective GitHub ruleset and required context names independently.
