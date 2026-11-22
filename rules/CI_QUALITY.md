# CI and merge-admission contract

CI status is evidence for the **exact candidate commit**, not an M1/T019
qualification, agent identity, independent review or permission to merge.
Current owner and agent policy lives in Dropbox `/FlowState/Dev`; attempt,
reviewer and acceptance records live in `/FlowState/Metadata`.

## Protected next branch

- Start only from current `origin/next` on a scoped feature worktree; no
  direct writes to `next` or `main`. Target `next` in every product PR.
- Enforce the changed-line limit from `FLOWSTATE_PR_MAX_LINES` in the protected
  target branch's `.env.ci` (currently 600), plus the 10-path and 128 KiB review
  budget including documentation, tests and generated files. No inline waivers.
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

## Contract checkers come from the base

A pull request must not be able to weaken the checks that judge it. The contract
checkers therefore run the code and configuration of the **base commit** against
the pull request's committed tree. `scripts/base_checkers.py` materialises these
exact paths from `BASE_SHA` into a private temporary directory and runs the
checker there, with the repository's git database so it still reads the candidate
`HEAD`:

- `go.mod`, `go.sum` (the module the Go checkers are built in);
- `scripts/check_code_contract.py`, `scripts/check_test_contract.py`,
  `scripts/check_python_contract.py`;
- their imports `scripts/contract_sources.py`, `scripts/python_contract_ast.py`,
  `scripts/test_layout.py`;
