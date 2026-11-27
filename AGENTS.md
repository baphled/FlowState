# FlowState contributor and agent bootstrap

Before starting, delegating or resuming, read the actual current Dropbox files:

1. `/FlowState/Dev/00-AGENT-READ-FIRST.md` and `/FlowState/Dev/AGENTS.md`.
2. Current owner decisions, code/documentation contract and relevant M1 contracts.
3. `/FlowState/Metadata/ACTIVE.json`, the assigned packet and referenced evidence.

Pass verified source text/hashes and the bounded contract to children; do not
assume prompt inheritance. Missing sources, changed scope or failed checks stop
writes to the affected packet. Follow current roles, one item per agent and shared
budgets. The coordinator arranges genuinely separate review.

## Source and documentation contracts

Read [code ownership](rules/ARCHITECTURE.md), [documentation standards](rules/DOCUMENTATION.md)
and [Ginkgo testing](rules/BDD_WORKFLOW.md). Keep domain/port/adaptor boundaries,
explicit composition and cohesive packages. Named types belong in owning-package
`types.go` or `*_types.go`; implementation and mutable variables stay elsewhere.
No catch-all types/common/util packages or inline rule waivers.

Document public APIs, invariants, ownership, errors and concurrency/cancellation
where relevant. Update affected guides with the code. Check local links and
examples against the candidate. A linter cannot establish semantic quality.

Use Ginkgo v2/Gomega with one behavioural expectation per spec, explicit setup
checks, isolated fixtures, cleanup and parallel randomized race tests. Preserve
failure coverage during refactoring. No Cucumber/Godog/Gherkin or new plain TestX
behavioural functions. Read the current Dropbox test rule before test work.

## Continuous SDLC reconciliation

Reconciliation is a lifecycle invariant, not a one-time planning exercise.
For FlowState the integration target is the live remote `origin/next`.
For other projects use their explicitly configured integration target.
A recorded SHA is evidence of a past observation, never the current authority.

At every checkpoint, fetch or resolve the actual remote target again. Compare
its current SHA and changed contracts against the last reconciled revision.
A local branch, cached SHA, plan date or successful earlier check is not proof
of freshness. Failure to resolve the remote or required evidence blocks the
affected transition; do not infer that nothing changed.

### Checkpoints and evidence

- **Discovery and planning:** resolve the target before research, distinguish
  delivered, partial and missing behaviour using current source and tests, and
  reconcile relevant requirements, decisions, existing research and dependencies.
  Recheck before submitting the plan for independent review.
- **Admission and dispatch:** resolve the target before admission, before each
  packet dispatch and on resumption. Compare approved plan bytes, permitted
  paths, dependency receipts and current implementation. Do not dispatch work
  already delivered by another packet. Preserve the approved revision.
- **Execution:** reconcile when relevant upstream changes, dependency changes or
  gate failures are detected, and before the next packet transition. Stop affected
  writes if the work is no longer in its approved scope; preserve candidate work.
- **Pre-push and pre-merge:** reconcile with the actual current target and exact
  candidate. Refresh affected parent-bound evidence and independent review after
  a rebase or amendment. Require local checks, exact-head CI and live admission
  under the existing contracts; a prior green status cannot grant fresh admission.
- **Release and closeout:** resolve the current integration target and configured
  release target, compare against the last released version and exact candidate,
  then verify the published artefacts and outstanding requirements. A merge alone
  is not evidence of release or capability acceptance.

Record checkpoint, repository, target ref, previous and freshly resolved SHA,
candidate SHA where applicable, approved plan and packet identity/digest, affected
requirements, dependencies, source and evidence changes, disposition and required
next action in Dropbox Metadata. Record the actual observation time and evidence
references; never overwrite the immutable approval or invent observations.

### Reconciliation outcomes

- **Current:** relevant scope and evidence still hold; proceed within admission.
- **Non-material drift:** document why the change does not affect approved
  behaviour, scope, dependencies or oracles; refresh applicable evidence and
  proceed. This does not waive exact-candidate review or required CI.
- **Material drift:** block affected work, revise only affected plan/packet scope
  and obtain separate independent approval before resuming implementation.
- **Conflict or unknown:** block the affected transition, retain evidence and
  resolve the conflict or missing input. Continue unrelated admitted,
  dependency-ready work; do not invalidate a whole plan solely because HEAD moved.

