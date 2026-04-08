package recall

import "errors"

type DiscoveryQuery struct {
	Kind     string
	Affects  string
	Priority string
}

// Discovery represents a published discovery event.
type Discovery struct {
	Kind     string
	Summary  string
	Details  string
	Affects  string
	Priority string
	Evidence string
}

// ErrDiscoveryNotFound is returned when a discovery cannot be found.
var ErrDiscoveryNotFound = errors.New("discovery not found")
