package session

import (
	"fmt"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
	tooldisplay "github.com/baphled/flowstate/internal/tool/display"
)

// MessageAppender is a narrow write-back interface for appending session messages.
//
// Expected:
//   - sessionID identifies the target session.
//   - msg is the message to append.
//
// Side effects:
//   - Implementations are expected to persist msg to the session identified by sessionID.
type MessageAppender interface {
	// AppendMessage persists msg to the session identified by sessionID.
	AppendMessage(sessionID string, msg Message)
}

// AppendMessage appends a message to the session identified by sessionID.
//
// Expected:
//   - sessionID identifies an existing session in the Manager.
//   - msg contains the message to append (ID and Timestamp are assigned internally).
//
// Returns:
//   - None.
//
// Side effects:
//   - Acquires the Manager lock and appends msg to the session's Messages slice.
//   - Does nothing when sessionID is not found.
func (m *Manager) AppendMessage(sessionID string, msg Message) {
	m.appendSessionMessage(sessionID, msg)
}

// streamAccumState holds mutable accumulation state within AccumulateStream.
type streamAccumState struct {
	sessionID     string
	agentID       string
	contentBuf    strings.Builder
	thinkingBuf   strings.Builder
	lastToolName  string
	lastToolInput string
}

// AccumulateStream wraps rawCh with a goroutine that records assistant and tool
// messages into the appender while forwarding every chunk to the returned channel.
//
// Expected:
//   - appender is a valid MessageAppender used to persist accumulated messages.
//   - sessionID and agentID are valid identifiers for the active session.
//   - rawCh is the stream channel containing provider chunks.
//
// Returns:
//   - A new channel that receives the same chunks as rawCh.
//
// Side effects:
//   - Spawns a goroutine that appends messages via appender.AppendMessage.
//   - The returned channel is closed once rawCh is fully consumed.
func AccumulateStream(
	appender MessageAppender,
	sessionID, agentID string,
	rawCh <-chan provider.StreamChunk,
) <-chan provider.StreamChunk {
	accumCh := make(chan provider.StreamChunk, 64)
	go func() {
		defer close(accumCh)
		s := &streamAccumState{sessionID: sessionID, agentID: agentID}
		for chunk := range rawCh {
			applyChunk(appender, s, chunk)
			accumCh <- chunk
		}
	}()
	return accumCh
}

// applyChunk processes one stream chunk, updating accumulation state and persisting messages.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state.
//   - chunk is the next chunk from the raw stream.
//
// Returns:
//   - None.
//
// Side effects:
//   - May call appender.AppendMessage to persist accumulated content.
//   - Mutates s.contentBuf, s.thinkingBuf, s.lastToolName, and s.lastToolInput.
func applyChunk(appender MessageAppender, s *streamAccumState, chunk provider.StreamChunk) {
	switch {
	case chunk.ToolCall != nil:
		applyToolCall(appender, s, chunk.ToolCall)
	case chunk.ToolResult != nil:
		applyToolResult(appender, s, chunk.ToolResult)
	case chunk.DelegationInfo != nil:
		applyDelegation(appender, s, chunk.DelegationInfo)
	case chunk.Done:
		flushThinking(appender, s)
		flushContent(appender, s)
	default:
		if chunk.Thinking != "" {
			s.thinkingBuf.WriteString(chunk.Thinking)
		}
		if chunk.Content != "" {
			s.contentBuf.WriteString(chunk.Content)
		}
	}
}

// applyToolCall flushes pending content, then stores a tool_call message.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state.
//   - tc is the non-nil tool call chunk to record.
//
// Side effects:
//   - Calls flushThinking and flushContent before appending the tool_call message.
//   - Updates s.lastToolName and s.lastToolInput for later use by applyToolResult.
func applyToolCall(appender MessageAppender, s *streamAccumState, tc *provider.ToolCall) {
	flushThinking(appender, s)
	flushContent(appender, s)
	input := toolArgValue(tc.Name, tc.Arguments)
	appender.AppendMessage(s.sessionID, Message{
		Role:      "tool_call",
		Content:   tc.Name,
		ToolName:  tc.Name,
		ToolInput: input,
		AgentID:   s.agentID,
	})
	s.lastToolName = tc.Name
	s.lastToolInput = input
}

