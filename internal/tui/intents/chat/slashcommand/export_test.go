package slashcommand

import (
	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/swarm"
)

// NewModelChoiceForTest exposes the unexported modelChoice payload so
// external test packages can construct the value the /model handler
// expects without leaking the type into the public surface.
//
// Expected:
//   - providerName and modelName mirror the live SetModelPreference
//     arguments.
//
// Returns:
//   - An opaque modelChoice value boxed as any.
//
// Side effects:
//   - None.
func NewModelChoiceForTest(providerName, modelName string) any {
	return modelChoice{Provider: providerName, Model: modelName}
}

// ManifestWriterForTest is the seam the wizard's external specs use
// to inject a fake swarm-manifest writer. Production callers go
// through NewSwarmBuilder which builds a real *swarm.ManifestWriter;
// keeping this shim test-only ensures the writer plumbing does not
// leak onto the package's public surface.
type ManifestWriterForTest interface {
	Write(name string, m *swarm.Manifest) error
	Delete(name string) error
	Path(name string) string
}

// NewSwarmBuilderWithWriterForTest constructs a /swarm wizard wired
// to the supplied writer. Specs use this to assert the wizard
// delegates persistence and rollback to the writer rather than
// poking the filesystem directly — see
// internal/swarm/manifest_writer.go for the production type the
// wizard normally targets.
//
// Expected:
//   - agents and schemaNames mirror NewSwarmBuilder.
//   - writer is a non-nil ManifestWriterForTest implementation.
//
// Returns:
//   - A Wizard wired to the supplied writer.
//
// Side effects:
//   - None.
func NewSwarmBuilderWithWriterForTest(agents *agent.Registry, schemaNames []string, writer ManifestWriterForTest) Wizard {
	return newSwarmBuilderWithWriter(agents, schemaNames, writer)
}
