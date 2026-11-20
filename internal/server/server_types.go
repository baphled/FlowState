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
