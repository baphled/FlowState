package engine

import (
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
)

// Constrained by [[ADR - Agent Model Contract]]: summarisation model
// selection MUST flow through CategoryResolver, keyed on a tier descriptor
// (the agent's ContextManagement.SummaryTier). It MUST NOT be hard-wired
// to a specific chat provider or to the agent's Complexity field — those
// govern the chat route, not the summariser route. The contract is
// enforced by internal/engine/summariser_resolver_test.go (regression
// guard "routes through CategoryResolver, not chat provider").
//
// Consumer: the L2 auto-compactor in internal/context will receive a
// SummariserResolver as an injected dependency (dependency-inversion —
// internal/engine imports internal/context, so the resolver lives here
// and the consumer accepts an interface at the call site to avoid a
// cycle).

// DefaultSummaryTier is the tier used when a manifest's
// ContextManagement.SummaryTier is empty. Matches the default applied by
// internal/agent/loader.go applyDefaults, so resolvers behave consistently
// whether or not the manifest was loaded via the loader pipeline.
const DefaultSummaryTier = "quick"

// ErrNilCategoryResolver is returned when SummariserResolver is
// constructed (or otherwise invoked) with a nil underlying CategoryResolver.
// Exposed so callers can distinguish misconfiguration from a missing tier.
var ErrNilCategoryResolver = errors.New("summariser resolver: category resolver is nil")

// ErrNilManifest is returned when ResolveForManifest is called with a nil
// manifest pointer. Exposed so callers can distinguish this defensive
// guard from a CategoryResolver lookup miss.
var ErrNilManifest = errors.New("summariser resolver: manifest is nil")

// SummariserResolver routes summarisation workloads to their configured
// model via the CategoryResolver. It is intentionally a narrow adapter:
// it takes a manifest, reads the summary tier, defaults an empty tier to
// DefaultSummaryTier, and delegates to CategoryResolver.Resolve.
//
// Implementations must NOT embed any direct provider reference. The whole
// point of the adapter is to keep the summariser route pluggable through
// category configuration, so a deployment can retarget summarisation to a
// different provider (e.g. a cheap local model) without touching code.
type SummariserResolver interface {
	// ResolveForManifest returns the CategoryConfig for the manifest's
	// summary tier. An empty tier is normalised to DefaultSummaryTier.
	ResolveForManifest(m *agent.Manifest) (CategoryConfig, error)
}

// categoryBackedSummariserResolver is the production implementation of
// SummariserResolver. It wraps a CategoryResolver and performs no logic
// beyond tier defaulting and delegation.
type categoryBackedSummariserResolver struct {
	categoryResolver *CategoryResolver
}

// NewSummariserResolver constructs a SummariserResolver that routes
// summarisation requests through the supplied CategoryResolver.
//
// Expected:
//   - r may be nil; in that case every call to ResolveForManifest returns
//     ErrNilCategoryResolver so misconfiguration surfaces as a typed
//     error rather than a nil-pointer panic inside the hot path.
//
// Returns:
//   - A SummariserResolver wrapping r. Never nil.
//
// Side effects:
//   - None.
func NewSummariserResolver(r *CategoryResolver) SummariserResolver {
	return &categoryBackedSummariserResolver{categoryResolver: r}
}

// ResolveForManifest returns the CategoryConfig the manifest's summary
// tier maps to. The tier is read from m.ContextManagement.SummaryTier and
// normalised to DefaultSummaryTier when empty (matching loader defaults).
// Lookup is delegated to the wrapped CategoryResolver; unknown tiers
// propagate its errUnknownCategory sentinel.
//
// Expected:
//   - m is a non-nil *agent.Manifest. A nil manifest returns
//     ErrNilManifest.
//
// Returns:
//   - The CategoryConfig produced by CategoryResolver.Resolve(tier) for
//     the manifest's summary tier (or DefaultSummaryTier if empty).
//   - ErrNilCategoryResolver if the resolver was constructed with a nil
//     CategoryResolver.
//   - ErrNilManifest if m is nil.
//   - Any error returned by CategoryResolver.Resolve wrapped with
//     tier context for diagnostics.
//
// Side effects:
//   - None beyond those of the wrapped CategoryResolver.Resolve call
//     (which may consult its ModelLister to resolve abstract descriptors).
func (s *categoryBackedSummariserResolver) ResolveForManifest(m *agent.Manifest) (CategoryConfig, error) {
	if s.categoryResolver == nil {
		return CategoryConfig{}, ErrNilCategoryResolver
	}
	if m == nil {
		return CategoryConfig{}, ErrNilManifest
	}
	tier := m.ContextManagement.SummaryTier
	if tier == "" {
		tier = DefaultSummaryTier
	}
	cfg, err := s.categoryResolver.Resolve(tier)
	if err != nil {
		return CategoryConfig{}, fmt.Errorf("resolving summary tier %q: %w", tier, err)
	}
	return cfg, nil
}
