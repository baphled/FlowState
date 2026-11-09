# Trusted merge-admission gate

**Source:** [Issue #225](https://github.com/baphled/FlowState/issues/225). This
verifier supplements PR-head `Build`, independent review and external evidence;
it does not prove runtime qualification or enforce branch protection by itself.

## Install without bypassing `next`

1. Deliver this script, tests, workflow and guide via reviewed PR to `next`, with a
   genuine Dropbox record. Never fabricate a `Project-Metadata` reference.
2. Promote reviewed source from `next` to default `main` through the protected
   release path. `pull_request_target` uses default-branch workflow code, not
   the PR target branch; a `next`-only workflow does not enforce admission.
3. Configure `FLOWSTATE_DROPBOX_METADATA_READ_TOKEN` only after credential
   isolation review. Dropbox Full Dropbox OAuth cannot restrict a token to an
   arbitrary existing directory; use an isolated App Folder mirror or gateway.
4. For affected public repositories, explicitly allow `pull_request_target` in
   GitHub Actions policy before 2 December 2026; test its actual event delivery.
5. Require **Build** and the exact-SHA **flowstate/merge-admission** status in
   `next` protection, with trusted status App, independent approval and stale
   review dismissal. Disable auto-merge until negative controls fail as expected.
   Read back the real GitHub rules; this connector cannot administer them.
6. Revalidate reviews through a trusted PR conversation comment or default-branch
   `workflow_dispatch`. Do not run PR-supplied review-event workflow code.
7. Verify missing evidence, malformed records, wrong head, quota comments and
   credential failures all fail closed; only valid exact-head evidence and actual
   independent approval may pass. A revoked credential alone triggers no recheck.

## Operational semantics

The privileged verifier never checks out candidate code. It obtains live PR,
review and commit data from GitHub, reads canonical external JSON records and
checks actual committed blobs. Its status targets the PR head SHA, never the merge
ref or default branch. Required Build checks still prove code quality separately.

This is pre-merge source admission, not proof of immutable Dropbox retention,
authenticated agent sessions or runtime acceptance. Deploying to `main` alone
does not enforce it: verify branch protection and a failing negative-control PR.
Merged #223/#224 retain historical evidence debts; do not rewrite old commits.
