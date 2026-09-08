package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

// RegisterDiscoveredGates walks cfg.GatesDir and registers each manifest
// in the swarm-package's ext-gate registry. Per-gate failures are
// returned as a slice without aborting boot — discovery skips
// malformed manifests so valid siblings still register, and a swarm
// referencing a failed gate fails per its failurePolicy at dispatch
// time. ctx is reserved for future cancel-during-discovery support;
// v0 discovery is synchronous and fast.
//
// Expected: parameters for RegisterDiscoveredGates.
// Returns: result of RegisterDiscoveredGates.
// Side effects: None.
func RegisterDiscoveredGates(_ context.Context, cfg *config.AppConfig) []error {
	if cfg == nil || cfg.GatesDir == "" {
		return nil
	}
	manifests, err := gates.Discover(cfg.GatesDir)
	if err != nil {
		// Discover now skip-and-collects: manifests holds every valid
		// sibling, err carries the per-directory failures. Register
		// the valid manifests FIRST, then surface the skipped dirs.
		var errs []error
		for _, m := range manifests {
			if regErr := swarm.RegisterExtGateFromManifest(m); regErr != nil {
				errs = append(errs, fmt.Errorf("register %q: %w", m.Name, regErr))
			}
		}
		return append([]error{fmt.Errorf("gate discovery under %s: %w", cfg.GatesDir, err)}, errs...)
	}
	var errs []error
	for _, m := range manifests {
		if err := swarm.RegisterExtGateFromManifest(m); err != nil {
			errs = append(errs, fmt.Errorf("register %q: %w", m.Name, err))
		}
	}
	return errs
}

// SummariseGateRegistrationFailures renders a boot log line naming
// every offending gate so an operator can locate the bad manifest
// from the error alone. Boot stays non-fatal; the caller logs this
// summary at error level.
//
// Expected: parameters for SummariseGateRegistrationFailures.
// Returns: result of SummariseGateRegistrationFailures.
// Side effects: None.
func SummariseGateRegistrationFailures(errs []error) string {
	if len(errs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(errs))
	for _, err := range errs {
		if err != nil {
			parts = append(parts, err.Error())
		}
	}
	return fmt.Sprintf("ext gate registration: %d failure(s): %s", len(parts), strings.Join(parts, "; "))
}
