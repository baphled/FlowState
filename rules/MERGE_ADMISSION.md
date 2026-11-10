# Merge admission source boundary

This change installs **source-only** validation; it does not install a privileged workflow, attest an independent reviewer, configure branch protection or grant merge admission.

- `admission_metadata` validates external record shape, expected parent and source readset.
- `admission_review` checks reviewer-claim shape only; a trusted attestor is still required.
- `admission_candidate` binds PR and GitHub changed blobs to the external manifest.
- `admission_io` owns external GitHub/Dropbox operations and orchestration; do not run it as a trusted status issuer until a separate approved and qualified deployment.

Run: `python3 -B -m unittest discover -s scripts/tests -p 'test_admission_*.py' -v`.
