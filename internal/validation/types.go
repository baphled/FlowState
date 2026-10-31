// Package validation implements the fail-closed M1 acceptance validation
// obligations (VA-*) declared in governance/VALIDATOR-ASSERTIONS.md. It is a
// pure domain package: it depends only on the standard library so the owning
// layer contract can keep it free of adapter imports such as internal/store.
package validation

// Receipt mirrors gates/m1-acceptance-receipt.schema.json. The Dropbox schema
// stays canonical; this value set is the runtime projection under test.
type Receipt struct {
	SchemaVersion  string
	CapabilityID   string
	RepoState      RepoState
	AuthorIdentity string
	Evidence       []EvidenceItem
	GateOutcome    string
	ObservedModel  ObservedModel
	Review         Review
	ValidatorRun   ValidatorRun
	MetadataRef    string
	ArtefactRoot   string
}

// RepoState captures the exact repository state the evidence was produced on.
type RepoState struct {
	CommitSHA      string
	Dirty          bool
	ManifestSHA256 string
	JobID          string
}

// EvidenceItem is one executed command with retained stdout/stderr artefacts.
type EvidenceItem struct {
	Command          string
	ExpectedExitCode int
	ExitCode         int
	Stdout           ArtefactRef
	Stderr           ArtefactRef
	CapturedAtUTC    string
}

// ArtefactRef references a retained artefact by path and digest.
type ArtefactRef struct {
	Path   string
	SHA256 string
}

// ObservedModel records the model attribution observed for the run.
type ObservedModel struct {
	Provider string
	ModelID  string
	Harness  string
	Source   string
}

// Review records the independently identifiable reviewer decision.
type Review struct {
	ReviewerIdentity string
	EvidenceRef      string
	CandidateSHA     string
	Verdict          string
}

// ValidatorRun records the self-test run of this validator.
type ValidatorRun struct {
	ExitCode               int
	DocsPackManifestSHA256 string
	Artefact               ArtefactRef
}

// WorkPacket is the VA-D0 value set: the proposed unit of work under review.
type WorkPacket struct {
	SchemaVersion string
	Sources       []string
	BaseSHA       string
	Tools         []string
	Scope         []string
	BudgetTokens  int
	Admitted      bool
}

// Candidate is the VA-D1/VA-D4 code candidate the evidence was produced on.
type Candidate struct {
	CommitSHA      string
	Dirty          bool
	ManifestSHA256 string
	ManifestBytes  []byte
	ChangedPaths   []string
	CapabilityID   string
}

// ReviewIdentity is the VA-D3 value set for the reviewer gate (GF-R05).
type ReviewIdentity struct {
	ReviewerIdentity string
	AuthorIdentity   string
	CandidateSHA     string
	EvidenceReviewed bool
	ReviewerResolves bool
	Verdict          string
}

// ArtefactVerification pairs a claimed digest with the observed digest.
type ArtefactVerification struct {
	Path          string
	ClaimedSHA256 string
	ActualSHA256  string
	Present       bool
}

// PersistedEvent is the stored event row projected onto stdout (VA-E2).
type PersistedEvent struct {
	SessionID     string
	Seq           int64
	CreatedAtUTC  string
	Actor         string
	Type          string
	PayloadSHA256 string
	Payload       map[string]any
}

// ExitCode identifies a stable CLI exit code from the CLI contract (VA-C1).
type ExitCode int

// ExitExpectation is one row of the VA-C1 command/error/exit matrix.
type ExitExpectation struct {
	Command string
	Error   string
	Exit    ExitCode
}