A new target SHA alone is not a material change. Changes to required behaviour,
contracts, gates, ownership, dependencies, release compatibility or acceptance
oracles are material when they affect the packet. If impact cannot be established,
fail closed for that packet. Recheck relevant remote state immediately before
authorising a transition; a concurrent change requires renewed reconciliation.

### Release management in every plan

Each plan explicitly identifies release impact or records evidence that none
applies. Specify versioning and compatibility, schema/data migrations and
downgrade constraints, dependency and contract changes, packaging and supported
platforms, required release checks, artefact verification, rollback or forward
recovery, documentation and post-release acceptance. Identify responsible
packets, prerequisites and observable acceptance criteria for applicable items.

For this repository, inspect the current release configuration and
[release workflow](.github/workflows/release.yml), rather than assuming a
merge into `next` publishes a release. Bind verification to the actual release
candidate, version/tag and artefacts. Never promote an unverified candidate
or change release policy merely to clear an implementation gate.

### Enforcement boundary

Apply this protocol through existing governance until automated enforcement is
implemented and qualified. Deterministic checks should resolve target freshness,
validate evidence bindings, detect missing prerequisites and reject stale or
mismatched transition evidence. A System One decision hook may classify only
cases covered by explicit verified rules; ambiguous semantic impact requires
independent reasoning and review. Never let a hook waive an existing gate.

Enforcement changes require positive and negative tests for a moving target,
missing or stale evidence, non-material and material drift, concurrent updates,
unrelated eligible work continuing, and incomplete release evidence. Run the
same applicable checks locally and in CI. A policy document is not proof of
installed enforcement or a substitute for the current Dropbox admission policy.

## Delivery and evidence

Read [CI and merge admission](rules/CI_QUALITY.md) before changing CI.
Every mandatory Build prerequisite must succeed. Separate exact-head review,
observed agent identity and merge authorisation remain required.

Fetch actual `origin/next`; work in a registered feature worktree inside the
verified bare root. Do not commit directly to `main`/`next` or transplant prototype
runtime. An unmerged prerequisite needs an explicit scoped disposition in Dropbox.
Keep PRs small and run the actual commands/hooks; never bypass protection.
Read [the PR review budget](rules/PR_SCOPE.md): one outcome, with the changed-line
limit set by `FLOWSTATE_PR_MAX_LINES` in the protected target branch's `.env.ci`
(currently 600), and at most 10 paths including tests/docs. Estimate before delegation; checkpoint
and split before exceeding it. Refactoring is a separate prerequisite, not padding.

```sh
make check
make check-size-live        # live working-tree budget check after each save, before ai-commit
make check-pr-size BASE_SHA=<actual-pr-base-sha>
make check-code-quality BASE_SHA=<actual-pr-base-sha>
make check-documentation BASE_SHA=<actual-pr-base-sha>
make install-hooks          # once per clone: repo-managed pre-commit/pre-push hooks
make check-admission-commits BASE_SHA=<actual-pr-base-sha>
make check-admission PR=<number> BASE_SHA=<actual-pr-base-sha> # after review and CI
make session-start          # requires installed/configured admission runner; fails on startup errors
```

Read [admission preflight setup](rules/CI_QUALITY.md#local-admission-preflight).
Before every push, validate actual parent-bound metadata and manifests, not only
the admission unit tests. After a rebase/amend, refresh affected external records
and obtain exact-head re-review. Before merge, attach the real `Agent-Review:`
reference to the PR description and run the live read-only preflight. A local
pass never substitutes for the trusted `flowstate/merge-admission` status.
Install the [packaged admission runner](rules/STATUS_ISSUER.md#packaged-development-runner)
before development. Never replace a missing publisher with a green source-test status.

Keep packets, outputs, qualifications, AI attribution and review receipts only in
Dropbox Metadata. Never guess a model/provider/harness or synthesize human review.
Commit only the neutral Project-Metadata reference to a real external record.
Native Git identity and GitHub checks/reviews remain native facts.

Re-query exact-candidate checks and material review findings before any separately
authorized merge. This contract grants no merge authority. After actual exact-head
approval, clean only registered, safely pushed, clean worktrees; preserve hidden,
ignored, untracked, unpushed work and unmerged branches. Installed harness identity,
leases, budgets and live compliance are not proved by these offline source gates.
