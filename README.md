# FlowState

A general-purpose AI assistant. This repository contains the Go implementation,
reusable tests and build tooling. Current M1 scope, decisions and acceptance
criteria live in Dropbox `/FlowState/Dev`; run records live in
`/FlowState/Metadata`.

Before agent work, read [AGENTS.md](AGENTS.md) and the required current Dropbox
entry, rules, decisions and Metadata ACTIVE pointer. Do not implement from old
repository plans or a remembered prompt. Resolve the assigned packet and actual
qualification evidence. Documentation readiness is not a T019 or runtime pass.

## Development

Use an isolated feature worktree from freshly fetched origin/next and open a
bounded PR to next. Do not commit to main/next or copy the prototype runtime.
Use the Go version declared by go.mod and CI; Python 3.11+ is required by the policy helpers as they land.

```bash
make test
make check
make build
```

Use standard Go tests and headless integration checks. Cucumber, Godog and
Gherkin are forbidden. See [the testing workflow](rules/BDD_WORKFLOW.md).

Project metadata, AI attribution, qualification reports, receipts and real captured
outputs are forbidden in the checkout. Store and read them in Dropbox Metadata.
Only a neutral Dropbox record reference goes into a commit. Reusable qualification
code, source schemas and explicitly synthetic test fixtures remain in Git.
