package server

import (
	"context"
	"encoding/json"
	"time"
)

// Ports is the narrow app surface serve may call; DTOs are server-owned.
type Ports struct {
	// ListSessions returns sessions, optionally filtered by status.
	ListSessions func(ctx context.Context, status string) ([]Session, error)
	// InspectSession returns one session and its ordered event log.
	InspectSession func(ctx context.Context, id string) (Session, []Event, error)
	// CloseSession closes a session and returns the closed row.
	CloseSession func(ctx context.Context, id string) (Session, error)
	// CancelSession cancels a session and returns the cancelled row.
	CancelSession func(ctx context.Context, id string) (Session, error)
	// DiagnoseSession returns one session's read-only diagnosis.
	DiagnoseSession func(ctx context.Context, id string) (Diagnosis, error)
	// BudgetOverview returns one session's budget overview.
	BudgetOverview func(ctx context.Context, id string) (Budget, error)
	// SetBudgetPool anchors the session's shared budget pool at a
	// positive total and returns the updated overview.
	SetBudgetPool func(ctx context.Context, id string, pool int64) (Budget, error)
	// SendSteeringMessage records one steering directive addressed to
	// the session and returns the stored message.
	SendSteeringMessage func(ctx context.Context, id, sender, body string) (Message, error)
	// ValidateSwarm validates one swarm manifest file without
	// executing it; validation failures surface the app error.
	ValidateSwarm func(ctx context.Context, manifestPath string) (SwarmValidation, error)
	// RunSwarm executes one configured swarm on behalf of a parent
	// session with the caller's budget total and returns the run
	// result; parent, manifest, budget and gate failures surface the
	// app error for the shared code table.
	RunSwarm func(ctx context.Context, parentID, manifestPath string, budget int64) (SwarmRun, error)
	// ValidateReceipt validates one receipt file and reports its
	// verdict; the verdict mirrors the CLI's per-file line, so an
	// invalid receipt is a verdict, not an error.
	ValidateReceipt func(ctx context.Context, path string) (ReceiptVerdict, error)
	// EventsAfter returns committed events with seq > afterSeq,
	// ordered by seq. SessionStatus returns the current status; the
	// SSE stream polls it for the terminal end states.
	EventsAfter   func(ctx context.Context, id string, afterSeq int64) ([]Event, error)
	SessionStatus func(ctx context.Context, id string) (string, error)
	// RunSession runs one bounded provider turn synchronously and
	// returns the projected event log; the run preamble (crash
	// recovery, resume, steering drain, budget) is owned by the
	// port, mirroring `session run <id>`. CreateSession creates one
	// session; empty Provider/Model defer to the serve config.
	RunSession    func(ctx context.Context, id string, opts RunOptions) ([]Event, error)
	CreateSession func(ctx context.Context, req CreateRequest) (Session, error)
	// ErrCode maps an error onto the closed contract code set.
	ErrCode func(err error) string
}

// Session is the session DTO; wire field names live in sessions.go.
type Session struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	Status    string
	Provider  string
	ModelID   string
	Harness   string
	ConfigSHA string
	ParentID  string
}

// Event is the event DTO; Payload is the stored canonical JSON object.
type Event struct {
	SessionID  string
	Seq        int64
	CreatedAt  time.Time
	Actor      string
	Type       string
	Payload    json.RawMessage
	PayloadSHA string
}

// Diagnosis is the doctor DTO mirroring the session-doctor wire shape
// (schemas/cli.json): Pool and Available are nil without a budget pool.
type Diagnosis struct {
	SessionID       string
	Status          string
	Crashed         bool
	RunActivity     bool
	PendingMessages int
	Reserved        int64
	Pool            *int64
	Available       *int64
}

// Message is the steering-message DTO mirroring the CLI message
// output (wire fields id/session_id/sender).
type Message struct {
	ID        string
	SessionID string
	Sender    string
}

// Budget is the budget-overview DTO mirroring the CLI budget output
// (wire fields session_id/reserved/budget_pool/available); Pool and
// Available are nil without a declared pool.
type Budget struct {
	SessionID string
	Reserved  int64
	Pool      *int64
	Available *int64
}

// SwarmValidation is the swarm-validate DTO mirroring the CLI swarm
// validate output (wire fields name/valid); Valid is true on every
// success body because a validation failure surfaces as an error
// through the shared code table instead.
type SwarmValidation struct {
	Name  string
	Valid bool
}

// SwarmChild is one delegated child row of a swarm run, mirroring
// the CLI swarm run child item (wire fields packet/session/status/
// parent_id).
type SwarmChild struct {
	Packet   string
	Session  string
	Status   string
	ParentID string
}

// SwarmRun is the swarm-run DTO mirroring the CLI swarm run output
// (wire fields manifest/admitted/accepted/children).
type SwarmRun struct {
	Manifest string
	Admitted []string
	Accepted bool
	Children []SwarmChild
}

// ReceiptVerdict is the receipt-validate DTO mirroring the CLI
// receipt validate per-file output (wire fields path/verdict; the
// verdict is "valid" or "invalid").
type ReceiptVerdict struct {
	Path    string
	Verdict string
}

