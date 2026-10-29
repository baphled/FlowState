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

## Delivery and evidence

Fetch actual `origin/next`; work in a registered feature worktree inside the
verified bare root. Do not commit directly to `main`/`next` or transplant prototype
runtime. An unmerged prerequisite needs an explicit scoped disposition in Dropbox.
Keep PRs small and run the actual commands/hooks; never bypass protection.

```sh
make check
make check-code-quality BASE_SHA=<actual-pr-base-sha>
make check-documentation BASE_SHA=<actual-pr-base-sha>
```

Keep packets, outputs, qualifications, AI attribution and review receipts only in
Dropbox Metadata. Never guess a model/provider/harness or synthesize human review.
Commit only the neutral Project-Metadata reference to a real external record.
Native Git identity and GitHub checks/reviews remain native facts.

Re-query exact-candidate checks and material review findings before any separately
authorized merge. This contract grants no merge authority. After actual exact-head
approval, clean only registered, safely pushed, clean worktrees; preserve hidden,
ignored, untracked, unpushed work and unmerged branches. Installed harness identity,
leases, budgets and live compliance are not proved by these offline source gates.
