# Merge admission source boundary

This change installs **source-only** validation, not a privileged workflow,
independent reviewer attestation, branch protection, or merge admission.

- `admission_metadata` validates records, parent SHA and source readset.
- `admission_review` checks claims; real attestation remains separate.
- `admission_candidate` binds PR and GitHub blobs to the source manifest.
- `admission_io` owns GitHub/Dropbox requests and orchestration. Do not use it
  as a trusted issuer until independently reviewed and qualified.

Run the source tests:

```bash
python3 -B -m unittest discover -s scripts/tests -p 'test_admission_*.py' -v
```
