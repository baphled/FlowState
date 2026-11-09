# Trusted merge-admission gate

**Source:** [Issue #225](https://github.com/baphled/FlowState/issues/225).
The trusted default-branch verifier checks GitHub blobs and Dropbox evidence.

## Install only after qualification

1. Deliver reviewed source to `next` with real Dropbox commit metadata.
2. Promote through the protected `next` to `main` release path. Privileged
   `pull_request_target` uses the default branch, not the PR's source.
3. Isolate the Dropbox read credential in an App Folder mirror or gateway.
   Full Dropbox OAuth does not restrict it to the Metadata directory.
4. For affected public repositories, allow and test `pull_request_target`
   before 2 December 2026; verify both positive and negative triggering.
5. Require trusted exact-head `flowstate/merge-admission` and `Build` statuses
   in `next` protection, strict base checks and no bypasses.
6. Set GitHub's required approving review count to zero. A separate agent
   review remains mandatory through the trusted status, not GitHub approvals.
7. Revalidate through trusted PR conversation comments or default-branch
   dispatch; exercise revoked credentials, wrong heads and negative-control PRs.

## Operational semantics

The workflow never checks out or executes candidate code with secrets. It
checks live GitHub PR and commit data, canonical Dropbox records and actual
committed file bytes, posting the verdict on the PR head rather than a merge
ref. Missing or invalid evidence must fail closed, including after success.
Deployment alone does not install branch protection or authenticate reviewers.

## Sole-maintainer agent-review contract

The PR description contains one `Agent-Review:` trailer referencing a JSON file
under `dropbox:/FlowState/Metadata/attempts/<attempt>/`. The external record
uses `flowstate-agent-review-v1` with matching `metadata_ref`, `pr_number`,
`head_sha`, and `verdict: PASS`. A reviewer with `role: verifier` records an
observed `run_id` and `identity_evidence`; the implementer supplies a distinct
observed run and evidence. Substantive `summary` and `findings` are required,
plus a passing `negative_oracle` with its own evidence. Missing, stale,
quota-only and same-run reviews fail closed without a human GitHub approver.
Dropbox content alone is not cryptographic agent identity proof: qualify the
trusted writer, attribution and credential boundaries before live enforcement.

## Attestation and status authority

The sole-maintainer workflow requires an independently controlled reviewer
attestor. Its record includes `attestation.issuer` set to
`flowstate-independent-review-attestor-v1` and `attestation.hmac_sha256`.
The HMAC authenticates canonical sorted JSON of all other record fields,
prefixed with the `flowstate-agent-review-v1` domain separator and NUL byte.
The key in `FLOWSTATE_AGENT_REVIEW_ATTESTATION_KEY` must be controlled by a
separate verifier service and hidden from implementation runs. A signature
authenticates its issuer, **not** the underlying agent run without attestor
controls and retained run/oracle receipts. Missing or forged signatures fail.

GitHub PR reads use the normal read-only `GITHUB_TOKEN`; only an isolated,
dedicated GitHub App status credential may populate
`FLOWSTATE_TRUSTED_STATUS_TOKEN`. The workflow has no default token write
permission; require and pin the App as the status source in branch rules.
Missing, identical or exposed credentials fail closed.

**Deployment blocker:** GitHub statuses persist after evidence revocation.
Do not enable merges from old commit statuses: require a trusted merge-time
revalidation gateway or enforced queue, immutable/retained evidence and an
after-success-without-event negative control. Source-read hashes/IDs also need
independent attestation or actual read-back before operational acceptance.
Neither this PR nor successful unit tests assert those controls are deployed.
