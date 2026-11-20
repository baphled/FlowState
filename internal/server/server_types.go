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
