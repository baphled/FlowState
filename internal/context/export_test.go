package context

import "github.com/baphled/flowstate/internal/provider"

// ExportedWalkUnits is a test-only shim exposing walkUnits to external test
// packages. It is defined in an _test.go file so the production build does
// not expose the walker's private contract.
func ExportedWalkUnits(msgs []provider.Message) []Unit {
	return walkUnits(msgs)
}

// ExportedWriteJob exposes the internal writeJob helper to error-path tests.
// Callers construct the job from public types via the shim below.
func ExportedWriteJob(storagePath string, kind UnitKind, msgs []provider.Message) error {
	job := persistJob{
		record:  CompactedMessage{StoragePath: storagePath},
		payload: CompactedUnit{Kind: kind, Messages: msgs},
	}
	return writeJob(job)
}
