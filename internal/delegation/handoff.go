package delegation

import "errors"

// Handoff carries typed metadata for delegation chains.
type Handoff struct {
	// SourceAgent identifies the agent that initiated the handoff.
	SourceAgent string `json:"source_agent"`
	// TargetAgent identifies the agent that should receive the handoff.
	TargetAgent string `json:"target_agent"`
	// TaskType describes the delegation category for routing and handling.
	TaskType string `json:"task_type"`
	// ChainID names the coordination store namespace for the delegation chain.
	ChainID string `json:"chain_id"`
	// Message carries the instruction or request passed to the target agent.
	Message string `json:"message"`
	// Feedback carries any response or review data returned to the caller.
	Feedback string `json:"feedback"`
	// Metadata carries arbitrary key-value attributes for delegation context.
	Metadata map[string]string `json:"metadata"`
}

// NewHandoff creates a new handoff with the supplied delegation metadata.
func NewHandoff(h Handoff) *Handoff {
	return &Handoff{
		SourceAgent: h.SourceAgent,
		TargetAgent: h.TargetAgent,
		TaskType:    h.TaskType,
		ChainID:     h.ChainID,
		Message:     h.Message,
		Feedback:    h.Feedback,
		Metadata:    h.Metadata,
	}
}

// Validate checks the required delegation metadata fields.
func (h Handoff) Validate() error {
	if h.SourceAgent == "" {
		return errors.New("source agent must not be empty")
	}

	if h.TargetAgent == "" {
		return errors.New("target agent must not be empty")
	}

	if h.ChainID == "" {
		return errors.New("chain id must not be empty")
	}

	return nil
}
