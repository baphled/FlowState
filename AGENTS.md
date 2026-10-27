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
- Follow the current Dropbox packet scope, allowed paths and cumulative budget.
- Use focused Go unit/headless integration tests, failing regression assertions
  for bugs, and real available commands. Cucumber, Godog and Gherkin are forbidden. Preserve Go package
  boundaries: cmd → internal/app → domain → ports; no concrete adapters in domain.
- Run `make check` and hooks; never skip hooks. The former attribution helper
  is paused until its external-record replacement is available.
- `make check-project-metadata` checks indexed paths/content. CI also checks new
  commit references/trailers. These are offline structural guards, not proof that
  a Dropbox record, serving model or independent review is authentic.
- Re-query actual GitHub CI/reviews for the exact candidate before merge. Resolve
  material findings including COMMENTED reviews. Record evidence in Dropbox.

Go files use snake_case names, lowercase packages and standard testing. Keep
imports grouped standard/internal. No inline/function-body comments or TODO/FIXME
markers (E2E tests may use inline comments). Product scope and done criteria live
in the current Dropbox contracts, not this bootstrap file.

Use standard Go `testing` for behavioural unit/integration/headless CLI coverage.
Do not add .feature files, Godog/Cucumber dependencies or step-definition suites.
Handoffs must not request Cucumber. Test-first behaviour uses Go assertions.
