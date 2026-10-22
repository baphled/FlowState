# Legacy defect lessons for greenfield acceptance

Evidence review: 22 October 2026. This is a **historical-source risk review**, not confirmation that faults persist on the current branch, and not a duplicate of the independent October bug investigation. Use it to tighten acceptance of relevant new components; keep optional reporting formats unmandated.

## Sources and concrete lessons

1. **Code-Reviewer gate/manifest contract drift** — Vault: `Bug Fixes/Code-Reviewer Not Found After Recent Changes (May 2026).md` records the May investigation at `feature/vue-ui-rebase` SHA `066234cd`: reviewer was actually found and ran, but `dev-swarm.yml` expected `code-review-verdict-v1` while the manifest emitted `bug-findings-v1` (missing `verdict`). **Acceptance:** validate effective manifest output schema, gate schema, key routing, and consumer jointly before dispatch; distinguish execution/selection failure from post-member validation failure. A matching JSON shape alone does not establish independent acceptance. Align with existing review-envelope work.

2. **Filesystem guard bypasses** — Vault: `1. Projects/FlowState/Documentation/Bug Hunt — agent-platform (August 2026).md`, branch `agent-platform`, historical HEAD `b7dffd0a`: empty-path writes and precommit pollution fail-open were reported fixed, but self-reported write omissions and symlink path escapes remained listed HIGH. **Acceptance:** enforce authorised effects at the real filesystem/tool boundary, canonicalise paths including symlinks before policy checks, reject missing/malformed evidence fail-closed, and verify side effects rather than trusting model reports. Verify against candidate SHA.

3. **Vue UI parity and state isolation** — Vault: `Documentation/Reviews/Vue Web Frontend Master Bug Report (May 2026).md`, May 20–21 review on `feature/vue-ui-rebase`: 97 unique historical findings, including stale-session mutations after navigation, race conditions during agent/session switching, unsafe Markdown rendering, missing FE CI, accessibility holes and misleading attempted tool calls. **Acceptance:** separate Vue repo CI builds/lints/tests; verify SSE reconnect/error and cancellation paths; bind asynchronous updates to the correct session/turn and generation; distinguish attempted/blocked/executed tool states; test keyboard/ARIA and Markdown sanitisation. Do not import old Vue architecture wholesale.

4. **Swarm recovery versus optimistic completion** — Vault: `Plans/Swarm Resumability (May 2026).md`, `status: draft`, `shipped_commits: []`: documents lost in-flight state on cancellation, provider failure, process restart and gate failures in the historical prototype. **Acceptance:** recover authorised child attempts, durable receipts, checkpoints, and exact review/CI gates across these stop modes using shared execution path. The historical proposed JSONL details are *not* approved greenfield implementation requirements.

5. **CI churn and checks that don't exercise real targets** — GitHub release commit history in `baphled/FlowState` (March 2026) records repeated golangci-lint version/install reversals. October PR #23 review separately identified a Godog command without an executable suite. **Acceptance:** deterministic CI commands that actually run against existing suites and current tree; no missing executable, skipped test or stale check masquerades as successful qualification.

6. **Autoresearch** — Vault: `Plans/Autoresearch Live Driver Integration (April 2026).md` explicitly relies on Git as a candidate ratchet. **Disposition:** defer Git-dependent autoresearch from initial dogfood; retain bounded cheap-model generate→evaluate→iterate as optional later research behind general execution/evaluation interfaces. No mandatory Git dependency in generic workflow engine.

## Product triage

Retain **behavioural needs**, not historical packages. Categorise each legacy feature **retain / redesign / defer / retire** only with evidence, user value, and architecture cost. Defects alone are not evidence a useful capability should disappear. The current independent October bug investigation may supersede historical findings: reconcile by exact paths/SHAs and avoid repeating the same audit.

Do not make the review report itself, Dropbox/Obsidian storage, vector retrieval, or the eventual decision-layer implementation mandatory runtime dependencies. Feed verified findings into the existing small task acceptance criteria and ADR reconciliation before implementing the relevant slice.
