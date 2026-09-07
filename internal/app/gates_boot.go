package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

// discoverGateManifests runs gates.Discover over cfg.GatesDir once so
// boot helpers share a single filesystem walk. A missing or unconfigured
// gates dir yields nil manifests without error — boot proceeds with no
// external gates.
//
// Expected: parameters for discoverGateManifests.
// Returns: discovered manifests and any discovery error.
// Side effects: None.
func discoverGateManifests(cfg *config.AppConfig) ([]gates.Manifest, error) {
	if cfg == nil || cfg.GatesDir == "" {
		return nil, nil
	}
	manifests, err := gates.Discover(cfg.GatesDir)
	if err != nil {
		return nil, fmt.Errorf("gate discovery: %w", err)
	}
	return manifests, nil
}

// RegisterDiscoveredGates walks cfg.GatesDir and registers each manifest
// in the swarm-package's ext-gate registry. Per-gate failures are
// returned as a slice without aborting boot — adjacent gates still
// register, and a swarm referencing a failed gate fails per its
// failurePolicy at dispatch time. ctx is reserved for future cancel-
// during-discovery support; v0 discovery is synchronous and fast.
//
// Expected: parameters for RegisterDiscoveredGates.
// Returns: result of RegisterDiscoveredGates.
// Side effects: None.
func RegisterDiscoveredGates(_ context.Context, cfg *config.AppConfig) []error {
	manifests, err := discoverGateManifests(cfg)
	if err != nil {
		return []error{err}
	}
	var errs []error
	for _, m := range manifests {
		if err := swarm.RegisterExtGateFromManifest(m); err != nil {
			errs = append(errs, fmt.Errorf("register %q: %w", m.Name, err))
		}
	}
	return errs
}

// ValidateDiscoveredGateReferences checks every swarm in reg that
// references an ext: gate kind against the manifests actually
// discovered under cfg.GatesDir. It runs at boot after gate
// registration and surfaces a swarm pointing at a gate whose manifest
// is malformed, incomplete (skipped by discovery), or absent — the
// gap left open by ValidateRegistryGateKinds, which only sees gates
// that registered successfully.
//
// Expected:
//   - cfg carries the gates dir to re-discover (nil/empty dir is a
//     no-op returning nil).
//   - reg is the populated swarm registry (nil is a no-op).
//
// Returns:
//   - nil when every ext: reference resolves to a discovered manifest
//     or reg is empty/nil.
//   - An error naming the offending swarm, gate, and kind otherwise.
//
// Side effects: None.
func ValidateDiscoveredGateReferences(_ context.Context, cfg *config.AppConfig, reg *swarm.Registry) error {
	if reg == nil {
		return nil
	}
	manifests, err := discoverGateManifests(cfg)
	if err != nil {
		return err
	}
	for _, m := range reg.List() {
		if err := swarm.ValidateDiscoveredExtGateKinds(m.Harness.Gates, manifests); err != nil {
			return fmt.Errorf("swarm %q: %w", m.ID, err)
		}
	}
	return nil
}
