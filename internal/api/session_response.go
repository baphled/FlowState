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
type SessionResponse struct {
	ID              string            `json:"id"`
	AgentID         string            `json:"agentId"`
	CurrentAgentID  string            `json:"currentAgentId,omitempty"`
	Status          string            `json:"status"`
	ParentID        string            `json:"parentId,omitempty"`
	ParentSessionID string            `json:"parentSessionId,omitempty"`
	Depth           int               `json:"depth"`
	Messages        []session.Message `json:"messages"`
	MessageCount    int               `json:"messageCount"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
}

// NewSessionResponse projects a session.Session into the wire-format DTO.
func NewSessionResponse(sess *session.Session) *SessionResponse {
	if sess == nil {
		return nil
	}
	messages := sess.Messages
	if messages == nil {
		messages = []session.Message{}
	}
	return &SessionResponse{
		ID:              sess.ID,
		AgentID:         sess.AgentID,
		CurrentAgentID:  sess.CurrentAgentID,
		Status:          sess.Status,
		ParentID:        sess.ParentID,
		ParentSessionID: sess.ParentSessionID,
		Depth:           sess.Depth,
		Messages:        messages,
		MessageCount:    len(messages),
		CreatedAt:       sess.CreatedAt,
		UpdatedAt:       sess.UpdatedAt,
	}
}
