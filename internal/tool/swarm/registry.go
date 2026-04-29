package swarm

import "github.com/baphled/flowstate/internal/swarm"

// SwarmReader is the read-only interface the swarm tools depend on.
// *swarm.Registry satisfies it.
type SwarmReader interface {
	Get(id string) (*swarm.Manifest, bool)
	List() []*swarm.Manifest
}
