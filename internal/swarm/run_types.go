package swarm

// RunResult is the outcome of one configured-swarm run: which
// packets were admitted, the per-gate outcomes and whether every
// gate accepted the run.
type RunResult struct {
	// Manifest is the manifest the run drove.
	Manifest Manifest
	// Admitted lists packet ids in admission order.
	Admitted []string
	// Gates is the aggregated gate decision of the run.
	Gates GatesDecision
}

// RunEvents is the append-only sink the run loop reports progress
// to; nil events are ignored.
type RunEvents interface {
	// PacketAdmitted reports one packet admitted to the run.
	PacketAdmitted(p Packet)
	// PacketRejected reports one packet refused admission with its
	// reason.
	PacketRejected(id, reason string)
}

// RunAdmitter is the admission seam the run loop drives; the
// concrete AdmissionLog implements it.
type RunAdmitter interface {
	// Admit attempts to admit one packet pinned to its source body.
	Admit(p Packet, sourceBody []byte) error
}

// RunReceipts is the durable sink for run progress: admission
// events and gate outcomes persisted beyond the process lifetime.
type RunReceipts interface {
	// RecordAdmission persists one admission decision.
	RecordAdmission(runID string, p Packet, admitted bool, reason string) error
	// RecordGates persists the aggregated gate decision of one run.
	RecordGates(runID string, d GatesDecision) error
}

// manifestDoc mirrors Manifest with explicit yaml field names; the
// exported Manifest stays tag-free for its Go consumers.
type manifestDoc struct {
	Name    string      `yaml:"name"`
	Scope   string      `yaml:"scope"`
	Packets []packetDoc `yaml:"packets"`
	Gates   []gateDoc   `yaml:"gates"`
}

// packetDoc mirrors Packet with snake_case yaml keys.
type packetDoc struct {
	ID            string   `yaml:"id"`
	Scope         string   `yaml:"scope"`
	Task          string   `yaml:"task"`
	SourceSHA256  string   `yaml:"source_sha256"`
	ParentID      string   `yaml:"parent_id"`
	Prerequisites []string `yaml:"prerequisites"`
}

// gateDoc mirrors Gate with snake_case yaml keys.
type gateDoc struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Command     string `yaml:"command"`
}
