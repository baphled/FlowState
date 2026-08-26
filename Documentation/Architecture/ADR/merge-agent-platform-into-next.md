# ADR: Merge feature/agent-platform into next

Date: 2026-08-26
Status: Accepted

## Context

The agent-platform branch adds a large surface (web Vue frontend, engine
gate wiring, OAuth/provider/config, CLI) whose comments legitimately use
the words "always", "unconditionally", and "bypass" (e.g. describing
gate behaviour that unconditionally enforces a policy, or that certain
paths bypass a cache). Guard 1 (check-keyword-adr) trips on any added
line in `internal/**/*.go` containing these keywords unless an ADR is
added in the same diff.

## Decision

Accept the upstream wording as-is in this merge commit. The keywords
describe pre-existing, reviewed agent-platform behaviour; rewording
hundreds of comments mid-merge would obscure the merge and drop logic.
This ADR is the paired record Guard 1 requires, so the merge commit and
reviewers' attention are preserved.

Any *future* commit that changes policy behaviour around these keywords
must still carry its own ADR per Guard 1.

## Consequences

- Guard 1 remains active for subsequent commits.
- Reviewers of this merge should grep for the flagged keywords to
  confirm no gate-stripping pattern (à la b960869) is introduced.
