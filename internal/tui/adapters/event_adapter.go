package adapters

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
)

// EventToMsg converts a streaming.Event into a suitable tea.Msg for the chat intent.
//
// Expected:
//   - event is one of the supported streaming event types.
//
// Returns:
//   - A chat.StreamChunkMsg for recognised events, or nil for unsupported events.
//
// Side effects:
//   - None.
func EventToMsg(event streaming.Event) tea.Msg {
	switch e := event.(type) {
	case streaming.DelegationEvent:
		return chat.StreamChunkMsg{
			EventType: streaming.EventTypeDelegation,
			DelegationInfo: &provider.DelegationInfo{
				SourceAgent:  e.SourceAgent,
				TargetAgent:  e.TargetAgent,
				ChainID:      e.ChainID,
				Status:       e.Status,
				ModelName:    e.ModelName,
				ProviderName: e.ProviderName,
				Description:  e.Description,
				ToolCalls:    e.ToolCalls,
				LastTool:     e.LastTool,
				StartedAt:    e.StartedAt,
				CompletedAt:  e.CompletedAt,
			},
		}

	case streaming.StatusTransitionEvent:
		return chat.StreamChunkMsg{
			EventType: streaming.EventTypeStatusTransition,
			DelegationInfo: &provider.DelegationInfo{
				SourceAgent: e.AgentID,
				TargetAgent: e.AgentID,
				Status:      e.To,
				Description: fmt.Sprintf("Transitioning from %s to %s", e.From, e.To),
			},
		}

	case streaming.ReviewVerdictEvent:
		desc := fmt.Sprintf("Verdict: %s (Confidence: %.2f)", e.Verdict, e.Confidence)
		if len(e.Issues) > 0 {
			desc += " - Issues: " + strings.Join(e.Issues, ", ")
		}
		return chat.StreamChunkMsg{
			EventType: streaming.EventTypeReviewVerdict,
			DelegationInfo: &provider.DelegationInfo{
				SourceAgent: e.AgentID,
				TargetAgent: e.AgentID,
				Status:      "reviewing",
				Description: desc,
			},
		}

	default:
		return nil
	}
}
