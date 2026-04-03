package session

import (
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
//   - Mutates s.contentBuf, s.lastToolName, and s.lastToolInput.
func applyChunk(appender MessageAppender, s *streamAccumState, chunk provider.StreamChunk) {
	switch {
	case chunk.ToolCall != nil:
		if s.contentBuf.Len() > 0 {
			appender.AppendMessage(s.sessionID, Message{
				Role:    "assistant",
				Content: s.contentBuf.String(),
				AgentID: s.agentID,
			})
			s.contentBuf.Reset()
		}
		s.lastToolName = chunk.ToolCall.Name
		s.lastToolInput = toolArgValue(chunk.ToolCall.Name, chunk.ToolCall.Arguments)
	case chunk.ToolResult != nil:
		appender.AppendMessage(s.sessionID, Message{
			Role:      "tool_result",
			Content:   chunk.ToolResult.Content,
			ToolName:  s.lastToolName,
			ToolInput: s.lastToolInput,
			AgentID:   s.agentID,
		})
	case chunk.Done:
		if s.contentBuf.Len() > 0 {
			appender.AppendMessage(s.sessionID, Message{
				Role:    "assistant",
				Content: s.contentBuf.String(),
				AgentID: s.agentID,
			})
			s.contentBuf.Reset()
		}
	default:
		if chunk.Content != "" {
			s.contentBuf.WriteString(chunk.Content)
		}
	}
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