- everything under `scripts/code-contract/` and `scripts/test-contract/`;
- `.golangci-contract.yml`, exported with `base_checkers.py config` and passed to
  the pinned linter by `--config` (CI step "Fetch the protected-base contract
  linter configuration"; `make check-code-quality` and `make preflight`).

`make check-code-contract`, `check-test-contract`, `check-python-contract`, the
pre-push hook and `make preflight` use it. It needs only git and the base commit
already fetched; no network or secret. Any failure to produce the base copy (a
base that is not a 40-character ancestor SHA, a missing required path, a
non-regular file) exits 2 and runs nothing: there is no fallback to the checkout.
A base that predates a checker cannot be bootstrapped by this gate; that needs an
explicit maintainer decision recorded outside the pull request.

Intended semantics: a pull request that legitimately changes a checker is judged
by the **old** checker, and the new one applies from the next pull request on.
The reviewer of such a change reads the diff of the checker itself, because the
old checker cannot vouch for it. Offline, `make preflight BASE_SHA=<base>` works
from a fetched `origin/next`. `--worktree` previews of the test checker are not
evidence and are not wrapped.

What this protects against, and what it does not. It defends against a pull
request **changing a checker** to pass its own violation. It does **not** defend
against a pull request changing its own CI wiring: the wrapper, the Makefile
(including `-include .env.ci`), the hook and the workflow files come from the
pull request (a `pull_request` run uses the merge ref's workflow files), so a
pull request can edit them to skip the wrapper, or write `$GITHUB_ENV` or
`$GITHUB_PATH` before it runs. Other checks also still run from the tree:
`scripts/pr_size.py` (the hook and `make check-pr-size`; in CI the `scope` job reads it from the base, but
copies it from the tree when the base lacks it, the same bootstrap gap as below),
`scripts/security_gate.py`, `scripts/check_docs.mjs` and
`scripts/docs_contract.mjs` (`make check-documentation`),
`scripts/check_commit_subject.py`, the `gofmt` and `make check` steps, `npm ci`
lifecycle scripts, and the unit tests inside the `make check-*` targets. The
tamper-resistant gate is the external `flowstate/merge-admission` status, whose
code runs outside the pull request, together with the exact-head independent
review; changes to any file named in this paragraph need that review as closely
as a checker change. For a pull request whose base is a feature branch rather
than `next` or `main` (the `pull_request` base list in `ci-next.yml` includes
such branches), "the base" is that writable branch and the trust is whatever it
holds. The older "protected-base Python source contract" step in `ci-next.yml`
is a separate mechanism that still copies the checker from the tree when the
base lacks it, the opposite of the fail-closed rule here; it is to be folded
into the wrapper. Moving the remaining checks to the base needs a required
workflow or a `pull_request_target` job that checks out the base, a repository
settings decision. Two more limits: `ci.yml` (pull requests to `main`) has no Python-contract step, only
`ci-next.yml` does; and `base.sha` in a pull request event can be older than the merge ref's first parent when the
base branch has moved, so the checkers are then the older base's. An inline `//nolint` in the pull request's Go
source still applies, because only the linter configuration comes from the base.

## Local CI preflight

`make preflight BASE_SHA=<actual-next-base>` runs most of the blocking CI jobs
on the committed `HEAD`: the build, `go vet` for Windows and macOS, the pinned
`golangci-lint` (the full configuration, which includes formatting, and the contract one),
the gosec regression gate, the documentation contract and the Go tests with
the race detector. It refuses a working tree with uncommitted changes, because
CI sees only what is committed. Everything runs on the Go toolchain named in
`go.mod`; the linter and gosec are installed on first use into `bin/tools` at
the versions CI pins. A newer system Go otherwise leaves the pinned linter
reporting only typecheck errors.

The pre-push hook runs it, so a push of the checked-out branch that would fail
one of those checks fails before it reaches CI. A pushed tip that is not the
checked-out `HEAD` is not covered: the hook warns and leaves it to CI. The hook
therefore needs Go, a C compiler for the race detector, Python and, when
`node_modules` or the pinned tools are absent, Node and network access.

It does not replace CI. Not covered locally: the randomised Ginkgo run and the
end-to-end suite of `make check`, `govulncheck`, and the test jobs on macOS
and Windows.

## Local admission preflight

From the checkout root, install `scripts/requirements-admission.txt` in your
Python environment and run `make install-hooks` once per clone. The pre-push hook
fetches `origin/next` and admission-checks every pushed `refs/heads/*` tip, including non-HEAD
branches. A tip pushed by raw refspec (`HEAD~1:refs/heads/x`, `<sha>:refs/heads/x`) has a
local name outside `refs/heads/`, so it is not admission-checked, but the hook warns for it, as
for every pushed non-HEAD tip other than deletions and tags.
It validates every new commit's external metadata against that commit's immediate
parent and complete blob manifest using the same validators as live admission.
Missing records, credentials and mismatched parents fail the push.

For live commit evidence reads, supply `DROPBOX_READ_TOKEN` through your secret
environment. Alternatively set `FLOWSTATE_ADMISSION_RECORDS` to an external JSON
file mapping full `dropbox:/FlowState/Metadata/attempts/.../record.json` references
to records just fetched/read back from Dropbox. Keep this file outside checkout.
A cache is structural feedback, not authenticated or fresh admission evidence.

```sh
make check-admission-commits BASE_SHA=<actual-next-base>
make check-admission PR=<number> BASE_SHA=<actual-next-base>
```

The second command requires live `GITHUB_TOKEN`, `DROPBOX_READ_TOKEN`, and the
independently configured `FLOWSTATE_REVIEW_PUBLIC_KEY` and
`FLOWSTATE_EXECUTION_PUBLIC_KEY` (distinct Ed25519 public keys, lowercase hex).
Unset `FLOWSTATE_ADMISSION_RECORDS`: the ready phase rejects cached evidence.
After CI and separate review, it checks the PR's actual head/base, all commit
records, the `Agent-Review:` reference in its description, signed review/execution
records, expiry, evidence drift and exact-head Build success. Rebase/amend requires
new parent bindings and affected exact-head re-review; never edit old receipts
to claim they reviewed a new candidate.

These commands perform no status writes and need no App signing credentials.
They do not check publisher configuration or grant merge authority. A trusted
admission runner must still execute live admission and publish
`flowstate/merge-admission` on that exact head. An absent status means publication
has not completed; green admission unit tests or Build cannot supply it.
