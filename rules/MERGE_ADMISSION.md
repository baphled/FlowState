# Merge admission source boundary

This change installs **source-only** validation, not a privileged workflow,
independent reviewer attestation, branch protection, or merge admission.

- `admission_metadata` validates records, parent SHA and source readset.
- `admission_review` checks claims; real attestation remains separate.
- `admission_candidate` binds PR and GitHub blobs to the source manifest.
- Candidate commits must form a linear chain from the exact current base SHA.
  Admission snapshots the head, base ref/SHA, PR number, author, open/draft state
  and full description. A relevant change before the final decision fails.
- `admission_io` owns GitHub/Dropbox requests and orchestration. Do not use it
  as a trusted issuer until independently reviewed and qualified.
- Every consumed Dropbox record is read again before success. Missing or changed
  records fail the decision, including review records. This detects validation
  races; it does not make a successful status fresh forever. A production merge
  authority must validate and retain the exact evidence at the actual merge
  boundary and prevent another actor from reusing a stale status. Until that
  authority and branch restrictions are qualified, this remains source-only.

Run the source tests:

```bash
python3 -B -m unittest discover -s scripts/tests -p 'test_admission_*.py' -v
```
