<!-- Reviewer guidance: read AGENTS.md (branch/worktree policy, ai-commit,
     BDD workflow) and rules/BDD_WORKFLOW.md before approving. Confirm the
     checklist is truthful rather than merely ticked. -->

## Summary

<!-- What changed and why. One or two sentences, then a short bullet list. -->

## Type of change

<!-- Tick one. -->

- [ ] `feat` — new behaviour
- [ ] `fix` — bug fix (failure coverage preserved/added)
- [ ] `refactor` — no behaviour change
- [ ] `test` — test-only (Ginkgo/Gomega)
- [ ] `docs` — documentation only
- [ ] `chore`/`ci`/`build` — tooling or pipeline
- [ ] Breaking change (`BREAKING CHANGE:` footer present)

## Checklist

- [ ] BDD scenario written first (Ginkgo v2/Gomega; one behavioural expectation per spec)
- [ ] `make test` / BDD suites pass locally
- [ ] `make check` is green
- [ ] Commits created with `make ai-commit` (AI-Generated-By / Reviewed-By trailers)
- [ ] godoc comments on exported identifiers; no comments inside function bodies (see rules/CODE_STYLE.md)
- [ ] No inline `//`-style review comments left in the diff
- [ ] Branch follows rules/branch-naming.md
- [ ] Project metadata/receipts recorded in Dropbox `/FlowState/Metadata`, not in this repository

## Notes for reviewers

<!-- Anything non-obvious: setup checks, failure cases exercised, seed for
     flaky-looking failures, impact on existing specs. -->
