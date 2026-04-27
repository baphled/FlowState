package app

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

// RegisterDiscoveredGates walks cfg.GatesDir and registers each manifest
// in the swarm-package's ext-gate registry. Per-gate failures are
// returned as a slice without aborting boot — adjacent gates still
// register, and a swarm referencing a failed gate fails per its
// failurePolicy at dispatch time. ctx is reserved for future cancel-
// during-discovery support; v0 discovery is synchronous and fast.
func RegisterDiscoveredGates(_ context.Context, cfg *config.AppConfig) []error {
	if cfg == nil || cfg.GatesDir == "" {
		return nil
	}
	manifests, err := gates.Discover(cfg.GatesDir)
	if err != nil {
		return []error{fmt.Errorf("gate discovery: %w", err)}
	}
	var errs []error
	for _, m := range manifests {
		if err := swarm.RegisterExtGateFromManifest(m); err != nil {
			errs = append(errs, fmt.Errorf("register %q: %w", m.Name, err))
		}
	}
	return errs
}
