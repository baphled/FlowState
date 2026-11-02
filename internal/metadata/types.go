// Package metadata emits attempt records into the Dropbox-backed
// metadata root ($FLOWSTATE_METADATA_ROOT, default
// /home/baphled/Dropbox/FlowState/Metadata): an immutable
// attempts/<id>/ directory holding packet.json, attribution.json,
// receipt.json and artefacts/, every file verified by an
// upload-then-readback byte comparison, and each successful receipt
// indexed in the SQLite receipts table.
package metadata

// Attribution is the runtime-observed attribution recorded in
// attribution.json. Values are observed at runtime (provider client,
// session row, harness); a requested model label is never hardcoded.
type Attribution struct {
	Provider       string `json:"provider"`
	ModelID        string `json:"model_id"`
	Harness        string `json:"harness"`
	AuthorIdentity string `json:"author_identity"`
	Source         string `json:"source"`
}

// Artefact is one immutable artefact placed under artefacts/.
type Artefact struct {
	Name string // relative name inside artefacts/
	Body []byte
}

// Attempt is the inbound attempt record awaiting emission.
type Attempt struct {
	ID           string // e.g. 2026-11-02-m1-dogfood
	CapabilityID string
	CommitSHA    string
	GateOutcome  string // pass or fail
	Attribution  Attribution
	Artefacts    []Artefact
}

// AttemptRecord is the emission result: every path written and its
// SHA-256 digest (self-consistent with the digests map recorded in
// packet.json).
type AttemptRecord struct {
	AttemptDir      string
	PacketPath      string
	AttributionPath string
	ReceiptPath     string
	PacketSHA256    string
	ReceiptSHA256   string
}

// attemptDoc is an attempt-dir JSON doc; marshalling splices the running
// digest map into any doc carrying artefact_digests.
type attemptDoc struct {
	name string
	doc  any
}
