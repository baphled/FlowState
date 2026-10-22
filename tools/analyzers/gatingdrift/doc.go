// Package gatingdrift provides a static analyser that flags struct
// fields whose godoc names a gating identifier (e.g. "declared in
// Capabilities.MCPServers") when the enclosing package no longer reads
// that identifier — Guard 3 of the review-pattern guards.
//
// # Overview
//
// Why: commit b960869 ("wire MCP tools to bypass manifest whitelist")
// stripped the manifest gate from buildAllowedToolSet but left the
// docstring on Config.MCPServerTools claiming the gate still applied.
// The reviewer (the same author) had no oracle to disagree with the
// behaviour change, and the test was rewritten to pin it. This analyser
// would have refused the commit by reporting that the docstring's
// gating identifier was no longer present anywhere in the engine
// package.
//
// # Algorithm
//
// Deliberately narrow per the user's "200 LOC ceiling, ship narrow"
// directive. The analyser:
//
//  1. Walks every struct type declaration.
//  2. For each field with a doc comment, extracts gating identifiers of
//     the form "<Phrase> <Capitalised>.<Capitalised>" where <Phrase> is
//     one of: "declared in", "gated by", "controlled by", "filtered by".
//     The phrase requirement keeps false positives down — incidental
//     mentions of dotted identifiers (e.g. "see foo.Bar for context")
//     are not flagged.
//  3. For each gating identifier, checks whether any *ast.SelectorExpr
//     in the same package has matching X / Sel names.
//  4. Reports a diagnostic on the struct type when the identifier is
//     named but not read.
//
// # Known Gaps
//
// Out of scope (see the b960869 commit body for rationale):
//
//   - Cross-package gates (the gating identifier might live in another
//     package). The b960869 case is intra-package, so this is a useful
//     start.
//   - Type-aware resolution. We compare names only; a field named
//     "MCPServers" on an unrelated type still counts as a read.
//   - Graceful handling of dot-imported packages.
//
// # Usage
//
// Run via the Makefile:
//
//	make check-gating-drift
//
// Or use directly with go vet:
//
//	go vet -vettool=$(which gatingdrift) ./...
package gatingdrift
