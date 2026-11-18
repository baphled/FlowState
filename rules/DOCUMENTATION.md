# Documentation contract

Read current Dropbox `/FlowState/Dev/governance/CODE-AND-DOCUMENTATION-CONTRACT.md`
and agent rules before code or documentation work. Write for a contributor who
has not read the agent conversation. Keep one current explanation per subject.

## Source documentation

Every new or changed exported Go function, method, type, alias, variable and
constant needs a Go doc comment starting with its identifier. Explain its purpose
and relevant inputs/results, errors, ownership, concurrency and cancellation
contracts. Comments must describe guarantees and restrictions rather than repeat
the signature. Package comments explain the responsibility and dependency
boundary. The parser checks exported comments even in platform/build-tagged files;
`revive` checks API/package documentation in the active build.

Use comments for a public contract or non-obvious invariant, not a narration of
implementation statements. Keep functions small enough to explain through names.
Do not manufacture boilerplate merely to satisfy a linter.

## Maintained guides

A package/architecture guide explains purpose, ownership, public surface,
dependency direction, invariants/failure behaviour and the smallest useful local
verification command. Add examples when they clarify usage. Label runnable
commands with prerequisites and working directory; do not invent an installed
command, path, feature, model or validation result. Check examples against the
actual source before publication. Executed evidence stays in Dropbox Metadata.

Update affected documentation in the same bounded PR when behaviour, interfaces,
configuration, schemas, dependency ownership or developer commands change. If no
documentation change is needed, the external work record explains why. A separate
verifier checks the impact and preserved guarantees; a checked box is insufficient.

## Markdown and navigation

Use one descriptive H1, ordered heading levels, concise prose, fenced code with a
language, descriptive links and explicit examples. Local paths and Markdown
anchors must resolve in the committed candidate. Prefer relative links within the
repository; product/agent authority remains in current Dropbox Dev. Use Markdown
links instead of raw HTML link/image elements. No machine-specific absolute paths,
private download URLs, generated run reports or historical execution instructions.

Pinned markdownlint, markdown-it and GitHub slug generation check whole changed
Markdown files and all committed documents' local links. Inline lint-disable
comments cannot waive checks. Link checks are offline: external URL availability
and non-Markdown fragment targets still need relevant manual review. Syntax does
not prove that a guide is accurate, complete or useful.

## Authority and evidence

Repository documentation describes reusable source usage and development
conventions. Dropbox Dev contains the lean current product and agent contracts.
Dropbox Metadata contains immutable packets, progress, attribution, qualifications,
outputs and review receipts. Do not duplicate those records into Git or Dev.

Run `make check-documentation BASE_SHA=<actual PR base>` and the code/Ginkgo
checks before review. All required CI gates must pass for the exact candidate.
No documentation, framework conversion or source-policy pass grants merge or
runtime admission.
