package api

import (
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
	CreatedAt         time.Time         `json:"createdAt"`
	UpdatedAt         time.Time         `json:"updatedAt"`
}

// sessionResponseOptions holds functional-option state for NewSessionResponse.
type sessionResponseOptions struct {
	isStreaming bool
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
		CreatedAt:         sess.CreatedAt,
		UpdatedAt:         sess.UpdatedAt,
	}
}
