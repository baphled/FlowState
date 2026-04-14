package context

import "github.com/baphled/flowstate/internal/provider"

// ExportedWalkUnits is a test-only shim exposing walkUnits to external test
// packages. It is defined in an _test.go file so the production build does
// not expose the walker's private contract.
func ExportedWalkUnits(msgs []provider.Message) []Unit {
	return walkUnits(msgs)
}
