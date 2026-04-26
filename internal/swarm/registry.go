package swarm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// ErrSwarmDirNotFound mirrors agent.ErrAgentDirNotFound: callers
// merging optional swarm directories use errors.Is to decide whether
// to log-and-continue or fail hard when the path is absent.
var ErrSwarmDirNotFound = errors.New("swarm directory not found")

// Registry is the in-memory store of swarm manifests, mirroring the
// shape of agent.Registry. The map is guarded by a sync.RWMutex
// because agent registries are typically populated once at startup
// and queried concurrently from request paths — the swarm registry
// will see the same access pattern once @Lead resolution lands in
// T-swarm-2, so the lock is added now to keep the contract consistent.
type Registry struct {
	mu        sync.RWMutex
	manifests map[string]*Manifest
}

// NewRegistry returns an empty, ready-to-use Registry. Mirrors
// agent.NewRegistry so swap-in/swap-out adapter helpers (validator
// wrappers, app-level setup helpers) can be written generically.
//
// Returns:
//   - A pointer to an initialised Registry with an empty manifest map.
//
// Side effects:
//   - None.
func NewRegistry() *Registry {
	return &Registry{manifests: make(map[string]*Manifest)}
}

// Register stores manifest under its ID, overwriting any prior entry
// with the same id. The caller is responsible for validation; the
// registry-aware validator path goes through NewRegistryFromDir.
// Returns silently on a nil manifest so misuse from a partial-load
// path does not panic.
//
// Expected:
//   - manifest is either nil or a Manifest pointer with a non-empty ID.
//
// Returns:
//   - Nothing.
//
// Side effects:
//   - Writes one entry to the registry's internal map under
//     manifest.ID, holding the registry mutex for the duration of the
//     write.
func (r *Registry) Register(manifest *Manifest) {
	if manifest == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.manifests[manifest.ID] = manifest
}

// Get returns the manifest registered under id and a boolean
// indicating presence. Mirrors agent.Registry.Get.
//
// Expected:
//   - id is a non-empty swarm identifier.
//
// Returns:
//   - The matching manifest pointer and true when registered.
//   - nil and false when no manifest is registered under id.
//
// Side effects:
//   - None (read-only access under the registry's RLock).
func (r *Registry) Get(id string) (*Manifest, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.manifests[id]
	return m, ok
}

// List returns every registered manifest sorted alphabetically by id.
// Returns nil when the registry is empty (matches agent.Registry.List
// so callers can drop one through the other without changing the
// nil-check). Sorting keeps log output and CLI listings stable across
// processes.
//
// Returns:
//   - A slice of manifests sorted by id.
//   - nil when the registry is empty.
//
// Side effects:
//   - None (read-only access under the registry's RLock).
func (r *Registry) List() []*Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.manifests) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.manifests))
	for id := range r.manifests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]*Manifest, 0, len(ids))
	for _, id := range ids {
		out = append(out, r.manifests[id])
	}
	return out
}

// AgentRegistry is the minimal surface NewRegistryFromDir needs from
// the agent registry to validate swarm manifests. Defining the
// interface in this package (rather than importing
// internal/agent.Registry directly) keeps the swarm package free of
// the agent dependency — the call site at app-construction time wraps
// the concrete *agent.Registry with this trivial adapter.
type AgentRegistry interface {
	// Get reports whether an agent id is registered. Only the boolean
	// is consumed; the manifest payload itself is never read by the
	// swarm validator.
	Get(id string) (any, bool)
}

