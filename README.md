# FlowState

FlowState is a Go backend under development. The current milestone is a native,
CLI/headless session engine with replaceable provider/tool ports and ordered
SQLite events. This repository contains reusable implementation and tests;
current product scope and agent execution authority live in Dropbox Dev.

## Start work

Read [the agent bootstrap](AGENTS.md), then the actual current Dropbox
`/FlowState/Dev/00-AGENT-READ-FIRST.md` and `/FlowState/Metadata/ACTIVE.json`.
Resolve the assigned bounded packet before editing. Do not infer readiness or
qualification from a repository description, branch name or old report.

Development uses registered feature worktrees inside the actual bare root and
PRs to `next`. Read [the source ownership contract](rules/ARCHITECTURE.md) and
[current architecture guide](docs/architecture/OVERVIEW.md). No prototype/TUI
architecture is an execution input for this milestone.

## Build and verify

Use the Go version recorded in `go.mod`. Run commands from the repository root.
Node.js 22 or newer and the pinned npm dependencies support documentation checks.
Install the pinned golangci-lint version used by Actions for the code-quality gate.

```sh
npm ci
make build
make check
make check-code-quality BASE_SHA=<actual-pr-base-sha>
make check-documentation BASE_SHA=<actual-pr-base-sha>
```

`make check` covers formatting, available lint and the test route. The explicit
contract commands also validate committed candidate structure, whole changed code
quality, Markdown and local navigation. Required Actions jobs run the same gates;
no model/Copilot quota is needed. Use [the Ginkgo contract](rules/BDD_WORKFLOW.md)
for new and modified tests and [documentation standards](rules/DOCUMENTATION.md)
for public interfaces and guides.

## Evidence and scope

Keep project packets, AI attribution, outputs, qualifications and review evidence
in Dropbox `/FlowState/Metadata`, outside the checkout and Dev input pack.
Reusable source, tests, schemas and synthetic fixtures belong in Git. Runtime
application data belongs in configured user storage, not the code checkout.

A green source check does not certify complete product implementation, live model
availability, T019 qualification or merge permission. Current decisions and
candidate-bound acceptance evidence determine the next eligible work item.

## Security regression gate

CI pins gosec v2.30.0 and scans immutable Git snapshots of the candidate and
its actual PR base (or the push event's before SHA). New findings at every severity
fail the build; inherited findings are reported as unresolved debt. This gate is
not a security clearance or proof that each scanner alert is exploitable.

With Go from `go.mod`, Python 3.11+ and pinned gosec on `PATH`, run from the
repository root (use an actual full ancestor commit SHA and a fresh output path):

```bash
python3 -B scripts/security_gate.py --base "$BASE_SHA" --output /tmp/security-results
```

The gate retains full native JSON and SARIF reports for both revisions, plus
scanner exits and logs. Missing bases, scanner/import errors and invalid reports
fail closed. Snapshots include all tracked regular blobs regardless of archive
attributes; tracked symlinks and gitlinks are rejected. Reports must be outside the
Git worktree and common directory.
Matching uses file, rule, details, offending code, severity and
confidence with duplicate counts; line shifts alone do not create a new finding.
Renames or changed offending code require review as new findings. Detection remains
limited to gosec's active-build analysis. Both scans ignore source `#nosec`
suppressions so new annotations cannot hide alerts; inherited debt requires
separate security review and remediation.
