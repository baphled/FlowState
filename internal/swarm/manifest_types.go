package swarm

// Gate is one meaningful completion gate for a configured swarm: the
// named check that must pass, expressed as a command the gate runner
// executes, together with a human description of what passing means.
type Gate struct {
	// Name identifies the gate within the manifest.
	Name string
	// Description explains, for a human reviewer, what passing means.
	Description string
	// Command is the command the gate runner executes.
	Command string
}

// Manifest is a configured swarm: the packets it may admit and the
// gates that must all pass before the swarm's outcome is accepted.
type Manifest struct {
	// Name is the swarm name.
	Name string
	// Scope is the dot-separated capability scope every packet must
	// stay within.
	Scope string
	// Packets are the admission packets of the swarm.
	Packets []Packet
	// Gates are the completion gates; all must pass for acceptance.
	Gates []Gate
}
