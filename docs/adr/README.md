---
title: Architecture Decision Records
---

# Architecture Decision Records

This directory holds the binding architecture decisions that regression tests
in this repository encode. The primary editable copies of these documents live
in the FlowState Obsidian vault; the files here are snapshots kept alongside
the code so reviewers working from a clone of the repository can read the
invariants without out-of-band access.

## Index

| ADR | Scope | Status |
|---|---|---|
| [ADR - View-Only Context Compaction](./ADR%20-%20View-Only%20Context%20Compaction.md) | Compaction is a view-only transformation; it must not mutate `session.Messages` or the recall store. | Proposed |
| [ADR - Tool-Call Atomicity in Context Compaction](./ADR%20-%20Tool-Call%20Atomicity%20in%20Context%20Compaction.md) | Compaction operates on indivisible "compactable units", never splitting a `tool_use`/`tool_result` pair. | Proposed |
| [ADR - Dual-Scope Gate Runner](./ADR%20-%20Dual-Scope%20Gate%20Runner.md) | Swarm gates fire at four lifecycle points (`pre`, `post`, `pre-member`, `post-member`); `kind: builtin:result-schema` validates structured outputs against a registry where file discovery overrides programmatic seed. | Accepted |
| [ADR - Tool-Capability Allowlist Gate](./ADR%20-%20Tool-Capability%20Allowlist%20Gate.md) | Delegation fails closed when the resolved (provider, model) is not on the tool-capable allowlist; deny precedence over allow; single-`*` glob patterns. | Accepted |

## Keeping these in sync

Both files are verbatim snapshots at the date noted in their front matter.
The canonical editable copy lives in the FlowState Obsidian vault under
`Documentation/Architecture/ADR/`. When an ADR is amended in the vault, the
snapshot here must be refreshed in the same change-set; the footer note on
each file restates that rule for readers who land on the snapshot directly.

Cross-document links of the form `[[Other Document]]` are Obsidian wiki-links
preserved verbatim from the vault; they will not resolve in GitHub's Markdown
renderer. The document titles are stable enough to search for when needed.
