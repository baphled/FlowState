// Package docblocks provides an analyser that enforces structured documentation
// on all exported symbols in Go packages.
//
// # Overview
//
// The docblocks analyser ensures that all exported types, functions, constants,
// and variables have proper godoc comments following the project's documentation
// standards.
//
// # Checks Performed
//
//   - Package-level doc.go files exist with proper package documentation
//   - Exported types have godoc comments starting with the type name
//   - Exported functions have structured documentation (Expected, Returns, Side effects)
//   - Exported constants and variables have descriptive comments
//
// # Usage
//
// Run via the Makefile:
//
//	make check-docblocks
//
// Or use directly with go vet:
//
//	go vet -vettool=$(which docblocks) ./...
package docblocks
