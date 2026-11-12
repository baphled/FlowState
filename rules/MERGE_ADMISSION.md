# Merge admission source boundary

These source modules validate candidate and signed review evidence. They do not
install a privileged workflow, trusted signing services, branch protection or an
exclusive merge authority.

- `admission_metadata` validates records, parent SHA and source readset.
- `admission_review` checks claims; real attestation remains separate.
- `admission_candidate` binds PR and GitHub blobs to the source manifest and
  verifies required CI checks: every configured check must report conclusion
  `success` on the exact candidate head SHA before admission can publish
  success. Skipped, failed, incomplete or absent checks fail the decision, as
  do checks bound to a different head. `required_checks` is trusted
  caller-supplied configuration (branch-protection contexts such as
  `Lint & Format`, `Test (ubuntu-latest)`, `Build`); it is never read from
  PR-controlled data. Legacy commit statuses are not consulted: only the
  check-runs API carries the per-run `head_sha` binding this gate needs.
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
execution_key, clock, required_checks)`, where `github` is the read-only repository
client, `publisher` is an isolated trusted issuer, `clock` returns UTC Unix seconds
and `required_checks` names the CI checks that must have succeeded on the exact
candidate head. Missing `required_checks` configuration fails closed.
Missing keys, missing execution evidence, expired review, a non-green required
check or failed publication prevents success. Review expiry is checked again
after the final state recheck.

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

## Squash merge trailer policy

The Project Metadata Guard requires EXACTLY ONE `Project-Metadata` trailer on
the `next` tip. Squash-merging a multi-commit PR concatenates every branch
commit body into the squash message, producing N trailers and failing push CI
(incident: run 43116444053, patched tip 4bf4ddf).

Repo-level defaults (`squash_merge_commit_title=PR_TITLE`,
`squash_merge_commit_message=BLANK`) make GitHub produce an empty squash body
by default, which eliminates concatenation for web and API merges. The guard
then fails the tip for the missing trailer, so every squash merge must supply
exactly one trailer explicitly:

```bash
gh pr merge --squash \
  --subject '<type>: <summary>' \
  --body 'Project-Metadata: dropbox:/FlowState/Metadata/attempts/<attempt>/<record>.json'
```

`gh pr merge` without `--subject`/`--body` uses the repo defaults; explicit
flags override them. Never squash with `--body` containing multiple commit
bodies or multiple trailers. The guard is the enforcement point: a bad tip
fails push CI red.
