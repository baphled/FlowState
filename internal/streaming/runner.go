package streaming

import (
	"context"

	"github.com/baphled/flowstate/internal/provider"
)

// Run drives a Streamer into a StreamConsumer, coordinating the streaming lifecycle.
//
// Expected:
//   - ctx is a valid context for the streaming operation.
//   - s is a non-nil Streamer implementation.
//   - c is a non-nil StreamConsumer implementation.
//   - agentID identifies the agent to stream from.
//   - message is the user's input text.
//
// Returns:
//   - nil on success.
//   - The Stream error if the initial stream call fails.
//   - The first WriteChunk error if content delivery fails.
//
// Side effects:
//   - Calls c.WriteError for stream-level and chunk-level errors.
//   - Calls c.Done after the stream completes regardless of errors.
func Run(ctx context.Context, s Streamer, c StreamConsumer, agentID, message string) error {
	defer c.Done()

	ch, err := s.Stream(ctx, agentID, message)
	if err != nil {
		c.WriteError(err)
		return err
	}

	var writeErr error
	for chunk := range ch {
		if chunk.Error != nil {
			c.WriteError(chunk.Error)
			continue
		}
		if dispatchHarnessEvent(c, chunk) {
			continue
		}
		if deliverDelegationEvent(c, chunk.DelegationInfo) {
			continue
		}
		deliverToolCall(c, chunk.ToolCall)
		deliverToolResult(c, chunk.ToolResult)
		if chunk.Content != "" && writeErr == nil {
			writeErr = c.WriteChunk(chunk.Content)
		}
		if chunk.Done {
			break
		}
	}

	return writeErr
}

// deliverToolCall extracts the tool call name and delivers it to the consumer.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - toolCall may be nil.
//
// Side effects:
//   - If toolCall is not nil and c implements ToolCallConsumer, calls c.WriteToolCall.
//   - Extracts the skill name from skill_load tool calls and prefixes with "skill:".
func deliverToolCall(c StreamConsumer, toolCall *provider.ToolCall) {
	if toolCall == nil {
		return
	}
	tc, ok := c.(ToolCallConsumer)
	if !ok {
		return
	}

	name := toolCall.Name
	if name == "skill_load" {
		if skillName, ok := toolCall.Arguments["name"].(string); ok && skillName != "" {
			name = "skill:" + skillName
		}
	}
	tc.WriteToolCall(name)
}

// dispatchHarnessEvent checks whether the chunk is a harness lifecycle event or typed plan event
// and delivers it to the consumer if supported. Returns true if the chunk was handled.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - chunk is the current stream chunk to inspect.
//
// Returns:
//   - true if the chunk carried a harness or typed plan event type (regardless of consumer support).
//   - false if the chunk is not a recognised event type.
//
// Side effects:
//   - If c implements HarnessEventConsumer, calls the corresponding method for harness event types.
//   - If c implements EventConsumer, calls WriteEvent for plan_artifact, review_verdict, and
//     status_transition event types.
func dispatchHarnessEvent(c StreamConsumer, chunk provider.StreamChunk) bool {
	var harnessFunc func(HarnessEventConsumer)
	switch chunk.EventType {
	case "harness_retry":
		harnessFunc = func(h HarnessEventConsumer) { h.WriteHarnessRetry(chunk.Content) }
	case "harness_attempt_start":
		harnessFunc = func(h HarnessEventConsumer) { h.WriteAttemptStart(chunk.Content) }
	case "harness_complete":
		harnessFunc = func(h HarnessEventConsumer) { h.WriteComplete(chunk.Content) }
	case "harness_critic_feedback":
		harnessFunc = func(h HarnessEventConsumer) { h.WriteCriticFeedback(chunk.Content) }
	case "plan_artifact":
		deliverTypedEvent(c, PlanArtifactEvent{Content: chunk.Content})
		return true
	case "review_verdict":
		deliverTypedEvent(c, ReviewVerdictEvent{})
		return true
	case "status_transition":
		deliverTypedEvent(c, StatusTransitionEvent{})
		return true
	default:
		return false
	}
	if hc, ok := c.(HarnessEventConsumer); ok {
		harnessFunc(hc)
	}
	return true
}

// deliverTypedEvent delivers a typed Event to the consumer if it implements EventConsumer.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - event is a non-nil Event implementation.
//
// Side effects:
//   - If c implements EventConsumer, calls WriteEvent with the event.
//   - Errors from WriteEvent are forwarded to c.WriteError.
func deliverTypedEvent(c StreamConsumer, event Event) {
	ec, ok := c.(EventConsumer)
	if !ok {
		return
	}
	if err := ec.WriteEvent(event); err != nil {
		c.WriteError(err)
	}
}

// deliverToolResult delivers the tool result to the consumer.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - result may be nil.
//
// Side effects:
//   - If result is not nil and c implements ToolResultConsumer, calls c.WriteToolResult.
func deliverToolResult(c StreamConsumer, result *provider.ToolResultInfo) {
	if result == nil {
		return
	}
	trc, ok := c.(ToolResultConsumer)
	if !ok {
		return
	}
	trc.WriteToolResult(result.Content)
}

// deliverDelegationEvent converts a DelegationInfo into a DelegationEvent and delivers
// it to the consumer if supported. Returns true when the chunk carried delegation info
// (regardless of consumer support) so the caller can skip normal content delivery.
//
// Expected:
//   - c is a non-nil StreamConsumer.
//   - info may be nil.
//
// Returns:
//   - true if info was non-nil (chunk consumed as delegation event).
//   - false if info was nil (chunk should continue normal processing).
//
// Side effects:
//   - If info is non-nil and c implements DelegationConsumer, calls c.WriteDelegation.
func deliverDelegationEvent(c StreamConsumer, info *provider.DelegationInfo) bool {
	if info == nil {
		return false
	}
	dc, ok := c.(DelegationConsumer)
	if !ok {
		return true
	}
	if err := dc.WriteDelegation(DelegationEvent{
		SourceAgent:  info.SourceAgent,
		TargetAgent:  info.TargetAgent,
		ChainID:      info.ChainID,
		Status:       info.Status,
		ModelName:    info.ModelName,
		ProviderName: info.ProviderName,
		Description:  info.Description,
		ToolCalls:    info.ToolCalls,
		LastTool:     info.LastTool,
		StartedAt:    info.StartedAt,
		CompletedAt:  info.CompletedAt,
	}); err != nil {
		c.WriteError(err)
	}
	return true
}