// RunOptions carries the optional run request body onto the run
// port, mirroring the CLI --budget/--max-tokens flags; zero values
// mean unset exactly as omitted flags do.
type RunOptions struct {
	Budget    int64
	MaxTokens int64
}

// CreateRequest carries the POST /sessions body onto the create
// port; empty Provider/Model defer to the serve config defaults.
type CreateRequest struct {
	Provider string
	Model    string
	Task     string
}

// Server renders the read-only serve surface over Ports.
type Server struct {
	ports Ports
	token string
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// sessionItem mirrors the CLI session-list item field-for-field.
type sessionItem struct {
	ID           string `json:"id"`
	CreatedAtUTC string `json:"created_at_utc"`
	UpdatedAtUTC string `json:"updated_at_utc"`
	Status       string `json:"status"`
	Provider     string `json:"provider"`
	ModelID      string `json:"model_id"`
	Harness      string `json:"harness"`
	ConfigSHA    string `json:"config_sha256"`
	ParentID     string `json:"parent_id"`
}

// eventItem mirrors the CLI session-events item field-for-field.
type eventItem struct {
	SessionID    string          `json:"session_id"`
	Seq          int64           `json:"seq"`
	CreatedAtUTC string          `json:"created_at_utc"`
	Actor        string          `json:"actor"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload"`
	PayloadSHA   string          `json:"payload_sha256"`
}

type inspectBody struct {
	sessionItem
	Events []eventItem `json:"events"`
}

// budgetBody mirrors the CLI budget output field-for-field; a nil
// pool renders budget_pool/available as JSON null, as the CLI does.
type budgetBody struct {
	SessionID  string `json:"session_id"`
	Reserved   int64  `json:"reserved"`
	BudgetPool *int64 `json:"budget_pool"`
	Available  *int64 `json:"available"`
}

// messageBody mirrors the CLI message output field-for-field.
type messageBody struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id"`
	Sender    string `json:"sender"`
}

// budgetRequest is the POST /sessions/{id}/budget body; Pool is a
// pointer so a missing pool field is distinguishable from zero.
type budgetRequest struct {
	Pool *int64 `json:"pool"`
}

// messageRequest is the POST /sessions/{id}/message body.
type messageRequest struct {
	From string `json:"from"`
	Body string `json:"body"`
}

// pathRequest is the POST body for the path-based endpoints
// (/swarm/validate, /receipt/validate), mirroring the CLI surface
// that names one operator-local file.
type pathRequest struct {
	Path string `json:"path"`
}

// swarmRunRequest is the POST /swarm/run body; Budget is a pointer
// so a missing budget is distinguishable from zero.
type swarmRunRequest struct {
	Parent string `json:"parent"`
	Path   string `json:"path"`
	Budget *int64 `json:"budget"`
}

// swarmValidationBody mirrors the CLI swarm validate output
// field-for-field (schemas/cli.json key swarm-validate).
type swarmValidationBody struct {
	Name  string `json:"name"`
	Valid bool   `json:"valid"`
}

// swarmChildBody mirrors the CLI swarm run child item
// field-for-field.
type swarmChildBody struct {
	Packet   string `json:"packet"`
	Session  string `json:"session"`
	Status   string `json:"status"`
	ParentID string `json:"parent_id"`
}

// swarmRunBody mirrors the CLI swarm run output field-for-field
// (schemas/cli.json key swarm-run).
type swarmRunBody struct {
	Manifest string           `json:"manifest"`
	Admitted []string         `json:"admitted"`
	Accepted bool             `json:"accepted"`
	Children []swarmChildBody `json:"children"`
}

// receiptVerdictBody mirrors the CLI receipt validate per-file
// output field-for-field (schemas/cli.json key receipt-validate).
type receiptVerdictBody struct {
	Path    string `json:"path"`
	Verdict string `json:"verdict"`
}

// runRequest is the optional POST /sessions/{id}/run body mirroring
// the CLI run flags; a missing field decodes as the unset zero value.
type runRequest struct {
	Budget    int64 `json:"budget"`
	MaxTokens int64 `json:"max_tokens"`
}

// createBody is the POST /sessions request body; empty provider and
// model fall back to the serve config defaults inside the port.
type createBody struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Task     string `json:"task"`
}

// sessionNewBody mirrors the CLI session-new wire shape
// (schemas/cli.json key session-new, shared with `session new`).
type sessionNewBody struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	ParentID string `json:"parent_id"`
}

// streamStatusBody is the terminal status frame that closes an SSE
// follow stream after the last committed event frame.
type streamStatusBody struct {
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// diagnosisBody mirrors the session-doctor item field-for-field; a nil
// pool renders budget_pool/available as JSON null, as the CLI does.
type diagnosisBody struct {
	SessionID       string `json:"session_id"`
	Status          string `json:"status"`
	Crashed         bool   `json:"crashed"`
	RunActivity     bool   `json:"run_activity"`
	PendingMessages int    `json:"pending_messages"`
	Reserved        int64  `json:"reserved"`
	BudgetPool      *int64 `json:"budget_pool"`
	Available       *int64 `json:"available"`
}
