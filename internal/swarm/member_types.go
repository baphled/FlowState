package swarm

// MemberIdentity names one admitted member of a configured swarm:
// the packet id plus the run (manifest) it belongs to.
type MemberIdentity struct {
	// RunName is the manifest name of the run.
	RunName string
	// PacketID is the admitted packet the member acts for.
	PacketID string
}

// MemberClaim is the proof a member presents to emit a receipt
// against a run: it must be an admitted packet of that run.
type MemberClaim struct {
	// Identity is the claiming member.
	Identity MemberIdentity
	// GateOutcome is the member's observed outcome (pass or fail).
	GateOutcome string
	// Evidence is the free-form evidence body backing the outcome.
	Evidence string
}

// MemberReceipt is the durable record emitted by an admitted
// member: identity, outcome and the evidence digest.
type MemberReceipt struct {
	// Identity is the emitting member.
	Identity MemberIdentity
	// GateOutcome is the member's observed outcome.
	GateOutcome string
	// EvidenceSHA256 pins the evidence body.
	EvidenceSHA256 string
}

// MemberAuthorizer admits member claims: a claim may emit a
// receipt only for an admitted packet of its run.
type MemberAuthorizer interface {
	// AuthorizeMember verifies the claim against run admission.
	AuthorizeMember(claim MemberClaim) error
}

// MemberReceiptSink persists member receipts.
type MemberReceiptSink interface {
	// RecordMemberReceipt stores one emitted receipt.
	RecordMemberReceipt(rec MemberReceipt) error
}

// admissionAuthorizer adapts one run's admission result into a
// MemberAuthorizer.
type admissionAuthorizer struct {
	runName  string
	admitted map[string]bool
}
