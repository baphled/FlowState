# Review handoff and correction loop

`GitHub` is the discussion surface; Dropbox `/FlowState/Metadata/attempts/<attempt>/`
is the immutable evidence surface. An agent starting or resuming a PR must read
both, compare the exact candidate SHA, and stop when either cannot be read.

## Reviewer

1. Review the full exact candidate, its required checks, architecture and tests.
2. Publish actionable GitHub comments with stable IDs (`F<PR>-<N>`), locations,
   expected correction and regression-test oracles. When GitHub disallows an
   author requesting changes on their own PR, publish a marked blocking
   Conversation comment; it is **not** a formal GitHub approval.
3. Upload a review ledger to Dropbox Metadata and **read it back**. Every finding
   records its state and GitHub URL; never put run-specific evidence into Git.
4. A new head invalidates the previous accepted verdict. Carry unresolved IDs
   forward into a fresh review ledger and inspect the new diff and full candidate.

## Implementer

1. Obtain a trusted local read-back copy of the current Dropbox review ledger.
2. Run `scripts/review_handoff.py` with `GITHUB_TOKEN`, repository, PR, exact head,
   and `--metadata <path>`. The tool retrieves GitHub PR Conversation comments,
   submitted reviews and inline review comments. It rejects stale ledgers,
   missing IDs, unstructured change requests and incomplete retrieval.
3. Fix outstanding findings, each with a deterministic regression test. Reply
   to the GitHub thread with the corrective commit, test and disposition; update
   immutable Dropbox evidence for the revised head.
4. Request re-review of the **entire exact candidate**. The script can report
   outstanding changes but **never** emits review PASS or authenticates agents.

## Ledger format

The coordinator materialises a Dropbox read-back copy as JSON:

```json
{
  "pr": 260,
  "head_sha": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "findings": [
    {
      "id": "F260-01",
      "status": "open",
      "description": "Lease fence must be checked before each tool invocation",
      "requested_change": "Recheck lease immediately before calling tool",
      "test_oracle": "Revoke lease during stream: no tool executes"
    }
  ]
}
```

Only the actual independently verified review and required exact-head gates can
qualify the PR. The handoff tool deliberately returns `review_accepted: false`.
A closed finding requires evidence, but evidence alone is not acceptance.
The current offline implementation is a read/validation tool, not a Dropbox
writer, GitHub comment publisher, durable lease or hosted coordinator.

## Pre-push

Run `make install-hooks` in each worktree. The tracked `.githooks/pre-push` hook
refreshes `origin/next`, rejects protected branches and non-descendant commits,
reads the trusted target `.env.ci` budget, then runs size and changed-source
contract checks before remote CI. This is not a substitute for required exact-head
checks or independent review, and no automated check may waive those gates.
