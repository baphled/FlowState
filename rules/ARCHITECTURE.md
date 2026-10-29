# Code structure contract

Read the current Dropbox `/FlowState/Dev/architecture.md` and agent rules before
work. This file implements the accepted M1 boundaries; it does not invent a new
product architecture or expand the milestone. No TUI/prototype layer is a source
for this backend.

## Ownership and dependency direction

| Area | Responsibility | Dependency boundary |
| --- | --- | --- |
| `cmd/flowstate` | Input parsing, output and stable exits | Application/configuration; no direct engine, provider or store wiring |
| `internal/app` | Composition and use cases | Wire domain ports to concrete adapters explicitly |
| `internal/runtime` | Turn lifecycle and sequencing | Domain/ports; no HTTP, SQL or provider SDK |
| `internal/session` | Session identity and state invariants | Domain/ports; no concrete store, HTTP or SQL |
| `internal/provider` | Provider port and domain messages | Standard library/domain types; adapters live in subpackages |
| `internal/tool` | Tool port, permission and loop policy | Domain/ports; storage behind a narrow consumer port |
| `internal/store`, `internal/storage` | Persistence adapters | No application, CLI or engine lifecycle dependency |
| `internal/provider/*`, `internal/oauth` | External transports/authentication | Implement ports; no application, CLI or lifecycle ownership |
| `internal/config` | Configuration values and validation | Configuration is passed into composition, not ambient domain wiring |

Production Go roots are `cmd/flowstate`, the classified `internal` areas, and
`scripts` for reusable tooling. New roots/public packages need an explicit ownership
and guard disposition. Internal code never imports `cmd`. New internal areas need an explicit ownership
and dependency disposition plus guard updates in the same reviewed change. A
renamed or nested package must not escape its layer. Tests may deliberately wire
adapters for integration; production dependency rules remain mandatory.

## Cohesion and file layout

Keep a package cohesive around one responsibility. Consumer-owned interfaces are
small and express only the operations the consumer needs. Do not expose concrete
adapter handles or move business logic into CLI/adapters to bypass a boundary.
Use explicit composition and injected collaborators; avoid mutable global wiring.

Package-level named types and aliases belong in `types.go` or `*_types.go` within
the owning package. Methods/functions and mutable variables belong in
implementation files; related constants may remain with types. Test fixture types
may stay in tests. Do not create catch-all `types`, `common`, `util`, `utils` or
`helpers` packages. Do not split types into a generic shared package.

Functions should do one thing. Changed production files are checked at 60 lines,
40 statements, a cyclomatic-complexity limit of 15, and at most 10 interface methods.
These are review boundaries, not targets to game by wrapping unrelated work.

## Enforcement and refactoring

`make check-code-contract BASE_SHA=<actual PR base>` parses committed changed
production files, including build-tagged/platform files. It checks layout,
classified ownership, forbidden imports and inline waiver directives. The
additional pinned golangci contract profile checks whole changed files for
`depguard`, `revive` API/package comments, function size, complexity and interfaces.

Untouched legacy violations remain visible refactoring debt. Once a production
file is changed, its whole contract applies; do not suppress the result with a
comment, generated marker, file rename or altered working copy. Record findings
and accepted scope in Dropbox Metadata. AST checks cover all changed tag/platform
variants; function-size, complexity and interface metrics run in the active Go
build. Other build variants need their relevant target lint/review; host success
does not waive those limits. A gate passes source checks, not proof of
cohesion, correct invariants, real independence or runtime readiness.

Use the [Ginkgo test contract](BDD_WORKFLOW.md). A separate verifier checks
responsibility, public surface, failure/cancellation semantics, documentation and
preserved behaviour. No merge is authorized by a source-contract PASS.