// NewRegistryFromDir walks dir for *.yml / *.yaml manifests, parses
// and validates each one (agent + swarm registry-aware), and returns
// a populated Registry. Aggregated per-file errors surface in the
// returned error so a single broken manifest does not mask the rest.
//
// Validation runs in two passes:
//  1. File-load validation (Load) catches scalar / self-reference /
//     gate-prefix mistakes per file with no registry context.
//  2. Registry-aware re-validation runs after every manifest has
//     been registered so lead/member resolution can see sibling
//     swarms (per the §1 "members may include swarm ids" rule) and
//     so the cross-registry uniqueness check (swarm id must not
//     collide with an agent id) fires once the full set is known.
//
// agentReg may be nil — when nil, only the within-swarm rules are
// enforced. Pass a real adapter at app-startup so lead/member
// resolution actually checks the agent registry.
//
// Expected:
//   - dir is a path that may or may not exist; missing dirs return
//     ErrSwarmDirNotFound (use errors.Is to detect).
//   - agentReg is nil or a populated agent-registry adapter.
//
// Returns:
//   - A populated *Registry plus a possibly-non-nil aggregated error
//     when individual files failed validation but at least one manifest
//     loaded.
//   - nil registry and an error wrapping ErrSwarmDirNotFound when dir
//     is absent.
//
// Side effects:
//   - Reads YAML files from dir.
func NewRegistryFromDir(dir string, agentReg AgentRegistry) (*Registry, error) {
	manifests, loadErr := LoadDir(dir)
	if loadErr != nil && len(manifests) == 0 {
		// Either the directory is missing/unreadable, or every file
		// failed to load — either way the caller has nothing to
		// register, so surface the wrapped error directly.
		if strings.Contains(loadErr.Error(), "does not exist") {
			return nil, fmt.Errorf("%w: %q: %s", ErrSwarmDirNotFound, dir, loadErr.Error())
		}
		return nil, loadErr
	}

	reg := NewRegistry()
	for _, m := range manifests {
		reg.Register(m)
	}

	validator := combinedValidator{agents: agentReg, swarms: reg}

	var (
		errs []string
	)
	if loadErr != nil {
		errs = append(errs, loadErr.Error())
	}

	// Cross-registry uniqueness: a swarm id must not collide with an
	// agent id. Per §1, an `@<id>` mention is unambiguous only when
	// the id lives in exactly one registry.
	if agentReg != nil {
		for _, id := range sortedRegistryIDs(reg) {
			if _, found := agentReg.Get(id); found {
				errs = append(errs, fmt.Sprintf("swarm id %q collides with an agent id (ids must be unique across both registries)", id))
			}
		}
	}

	// Registry-aware re-validation + cycle check.
	swarmMap := snapshotSwarms(reg)
	for _, id := range sortedRegistryIDs(reg) {
		m, _ := reg.Get(id)
		if err := m.Validate(validator); err != nil {
			errs = append(errs, fmt.Sprintf("validating %q: %s", id, err.Error()))
			continue
		}
		if err := cycleCheck(m, swarmMap); err != nil {
			errs = append(errs, fmt.Sprintf("validating %q: %s", id, err.Error()))
		}
	}

	if len(errs) > 0 {
		return reg, fmt.Errorf("swarm registry load: %d error(s):\n%s", len(errs), strings.Join(errs, "\n"))
	}
	return reg, nil
}

// snapshotSwarms returns a map suitable for cycleCheck without
// holding the registry lock during the walk.
//
// Expected:
//   - reg is a non-nil *Registry.
//
// Returns:
//   - A new map[string]*Manifest copying reg's id -> manifest entries.
//
// Side effects:
//   - None (read-only access under reg.mu.RLock()).
func snapshotSwarms(reg *Registry) map[string]*Manifest {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	out := make(map[string]*Manifest, len(reg.manifests))
	for id, m := range reg.manifests {
		out[id] = m
	}
	return out
}

// sortedRegistryIDs returns the registry's swarm ids in sorted order
// so iteration is deterministic across processes.
//
// Expected:
//   - reg is a non-nil *Registry.
//
// Returns:
//   - A new slice of registered ids sorted lexicographically.
//
// Side effects:
//   - None (read-only access under reg.mu.RLock()).
func sortedRegistryIDs(reg *Registry) []string {
	reg.mu.RLock()
	defer reg.mu.RUnlock()
	ids := make([]string, 0, len(reg.manifests))
	for id := range reg.manifests {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// combinedValidator implements Validator by consulting an agent
// registry and a swarm registry in turn. Defined privately because
// app-startup wiring should construct one with the typed registries
// rather than open-coding the lookup logic.
type combinedValidator struct {
	agents AgentRegistry
	swarms *Registry
}

// HasAgent returns true when the agent registry recognises id. A nil
// agent registry returns false, which is the right thing for tests
// that want to validate against swarms only.
//
// Expected:
//   - id is a non-empty agent identifier candidate.
//
// Returns:
//   - true when the wrapped agent registry has id; false otherwise.
//
// Side effects:
//   - None.
func (c combinedValidator) HasAgent(id string) bool {
	if c.agents == nil {
		return false
	}
	_, ok := c.agents.Get(id)
	return ok
}

// HasSwarm returns true when the swarm registry recognises id. The
// validator does not exclude the manifest's own id here because
// Validate() calls validateSelfReference before the registry-aware
// resolver runs, so a self-referencing manifest is already rejected
// by the time HasSwarm is consulted.
//
// Expected:
//   - id is a non-empty swarm identifier candidate.
//
// Returns:
//   - true when the wrapped swarm registry has id; false otherwise.
//
// Side effects:
//   - None.
func (c combinedValidator) HasSwarm(id string) bool {
	if c.swarms == nil {
		return false
	}
	_, ok := c.swarms.Get(id)
	return ok
}
