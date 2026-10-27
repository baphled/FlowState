# FlowState repository rules

## Dropbox checkpoint — mandatory before work and resumption

Read the actual current Dropbox files, using available Dropbox tools:

1. `/FlowState/Dev/00-AGENT-READ-FIRST.md`
2. `/FlowState/Dev/AGENTS.md`
3. `/FlowState/Dev/governance/OWNER-DECISIONS-AND-OPEN-QUESTIONS.md`
4. `/FlowState/Metadata/ACTIVE.json`, then only the assigned packet and referenced
   attribution/qualification/receipt evidence under `/FlowState/Metadata/attempts/`.

Verify paths, revisions, hashes and candidate/prerequisite evidence. Read only the
relevant current M1 contracts from `/FlowState/Dev`. Old repository plans, tasks,
handoffs and copied pack files are not execution authority. If Dropbox cannot be
read or evidence conflicts, stop the affected work with a checkpoint. Do not
substitute memory or a stale checkout. Pass verified source text and hashes to
children; do not assume they inherit these instructions.

## Project metadata — repository storage is forbidden

Store project packets, progress, AI attribution, qualifications, acceptance/review
receipts and captured evidence in Dropbox `/FlowState/Metadata`, never in this
checkout, commits or PR descriptions. Keep one immutable attempt directory.
Only a neutral `Project-Metadata: dropbox:/FlowState/Metadata/attempts/<attempt>/<record>.json`
reference belongs in a commit. Read that record in Dropbox before relying on it.

Never guess a provider/model/harness or copy a requested model into observed
identity. Record observable facts and their evidence source in Dropbox; otherwise
use `unverified`. Git user.name, environment overrides and a login are not evidence
of model identity, authorship or review. Do not add AI-Generated-By, Reviewed-By or
Co-authored-by trailers. Native Git identity, GitHub CI and actual GitHub reviews
remain native facts; do not synthesize human approval or rewrite shared history.

Reusable source code, qualification tests, schema definitions and explicitly
synthetic test fixtures belong in Git. Actual run reports, logs, databases and
qualification/receipt JSON do not. Store temporary capture files outside the
checkout and current Dev pack; upload and read back before accepting evidence.
Application runtime data also lives outside the code checkout; this policy does
not require the application to use Dropbox as its event database.

## Delivery

- Fetch actual origin/next; create an isolated feature worktree from that SHA.
  Never work in the bare repo, commit to main/next, branch from main or transplant
  prototype runtime. Open one bounded PR to next.
- Follow the assigned current Dropbox packet's scope, allowed paths and cumulative
  budget. Read the current Dropbox role manifest and AGENT-WORK-POLICY.md. Each
  agent has one active item; seniors focus on complex work and delegate bounded
  parts to mid/junior engineers. The coordinator arranges separate review.
- Use focused Go unit/headless integration tests, failing regression assertions
  for bugs, and real available commands. Cucumber, Godog and Gherkin are forbidden. Preserve Go package
  boundaries: cmd → internal/app → domain → ports; no concrete adapters in domain.
- Run `make check` and hooks. Never use --no-verify. Commit using
  `PROJECT_METADATA_FILE=/outside/checkout/record.json make ai-commit FILE=/outside/checkout/message.txt`.
  This external file must be a read-back copy of the current Dropbox record.
- `make check-project-metadata` checks indexed paths/content. CI also checks new
  commit references/trailers. These are offline structural guards, not proof that
  a Dropbox record, serving model or independent review is authentic.
- Re-query actual GitHub CI/reviews for the exact candidate before merge. Resolve
  material findings including COMMENTED reviews. Record evidence in Dropbox.

Go files use snake_case names, lowercase packages and standard testing. Keep
imports grouped standard/internal. No inline/function-body comments or TODO/FIXME
markers (E2E tests may use inline comments). Product scope and done criteria live
in the current Dropbox contracts, not this bootstrap file.

A delegator's claimed model/harness is not the worker's observed identity.
Handoffs must not prefill AI_AGENT/AI_MODEL or command a child to claim a particular
serving identity. The commit helper ignores such overrides and adds only the
external record reference. `coordination_store` and chat summaries are transient
messages, not durable project evidence; publish/read back the actual metadata in
Dropbox and pass its reference in coordination messages.

Worktrees must be immediate directories inside the actual registered bare
repository (normally `~/Projects/FlowState.git`), resolved from Git's common dir;
never create a separate `~/flowstate-work` clone or a sibling outside that root.
Validate that it is bare, fetch/record actual origin/next and create a feature
worktree inside that root. Read the actual repository helper, not a remembered
command. Do not substitute an unmerged prerequisite branch without a newly
accepted scoped dependency disposition.

PR approval triggers a cleanup checkpoint: re-query actual GitHub approval for
the exact current head, publish/read back an approval record in Dropbox Metadata,
then remove the clean, registered, safely pushed feature worktree. Preserve dirty,
untracked, ignored and unpushed work. Keep an unmerged local branch; delete it
only after verified merge and ancestry, without force. Preserve remote PR/commit
history. Cleanup is not an excuse to bypass review or merge protection.

Use standard Go `testing` for behavioural unit/integration/headless CLI coverage.
Do not add .feature files, Godog/Cucumber dependencies or step-definition suites.
Handoffs must not request Cucumber. Test-first behaviour uses Go assertions.

Validate a proposed allocation with `python3 -B scripts/agent_work.py --roles
/outside/checkout/current-agent-roles.json --plan /outside/checkout/read-back-plan.json`.
Both inputs come from current Dropbox records, not guessed role/model names.
The helper checks declared constraints only. The new harness must bind actual
actors, enforce atomic leases and shared budgets, and verify real separate review
sessions; current runtime installation/enforcement is not claimed.
