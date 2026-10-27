# FlowState — greenfield agent instructions

## Source of truth
The active execution pack is in Dropbox at `/Development Notes/FlowState-Rewrite-2026-10-19/`. Read `00-START-GREENFIELD.md`, `BUILD-SCOPE.md`, `DELIVERY-AND-CI.md` and the assigned task packet. Archived plans and prototype instructions are historical evidence, not execution authority. Retrieve only the context necessary for the assigned task; report inaccessible sources rather than improvising.

## Branch and PR rules
- `main` is release-only. Never edit it or branch implementation work from it.
- `next` is protected integration. Never implement directly on it.
- Fetch/verify the actual `origin/next` SHA and create a new isolated feature branch/worktree **from origin/next**. Every implementation PR targets `next`.
- Before merge, require the actual required CI checks, an independent review of the candidate SHA, and resolution of material findings. Never bypass protection with admin privileges.
- When waiting on CI or review, checkpoint PR number, candidate SHA and remaining work; release execution resources. Upon resumption re-query GitHub for checks, approval and unresolved threads, including findings in COMMENTED reviews. Changed candidates require reassessment.

## Scope and engineering
Build a composable, provider-neutral Go runtime with the separate Vue frontend (`flowstate-web`). External definitions configure agents, swarms, tools, evaluation and gates. The old Bubble Tea/TUI architecture does not constrain the rewrite and prototype code must not be transplanted. Prioritise the smallest dogfoodable new runtime slice, not broad repairs of the old harness.

Define observable behaviour first, produce an appropriate failing unit/contract/integration test, then implement and verify green. **BDD is not synonymous with Godog or Cucumber.** Avoid unnecessary feature suites, inference and browser tests. Deterministic CI checks mechanics; independent review considers requirements, missed behaviour, architecture and test sufficiency against the actual change.

## Attribution and review provenance
- Record the actual agent role, harness, provider, and **observed serving model** per authoring session. Distinguish observed from configured/requested identities; when the serving model cannot be verified, state that explicitly rather than copying stale routing labels.
- Use truthful `AI-Generated-By` metadata, with task/run/session identifiers and candidate SHA retained in evidence. Git author identity does not establish human authorship or review.
- Do not add a human `Reviewed-By` or `Co-authored-by` unless the contribution really occurred. Capture separate reviewer identity, reviewed SHA, verdict and findings.
- Prefer a reusable session-attestation/commit-metadata adapter; trailer-presence checks alone do not verify true attribution.

## Reusable workflow MVP
The SDLC swarm is a configurable example, not hardcoded orchestration. The generic execution/evidence/evaluation/gate/outcome flow must support an additional minimal non-SDLC workflow unchanged. The first dogfood milestone exercises delegation, tools, attributable events, bounded recovery, real CI and independent review. Model-assisted evaluators remain replaceable and cannot substitute for deterministic facts or configured merge policy.

GitHub is authoritative for commits, PRs, CI and reviews; Dropbox `PROGRESS.md` is the readable progress record, not a second scheduler. Never invent verification or publication receipts.
