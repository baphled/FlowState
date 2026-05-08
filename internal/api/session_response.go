package api

import (
	"encoding/json"
	"time"

	"github.com/baphled/flowstate/internal/session"
)

// SessionResponse is the canonical wire shape for session-returning endpoints.
//
// All keys are camelCase to match the SessionSummary contract consumed by the
// Vue frontend, and the body always includes messageCount so callers never
// read undefined.
//
// IsStreaming signals whether the backend broker has an active Publish in
// progress for this session. The Vue frontend uses this flag to reconnect
// an EventSource on page load without relying solely on the local message
// heuristic (last-message role check).
//
// ContextUsage carries the always-on context-window usage figure the
// chat UI's chip renders. Phase 3 of the May 2026 saturation fix
// includes this on agent / model PATCH responses so the chip ticks up
// to reflect the new (provider, model, message) state without waiting
// for the next pre-send streamed event. Embedded as a json.RawMessage
// because the wire shape is owned by the engine's contextUsagePayload
// (engine.go) and the api server forwards it verbatim. Omitted when no
// ContextUsageProvider is wired or the figure cannot be computed.
type SessionResponse struct {
	ID                string            `json:"id"`
	AgentID           string            `json:"agentId"`
	CurrentAgentID    string            `json:"currentAgentId,omitempty"`
	CurrentModelID    string            `json:"currentModelId,omitempty"`
	CurrentProviderID string            `json:"currentProviderId,omitempty"`
	Status            string            `json:"status"`
	ParentID          string            `json:"parentId,omitempty"`
	ParentSessionID   string            `json:"parentSessionId,omitempty"`
	Depth             int               `json:"depth"`
	Messages          []session.Message `json:"messages"`
	MessageCount      int               `json:"messageCount"`
	IsStreaming       bool              `json:"isStreaming"`
	ContextUsage      json.RawMessage   `json:"contextUsage,omitempty"`
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// sessionResponseOptions holds functional-option state for NewSessionResponse.
type sessionResponseOptions struct {
	isStreaming  bool
	contextUsage json.RawMessage
}

// SessionResponseOption is a functional option for NewSessionResponse.
type SessionResponseOption func(*sessionResponseOptions)

// WithIsStreaming annotates the response with the live-streaming flag from
// the session broker. Pass true when the broker reports IsPublishing for the
// session being projected.
func WithIsStreaming(streaming bool) SessionResponseOption {
	return func(o *sessionResponseOptions) {
		o.isStreaming = streaming
	}
}

// WithContextUsage annotates the response with the engine's current
// context_usage payload (Phase 3). Pass the JSON bytes verbatim from
// the engine — the wire shape is owned by the engine and the api
// server forwards it without re-parsing.
func WithContextUsage(payload []byte) SessionResponseOption {
	return func(o *sessionResponseOptions) {
		if len(payload) > 0 {
			o.contextUsage = json.RawMessage(payload)
		}
	}
}

// NewSessionResponse projects a session.Session into the wire-format DTO.
// Optional SessionResponseOption values can annotate the response with
// runtime state (e.g. broker streaming status) that is not part of the
// persisted session model.
func NewSessionResponse(sess *session.Session, opts ...SessionResponseOption) *SessionResponse {
	if sess == nil {
		return nil
	}
	o := &sessionResponseOptions{}
	for _, opt := range opts {
		opt(o)
	}
	messages := sess.Messages
	if messages == nil {
		messages = []session.Message{}
	}
	return &SessionResponse{
		ID:                sess.ID,
		AgentID:           sess.AgentID,
		CurrentAgentID:    sess.CurrentAgentID,
		CurrentModelID:    sess.CurrentModelID,
		CurrentProviderID: sess.CurrentProviderID,
		Status:            sess.Status,
		ParentID:          sess.ParentID,
		ParentSessionID:   sess.ParentSessionID,
		Depth:             sess.Depth,
		Messages:          messages,
		MessageCount:      len(messages),
		IsStreaming:       o.isStreaming,
		ContextUsage:      o.contextUsage,
		CreatedAt:         sess.CreatedAt,
		UpdatedAt:         sess.UpdatedAt,
	}
}
