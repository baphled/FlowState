# Trusted merge-admission gate

**Source:** [Issue #225](https://github.com/baphled/FlowState/issues/225). This
verifier supplements, but does not replace, PR-head `Build`, genuine reviewer
identity, Dropbox immutable attempt receipts, or M1 qualification.

## Install without bypassing `next`

1. Deliver the script, offline tests, workflow and this document in a small
   reviewed PR to `next`, using a genuine Dropbox-backed commit attempt record.
   Do not invent a Project-Metadata reference to satisfy the syntax guard.
2. Promote the reviewed source from `next` to default branch `main` using the
   ordinary protected-branch release path. Since December 2025, GitHub
   `pull_request_target` uses workflows from **default `main`** even for PRs
   targeting `next`. Do not assume a `next`-only workflow is active.
3. Configure `FLOWSTATE_DROPBOX_METADATA_READ_TOKEN` as a GitHub Actions secret,
   scoped to read only `/FlowState/Metadata/attempts`. No token or PR HEAD code
   may be exposed to an untrusted `pull_request` job. Rotate and revoke it via
   the GitHub/Dropbox provider UI.
4. In GitHub branch protection for `next`, require BOTH the existing **Build**
   check and the SHA-bound **flowstate/merge-admission** commit status. Set the
   expected source App and require independent pull-request approval with stale
   reviews dismissed. Disable auto-merge until the check actually runs and can
   demonstrate a failing negative-control PR; review rules cannot be installed
   through the current limited GitHub connector.
5. Independently verify after deployment: a PR missing its Dropbox record
   stays blocked; reviewer quota comments cannot approve; a new head invalidates
   old approval; a genuine independent exact-head approval and all valid records
   yield a successful status. Confirm missing/removed secret fails closed.

## Operational semantics

The trusted workflow never checks out candidate code; it reads current PR,
reviews and commits from GitHub, and downloads only canonical `.json` references
under the Dropbox attempt prefix. It posts the `flowstate/merge-admission` status
on the exact head SHA, not the default branch or merge ref. The full Build gate
remains separately required, because review and metadata approval cannot prove
unit tests or CI. This is a **pre-merge source admission** check, not a proof of
immutable Dropbox retention, authenticated agent sessions or runtime acceptance.

Deploying a new workflow to `main` alone is not enforcement. Enforce both status
and review in branch protection, test wrong-head and missing-record cases, and
check real GitHub rule settings. Existing merged #223/#224 retain unresolved
historical evidence debts; do not rewrite their shared commits to hide them.
