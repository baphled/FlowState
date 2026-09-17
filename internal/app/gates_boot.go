package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/gates"
	"github.com/baphled/flowstate/internal/swarm"
)

// discoverGateManifests runs gates.Discover over cfg.GatesDir once so
// boot helpers share a single filesystem walk. A missing or unconfigured
// gates dir yields nil manifests without error — boot proceeds with no
// external gates. Discovery skip-and-collects malformed manifests, so a
// non-nil error can accompany the valid manifests it returned; callers
// decide how much of that partial failure is boot-fatal.
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
		return manifests, fmt.Errorf("gate discovery: %w", err)
	}
	return manifests, nil
}

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
	manifests, err := discoverGateManifests(cfg)
	var errs []error
	if err != nil {
		errs = append(errs, err)
	}
	for _, m := range manifests {
		if regErr := swarm.RegisterExtGateFromManifest(m); regErr != nil {
			errs = append(errs, fmt.Errorf("register %q: %w", m.Name, regErr))
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
// that registered successfully. Partial discovery failures (skipped
// malformed manifests) are not boot-fatal here: RegisterDiscoveredGates
// already surfaces them at error level, and boot stays non-fatal per
// the discovery-resilience contract.
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
	manifests, _ := discoverGateManifests(cfg)
	for _, m := range reg.List() {
		if err := swarm.ValidateDiscoveredExtGateKinds(m.Harness.Gates, manifests); err != nil {
			return fmt.Errorf("swarm %q: %w", m.ID, err)
		}
	}
	return nil
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
