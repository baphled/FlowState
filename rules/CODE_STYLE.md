# Code style

Follow [the owning-package contract](ARCHITECTURE.md), [documentation standards](DOCUMENTATION.md)
and [the Ginkgo test contract](BDD_WORKFLOW.md). Current product and agent authority
is Dropbox Dev; this page defines reusable source conventions.

## Names and layout

Use snake_case Go filenames, lowercase cohesive package names, PascalCase exported
names and camelCase private names. Prefer consumer-owned narrow interfaces.
Named package types and aliases live in `types.go` or `*_types.go` within their
owner; methods, functions and mutable variables live in implementation files.

## Readability and errors

Keep functions focused and within the contract's size/complexity limits. Use
explicit dependencies and constructors rather than ambient mutable wiring. Group
standard-library, external and internal imports consistently. Handle returned
errors and preserve their context; do not silence failures to obtain green CI.

Use comments to document public guarantees and non-obvious invariants. Do not
narrate implementation statements or add inline suppression directives. Track
actual work and findings in Dropbox Metadata, outside the repository.

## Verification

Use gofmt, the pinned golangci profiles, source-contract checks and Ginkgo/Gomega.
Check whole changed production files and documentation. A passing tool does not
replace a separate review of modularity, meaningful tests and accurate examples.
