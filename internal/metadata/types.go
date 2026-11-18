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

// Attempt is the inbound attempt record awaiting emission. The
// command fields seed the single evidence row of the
// m1-acceptance-receipt-v2 schema; paths and digests are resolved by
// the emitter after the artefacts are written.
type Attempt struct {
	ID           string // e.g. 2026-11-02-m1-selftest
	CapabilityID string
	CommitSHA    string
	GateOutcome  string // pass or fail
	Attribution  Attribution
	Artefacts    []Artefact
	// JobID identifies the CI job admitting the attempt ("local"
	// outside CI); it lands in repo_state.job_id.
	JobID string
	// Command is the evidence command line (defaults to
	// Attribution.Source).
	Command string
	// ExpectedExitCode / ExitCode form the evidence row's codes.
	ExpectedExitCode int
	ExitCode         int
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

// receiptSchema is the schema label the validator requires.
const receiptSchema = "m1-acceptance-receipt-v2"

// artifactRef is an {path, sha256} object reference of the schema.
type artifactRef struct {
	Path  string `json:"path"`
	SHA64 string `json:"sha256"`
}

// repoState is the repo_state block of the v2 receipt.
type repoState struct {
	CommitSHA      string `json:"commit_sha"`
	ManifestSHA256 string `json:"manifest_sha256"`
	JobID          string `json:"job_id"`
	Dirty          bool   `json:"dirty"`
}

// evidenceRow is one command evidence row of the v2 receipt.
type evidenceRow struct {
	Command         string      `json:"command"`
	ExpectedExitCod int         `json:"expected_exit_code"`
	ExitCode        int         `json:"exit_code"`
	Stdout          artifactRef `json:"stdout"`
	Stderr          artifactRef `json:"stderr"`
	CapturedAtUTC   string      `json:"captured_at_utc"`
}

// reviewBlock is the review block of the v2 receipt.
type reviewBlock struct {
	ReviewerIdentity string `json:"reviewer_identity"`
	EvidenceRef      string `json:"evidence_ref"`
	CandidateSHA     string `json:"candidate_sha"`
	Verdict          string `json:"verdict"`
}

// validatorRunBlock is the docs-validator block of the v2 receipt.
type validatorRunBlock struct {
	ExitCode              int         `json:"exit_code"`
	DocsPackManifestSHA64 string      `json:"docs_pack_manifest_sha256"`
	Artefact              artifactRef `json:"artefact"`
}

// attemptDoc is an attempt-dir JSON doc; marshalling splices the running
// digest map into any doc carrying artefact_digests.
type attemptDoc struct {
	name string
	doc  any
}