// applyToolResult stores a tool_result or tool_error message.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state, including the preceding tool name and input.
//   - tr is the non-nil tool result chunk to record.
//
// Side effects:
//   - Appends a tool_result message (or tool_error on tr.IsError) via appender.AppendMessage.
func applyToolResult(appender MessageAppender, s *streamAccumState, tr *provider.ToolResultInfo) {
	role := "tool_result"
	if tr.IsError {
		role = "tool_error"
	}
	appender.AppendMessage(s.sessionID, Message{
		Role:      role,
		Content:   tr.Content,
		ToolName:  s.lastToolName,
		ToolInput: s.lastToolInput,
		AgentID:   s.agentID,
	})
}

// applyDelegation stores a delegation message when status is completed or failed.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state.
//   - info is the non-nil delegation info chunk to evaluate.
//
// Side effects:
//   - Appends a delegation message via appender.AppendMessage when info.Status is "completed" or "failed".
//   - Does nothing for any other status value.
func applyDelegation(appender MessageAppender, s *streamAccumState, info *provider.DelegationInfo) {
	if info.Status != "completed" && info.Status != "failed" {
		return
	}
	appender.AppendMessage(s.sessionID, Message{
		Role:    "delegation",
		Content: formatDelegationSummary(info),
		AgentID: s.agentID,
	})
}

// flushThinking writes accumulated thinking content as a thinking message and resets the buffer.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state with a possibly non-empty thinkingBuf.
//
// Side effects:
//   - Appends a thinking message via appender.AppendMessage and resets s.thinkingBuf.
//   - Does nothing when s.thinkingBuf is empty.
func flushThinking(appender MessageAppender, s *streamAccumState) {
	if s.thinkingBuf.Len() == 0 {
		return
	}
	appender.AppendMessage(s.sessionID, Message{
		Role:    "thinking",
		Content: s.thinkingBuf.String(),
		AgentID: s.agentID,
	})
	s.thinkingBuf.Reset()
}

// flushContent writes accumulated assistant content as an assistant message and resets the buffer.
//
// Expected:
//   - appender is the message sink.
//   - s holds the current accumulation state with a possibly non-empty contentBuf.
//
// Side effects:
//   - Appends an assistant message via appender.AppendMessage and resets s.contentBuf.
//   - Does nothing when s.contentBuf is empty.
func flushContent(appender MessageAppender, s *streamAccumState) {
	if s.contentBuf.Len() == 0 {
		return
	}
	appender.AppendMessage(s.sessionID, Message{
		Role:    "assistant",
		Content: s.contentBuf.String(),
		AgentID: s.agentID,
	})
	s.contentBuf.Reset()
}

// formatDelegationSummary builds a human-readable summary of a delegation event.
//
// Expected:
//   - info is the non-nil DelegationInfo to summarise.
//
// Returns:
//   - A newline-joined string containing the target agent, status, optional model name, and tool call count.
//
// Side effects:
//   - None.
func formatDelegationSummary(info *provider.DelegationInfo) string {
	parts := []string{fmt.Sprintf("│ %s [%s]", info.TargetAgent, info.Status)}
	if info.ModelName != "" {
		parts = append(parts, "  Model: "+info.ModelName)
	}
	if info.ToolCalls > 0 {
		toolInfo := fmt.Sprintf("  %d tool calls", info.ToolCalls)
		if info.LastTool != "" {
			toolInfo += fmt.Sprintf(" (last: %s)", info.LastTool)
		}
		parts = append(parts, toolInfo)
	}
	return strings.Join(parts, "\n")
}

// toolArgValue returns the primary display argument value for the given tool call.
//
// Expected:
//   - name is a tool identifier.
//   - args contains the tool call argument map.
//
// Returns:
//   - The string value of the primary argument, or an empty string when absent.
//
// Side effects:
//   - None.
func toolArgValue(name string, args map[string]any) string {
	key := tooldisplay.PrimaryArgKey(name)
	if key == "" {
		return ""
	}
	v, ok := args[key].(string)
	if !ok {
		return ""
	}
	return v
}
