package swarm

import (
	"errors"
	"fmt"
	"slices"
)

// ErrNotExecutable marks a manifest that cannot run in execute mode: it names
// no coordinator, or names a profile that is not loaded. Callers map it to a
// usage error, before anything is created.
var ErrNotExecutable = errors.New("swarm: manifest cannot be executed")

// CheckExecutable decides whether m can run in execute mode against the named
// profiles: a coordinator is required and every profile the manifest names (the
// coordinator and each packet role) must be among profiles. The first problem
// found is returned, naming the packet id for a role. Nothing is read or
// written; the roles stay data, so no role name is known to this package.
func CheckExecutable(m Manifest, profiles []string) error {
	if m.Coordinator == "" {
		return fmt.Errorf("%w: manifest %s has no coordinator: add `coordinator: <profile>`", ErrNotExecutable, m.Name)
	}
	if !slices.Contains(profiles, m.Coordinator) {
		return fmt.Errorf("%w: coordinator profile %q is not loaded", ErrNotExecutable, m.Coordinator)
	}
	for _, p := range m.Packets {
		if p.Role != "" && !slices.Contains(profiles, p.Role) {
			return fmt.Errorf("%w: packet %s: role profile %q is not loaded", ErrNotExecutable, p.ID, p.Role)
		}
	}
	return nil
}
