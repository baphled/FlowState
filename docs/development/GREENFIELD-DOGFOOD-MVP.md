# Greenfield dogfood MVP — delivery slice and coordinator contract

Status: proposed delivery amendment, 22 October 2026. The active authority and full requirements remain in Dropbox `/Development Notes/FlowState-Rewrite-2026-10-19/` (`00-START-GREENFIELD.md`, `BUILD-SCOPE.md`, `DELIVERY-AND-CI.md`, `PROGRESS.md`). This document is a scoped implementation acceptance guide, not an alternative scheduler or a replacement for archived P00–P19 requirements. Reconcile it into the active Dropbox pack through the designated documentation writer.

## Goal
Deliver an externally configured SDLC swarm using the fresh Go runtime that can implement a bounded FlowState change through a PR, real CI, independent review and evidence-backed gate. Agents/swarms, tools, reviewers, policies and evaluator implementations are configuration, **not special cases hardcoded into the engine**. Verify composition with one small non-SDLC workflow using the same runtime without source changes.

## Four incremental, independently reviewable milestones
1. **Agent execution:** payload → session/turn → provider → tools → ordered attributable events → terminal result/cancellation. Deterministic tests and bounded provider dogfood.
2. **Composable swarm:** load external definitions, delegate as a tool, communicate, preserve shared hierarchical budgets, record agent identity, recover/reassign a failed child without destroying the parent session, and resume from durable checkpoints.
3. **SDLC cycle:** test-first behavioural development (no mandatory Godog), small commits/PRs targeting `next`, actual CI results, fresh context-isolated review of the exact diff/SHA, and a policy gate over requirements, objective evidence and independent findings. Review comments in COMMENTED submissions can block.
4. **Dogfood and generality:** one real self-improvement PR, including the feedback/correction loop if needed; then a minimal second non-SDLC workflow configured on the same execution/gate machinery.

## Map to existing plan — avoid invented duplicate subsystems
- P03: shared versioned contracts for evidence, evaluation criteria, findings, outcome and provenance; record configured/requested vs observed model identity.
- P04–P07: lifecycle, events, persistence, provider/tool execution, permissions and resource budgets.
- P08: external agent and extension discovery, replaceable evaluation adapters, gates and hooks.
- P09: externally configured swarm graphs, isolated/ephemeral or retained worker context, supervisor-driven delegation, checkpoints, recovery and outcome gates.
- P10: minimally sufficient attributable API/event stream and CLI; full UI parity deferred.
- P11–P19: retain on the full roadmap, do not make completion of these plans a prerequisite for first dogfood.

## Coordinator execution contract
Given an authorised milestone and explicit overall budget, the coordinator must continue the eligible work **without repeatedly prompting the owner**. After each accepted task/merge, select the next eligible bounded packet and dispatch it, preserving task authority and branch discipline.

- PR opened: store PR number, base/head/merge candidate SHAs and current review/check evidence. Checkpoint and release idle model/worker resources when waiting. Resume through a trusted GitHub status/review re-query (bounded polling initially, event-driven later). Do not call waiting a completed task.
- CI failed, missing or unexpectedly skipped: diagnose the specific candidate/check, schedule a bounded correction, and revalidate its changed SHA. No `|| true`, admin merge bypass or fictitious green evidence.
- Reviewer feedback: inspect every review state, including COMMENTED, plus unresolved threads and actual content; classify material defects. Correct, address comments accurately, and request re-review of the new candidate. Independent review never equals human approval unless it really was human.
- Local child failure: checkpoint evidence, reassign or resume through an eligible agent within shared limits; do not terminate the whole milestone merely because one worker failed.
- A packet's two-attempt / 45-active-minute ceiling is **not** automatically the milestone/session ceiling. Persist the cumulative budgets and apply both limits without resetting them by spawning children, retrying, creating new commits or restarting.
- Stop only on: completed milestone with verified outcomes; exhausted **overall authorised** resources; genuine unrecoverable block; or an action requiring owner permission/decision. Surface an actionable checkpoint containing completed evidence, precise blocker, remaining scope and resumption instruction. Never claim unattended recovery is implemented until verified.

## Review and evaluation acceptance
Use objective checks for mechanically provable claims, and a fresh, context-limited reviewer for behavioural/architectural correctness and test sufficiency. Reconcile requirements, review findings and verifiable implementation evidence through a configurable gate. A model-assisted/JEV-like evaluator is optional behind a replaceable adapter: agreement between models does not override failed CI or missing evidence. An evaluator must distinguish finding confidence from objective proof.

## Attribution acceptance
Record agent role, harness, provider, actual observed model (or `unverified`), run/session ID, candidate SHA, reviewer identity/session/reviewed SHA and GitHub/CI receipts. Do not construct model attribution from stale config labels. Commit trailers and the GitHub identity alone cannot establish provenance. Build toward reusable session-linked attestation and validation tooling; never invent human `Reviewed-By` or `Co-authored-by`.

## Qualification tests
- A complete PR review/CI/correction/re-review/merge sequence is evidenced at the relevant SHAs, with no owner relaying each transition.
- An interrupted/coordinator-resumed task does not repeat a published commit, duplicate the worker, or spend a reset budget.
- A child failure is isolated; the supervisor can reassign it within bounds.
- A COMMENTED review with a material unresolved finding blocks acceptance even if all CI jobs pass.
- A configured non-SDLC workflow can use the same runtime, evidence and gate contracts.
- Every completed claim has an immutable or externally verifiable receipt; unavailable receipts remain explicitly pending.

Plan tasks should be reprioritised as **verified / required for first dogfood / deferred**, never marked done merely to bypass upstream dependencies. Implementation proceeds via fresh feature branches from verified `origin/next`, small PRs into `next`, separate independent review, and verified CI; `main` stays at release.
