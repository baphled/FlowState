# Native verifier dispatch

The independent-review launcher is `scripts/reviewer_dispatch.py`. It launches
only the **native FlowState** session binary built at `build/flowstate`; it does
not discover another CLI, use OpenCode, or fall back to Copilot. The reviewer
receives an exact PR head/base, a public review packet and a fixed `verifier`
role instruction. The coordinator supplies the actual model and the implementer's
observed session ID. The verifier must use a distinct session.

This is a bounded **review execution adapter**, not an authenticated reviewer
identity provider. It reports a review attempt and never outputs a `PASS` verdict
for merge admission. Independent observed actors, signed review and execution
records, immutable Dropbox read-back and qualified `flowstate/merge-admission`
remain required; see [review handoff](REVIEW-HANDOFF.md) and
[merge admission](../../rules/MERGE_ADMISSION.md).

## Invocation

From the repository root, first build `build/flowstate`. Provide a pre-existing,
private (mode 0700), empty working directory **outside the checkout** and an
independently authored public review packet. Do not point it at the code repository
or Dropbox Dev. The native Z.ai provider must already be configured.

```bash
python3 -B -m scripts.reviewer_dispatch \
  --repo "$PWD" --workspace "$HOME/.local/state/flowstate/reviews/pr280-attempt1" \
  --packet /path/to/public-review-packet.md --pr 280 \
  --head <exact-40-character-head-sha> --base <exact-40-character-base-sha> \
  --implementer-session <observed-implementer-session> --model <configured-model>
```

The launcher creates a separate native session with the exact public task,
invokes one bounded run, and saves a private `verifier-dispatch.json` containing
candidate/session identifiers, an event-log digest and
`review_accepted: false`. It does **not** submit review approval. A missing binary,
unsupported/failed provider, invalid candidate, exhausted budget or self-review
fails without attempting any alternative executor.

## Regression checks

```bash
python3 -B -m unittest scripts.tests.test_reviewer_dispatch -v
python3 -B -m scripts.check_python_contract --base "$(git merge-base HEAD origin/next)"
```
