# Merge admission source boundary

These source modules validate candidate and signed review evidence. They do not
install a privileged workflow, trusted signing services, branch protection or an
exclusive merge authority.

- `admission_metadata` validates records, parent SHA and source readset.
- `admission_review` checks claims; real attestation remains separate.
- `admission_candidate` binds PR and GitHub blobs to the source manifest.
- Candidate commits must form a linear chain from the exact current base SHA.
  Admission snapshots the head, base ref/SHA, PR number, author, open/draft state
  and full description. A relevant change before the final decision fails.
- `admission_io.GitHub` exposes reads only, with no status publishing method.
  Admission requires an explicitly injected separate trusted status publisher.
- `admission_authentication` requires the signed review and an execution receipt
  signed by a separately pinned public authority. It uses the actual authenticated
  review API; unsigned claims cannot publish success. Both keys are public-only
  verifier inputs, configured outside candidate PR code.
- `admission_transport` rejects redirects, duplicate JSON object keys and
  non-finite JSON numbers before evidence validation.
- Every consumed Dropbox record is read again before success. Missing or changed
  records fail the decision, including review records. This detects validation
  races; it does not make a successful status fresh forever. A production merge
  authority must validate and retain the exact evidence at the actual merge
  boundary and prevent another actor from reusing a stale status. Until that
  authority and branch restrictions are qualified, this remains source-only.

## Trusted runner integration

The standalone `python3 -B -m scripts.admission_io` command intentionally fails.
No workflow or privileged default configuration is installed. A qualified external
runner must supply `execute(event, github, dropbox_token, publisher, review_key,
execution_key, clock)`, where `github` is the read-only repository client,
`publisher` is an isolated trusted issuer and `clock` returns UTC Unix seconds.
Missing keys, missing execution evidence, expired review or failed publication
prevents success. Review expiry is checked again after the final state recheck.

The execution authority must observe actual independent harness actor/session/run
identities, then sign `flowstate-review-execution-v1` with its own Ed25519 key.
Its envelope uses the canonical signature grammar in [review signatures](REVIEW_SIGNATURE.md).
The receipt binds the entire signed review's canonical payload digest and exact
reviewer/implementer observations. Dropbox supplies storage, not authority: a
document without the configured observer signature fails. Provisioning a signer
that merely repeats supplied identity strings would not qualify this boundary.

Only an externally qualified merge authority can supply durable one-use admission,
retain immutable evidence and exclusively control updates at the actual merge
boundary. This source API does not implement that service. A success status has
no expiry enforced by GitHub; rerunning validation cannot make it an atomic
transaction across GitHub and Dropbox. Keep the deployment issue open until real
signers, protected status origin, issuer credentials and merge exclusivity have
been installed and exercised. Candidate PR code must never receive issuer keys.

Run the source tests:

```bash
python3 -B -m unittest discover -s scripts/tests -p 'test_admission_*.py' -v
```

## Deployed enforcement

The trusted runner is deployed as a local service outside this checkout
(`~/Projects/flowstate-admission`). It imports the admission modules only from
a checkout pinned to the reviewed `origin/next` admission source SHA; a moved
`next` head fails the pin check and blocks admission until the owner
re-qualifies the pin after a reviewed source update.

Enforcement is branch protection: `next` must require
`flowstate/merge-admission` pinned to the dedicated issuer GitHub App (commit
statuses write plus metadata read only) alongside the existing required
checks. Only that App can satisfy the check: the issuer refuses success
unless the live protection pins it to the same App with administrator
enforcement, linear history and a strict Build requirement intact, and the
protection update is itself recorded as separate closeout evidence before the
first admission run.

The runner performs admission and merge in one process: it publishes
`pending`, revalidates the exact candidate evidence (signed review from a
separate reviewer session, separately signed execution receipt corroborated
against the local harness session store, candidate commit chain and blob
digests, final Dropbox recheck), waits for the other required checks, then
squash-merges. A success status is consumed at the end of the flight: after a
completed or failed merge attempt the runner publishes a terminal failure, and
a lease-aware watchdog revokes any lingering success on an unmerged open
candidate, so a stale success cannot authorise a later merge after evidence,
review or candidate state changed.

The two Ed25519 signing authorities (review attestor and execution observer)
live in the runner service, never in this repository. Before signing, the
execution authority verifies that the claimed reviewer and implementer
sessions exist in the local harness session store and that their transcripts
reference the exact candidate head SHA and PR number; supplied identity
strings alone are rejected. Merges into `next` are made only through this
runner; direct merges stay blocked by the same protection, which the runner
re-verifies live before any success publication.
