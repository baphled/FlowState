package api

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/coder/websocket"
)

// wsIncomingMsg represents a message received from a WebSocket client.
type wsIncomingMsg struct {
	Content string `json:"content"`
}

// BuildWSChunkMsg converts a provider.StreamChunk to a WSChunkMsg.
//
// Expected:
//   - chunk is a valid StreamChunk.
//
// Returns:
//   - A WSChunkMsg with fields populated from the chunk.
//
// Side effects:
//   - None.
func BuildWSChunkMsg(chunk provider.StreamChunk) WSChunkMsg {
	msg := WSChunkMsg{
		Content:   chunk.Content,
		Done:      chunk.Done,
		EventType: chunk.EventType,
	}
	if chunk.Error != nil {
		msg.Error = chunk.Error.Error()
	}
	if chunk.DelegationInfo != nil {
		msg.Delegation = chunk.DelegationInfo
	}
	if chunk.ToolCall != nil {
		msg.ToolCall = chunk.ToolCall
	}
	if chunk.Event != nil {
		if progressEvent, ok := chunk.Event.(streaming.ProgressEvent); ok {
			msg.Progress = &progressEvent
		}
		if _, ok := chunk.Event.(streaming.Event); ok {
			msg.EventData = chunk.Event
		}
	}
	return msg
}

// WSChunkMsg represents a response chunk sent to a WebSocket client.
type WSChunkMsg struct {
	Content    string                   `json:"content,omitempty"`
	Done       bool                     `json:"done,omitempty"`
	Error      string                   `json:"error,omitempty"`
	Delegation *provider.DelegationInfo `json:"delegation,omitempty"`
	ToolCall   *provider.ToolCall       `json:"tool_call,omitempty"`
	Progress   *streaming.ProgressEvent `json:"progress,omitempty"`
	EventType  string                   `json:"event_type,omitempty"`
	EventData  interface{}              `json:"event_data,omitempty"`
}

// handleSessionWebSocket upgrades the connection to WebSocket, validates the session,
// then forwards incoming messages to the session engine and streams engine responses back.
//
// Expected:
//   - Request path parameter "id" identifies an existing session.
//   - The request can be upgraded to a WebSocket connection.
//
// Side effects:
//   - Reads JSON messages from the client and forwards them to the session engine.
//   - Writes engine response chunks as JSON to the client.
//   - Subscribes to EventBus events for the session and forwards them to the client.
//   - Closes the connection when the engine stream is complete or an error occurs.
func (s *Server) handleSessionWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.sessionManager == nil {
		http.Error(w, errSessionManagerNotConfigured, http.StatusNotImplemented)
		return
	}
	id := r.PathValue("id")
	if _, err := s.sessionManager.GetSession(id); err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"localhost:*"},
	})
	if err != nil {
		return
	}

	out := make(chan WSChunkMsg, 128)
	writeDone := make(chan struct{})
	ctx := r.Context()

	go func() {
		writeWSLoop(ctx, conn, out)
		close(writeDone)
	}()

	stopBus := s.subscribeSessionBus(id, out)

	for {
		incoming, ok := readWSMessage(ctx, conn)
		if !ok {
			break
		}
		if !s.serveWSSession(ctx, out, id, incoming) {
			break
		}
	}

	close(out)
	stopBus()
	<-writeDone
	closeWebSocket(conn)
}

// readWSMessage reads and decodes the next message from the WebSocket connection.
//
// Expected:
//   - conn is an open WebSocket connection.
//
// Returns:
//   - The decoded message and true on success.
//   - An empty message and false when the connection closes or the message is empty.
//
// Side effects:
//   - Blocks until a message is available.
func readWSMessage(ctx context.Context, conn *websocket.Conn) (wsIncomingMsg, bool) {
	_, raw, err := conn.Read(ctx)
	if err != nil {
		return wsIncomingMsg{}, false
	}
	var msg wsIncomingMsg
	if jsonErr := json.Unmarshal(raw, &msg); jsonErr != nil || msg.Content == "" {
		return wsIncomingMsg{}, true
	}
	return msg, true
}

// writeWSLoop reads messages from the out channel and writes each to the WebSocket connection.
// This ensures all writes are serialised through a single goroutine, preventing concurrent writes.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - conn is an open WebSocket connection.
//   - out is a readable channel of WSChunkMsg values.
//
// Side effects:
//   - Writes JSON-encoded messages to conn until out is closed or ctx is cancelled.
func writeWSLoop(ctx context.Context, conn *websocket.Conn, out <-chan WSChunkMsg) {
	for msg := range out {
		if err := sendWSMsg(ctx, conn, msg); err != nil {
			return
		}
	}
}

// serveWSSession forwards an incoming message to the session engine and streams the response.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - out is a channel for sending response chunks to the WebSocket writer goroutine.
//   - sessionID identifies an existing session.
//   - msg contains the content to send to the engine.
//
// Returns:
//   - true to continue the read loop, false to close the connection.
//
// Side effects:
//   - Calls sessionManager.SendMessage and forwards response chunks to out.
func (s *Server) serveWSSession(ctx context.Context, out chan<- WSChunkMsg, sessionID string, msg wsIncomingMsg) bool {
	if msg.Content == "" {
		return true
	}
	chunks, err := s.sessionManager.SendMessage(ctx, sessionID, msg.Content)
	if err != nil {
		return false
	}
	return s.forwardWSChunks(ctx, out, chunks)
}

// forwardWSChunks reads from a chunk channel and sends each chunk through the out channel.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - out is a channel for sending WSChunkMsg values to the writer goroutine.
//   - chunks is a readable channel of provider.StreamChunk values.
//
// Returns:
//   - true to continue the read loop, false when streaming is complete or an error occurs.
//
// Side effects:
//   - Sends WSChunkMsg values to out.
func (s *Server) forwardWSChunks(ctx context.Context, out chan<- WSChunkMsg, chunks <-chan provider.StreamChunk) bool {
	for chunk := range chunks {
		msg := BuildWSChunkMsg(chunk)
		select {
		case out <- msg:
		case <-ctx.Done():
			return false
		}
		if chunk.Done || chunk.Error != nil {
			return false
		}
	}
	return true
}

// sendWSMsg encodes msg as JSON and writes it to the WebSocket connection.
//
// Expected:
//   - conn is an open WebSocket connection.
//   - msg is JSON-serialisable.
//
// Returns:
//   - An error if marshalling or the write fails.
//
// Side effects:
//   - Writes a JSON text frame to the WebSocket connection.
func sendWSMsg(ctx context.Context, conn *websocket.Conn, msg WSChunkMsg) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, data)
}

// closeWebSocket closes the WebSocket connection if possible.
//
// Expected:
//   - The connection can be closed without panicking.
//
// Side effects:
//   - Closes the underlying WebSocket connection.
func closeWebSocket(conn *websocket.Conn) {
	if err := conn.CloseNow(); err != nil {
		return
	}
}

// WSConsumer implements streaming.DelegationConsumer, streaming.DelegationProgressConsumer,
// streaming.EventConsumer, and streaming.HarnessEventConsumer by forwarding events as
// JSON over a WebSocket connection.
type WSConsumer struct {
	conn *websocket.Conn
	ctx  context.Context
}

// NewWSConsumer creates a WSConsumer for sending events over a WebSocket connection.
//
// Expected:
//   - ctx is a valid context for the WebSocket operations.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - A configured WSConsumer.
//
// Side effects:
//   - None.
func NewWSConsumer(ctx context.Context, conn *websocket.Conn) *WSConsumer {
	return &WSConsumer{conn: conn, ctx: ctx}
}

// WriteDelegation sends a DelegationEvent as a WebSocket message to the client.
//
// Expected:
//   - ev is a valid DelegationEvent.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteDelegation(ev streaming.DelegationEvent) error {
	msg := c.WriteDelegationToMsg(ev)
	return sendWSMsg(c.ctx, c.conn, msg)
}

// WriteDelegationToMsg converts a DelegationEvent to a WSChunkMsg for testing.
//
// Expected:
//   - ev is a valid DelegationEvent.
//
// Returns:
//   - A WSChunkMsg with the delegation field populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteDelegationToMsg(ev streaming.DelegationEvent) WSChunkMsg {
	return WSChunkMsg{
		Delegation: &provider.DelegationInfo{
			SourceAgent:  ev.SourceAgent,
			TargetAgent:  ev.TargetAgent,
			ChainID:      ev.ChainID,
			Status:       ev.Status,
			ModelName:    ev.ModelName,
			ProviderName: ev.ProviderName,
			Description:  ev.Description,
			ToolCalls:    ev.ToolCalls,
			LastTool:     ev.LastTool,
			StartedAt:    ev.StartedAt,
			CompletedAt:  ev.CompletedAt,
		},
	}
}

// WriteProgress sends a ProgressEvent as a WebSocket message to the client.
//
// Expected:
//   - ev is a valid ProgressEvent.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteProgress(ev streaming.ProgressEvent) error {
	msg := c.WriteProgressToMsg(ev)
	return sendWSMsg(c.ctx, c.conn, msg)
}

// WriteProgressToMsg converts a ProgressEvent to a WSChunkMsg for testing.
//
// Expected:
//   - ev is a valid ProgressEvent.
//
// Returns:
//   - A WSChunkMsg with the progress field populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteProgressToMsg(ev streaming.ProgressEvent) WSChunkMsg {
	return WSChunkMsg{
		Progress: &ev,
	}
}

// WriteEvent sends a typed streaming Event as a WebSocket message to the client.
//
// Expected:
//   - event is a non-nil Event implementation.
//   - conn is an open WebSocket connection.
//
// Returns:
//   - An error if the write fails.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteEvent(event streaming.Event) error {
	msg := c.WriteEventToMsg(event)
	return sendWSMsg(c.ctx, c.conn, msg)
}

// WriteEventToMsg converts a typed streaming Event to a WSChunkMsg for testing.
//
// Expected:
//   - event is a non-nil Event implementation.
//
// Returns:
//   - A WSChunkMsg with event_type and event_data fields populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteEventToMsg(event streaming.Event) WSChunkMsg {
	return WSChunkMsg{
		EventType: event.Type(),
		EventData: event,
	}
}

// WriteHarnessRetry sends a harness retry event as a WebSocket message to the client.
//
// Expected:
//   - content describes the validation failure and retry reason.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteHarnessRetry(content string) {
	msg := c.WriteHarnessRetryToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteHarnessRetryToMsg converts harness retry content to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the validation failure and retry reason.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_retry and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteHarnessRetryToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_retry",
		Content:   content,
	}
}

// WriteAttemptStart sends a harness attempt start event as a WebSocket message to the client.
//
// Expected:
//   - content describes the attempt being started.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteAttemptStart(content string) {
	msg := c.WriteAttemptStartToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteAttemptStartToMsg converts harness attempt start content to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the attempt being started.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_attempt_start and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteAttemptStartToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_attempt_start",
		Content:   content,
	}
}

// WriteComplete sends a harness completion event as a WebSocket message to the client.
//
// Expected:
//   - content describes the evaluation outcome.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteComplete(content string) {
	msg := c.WriteCompleteToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteCompleteToMsg converts harness completion content to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the evaluation outcome.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_complete and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteCompleteToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_complete",
		Content:   content,
	}
}

// WriteCriticFeedback sends a harness critic feedback event as a WebSocket message to the client.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//   - conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection.
func (c *WSConsumer) WriteCriticFeedback(content string) {
	msg := c.WriteCriticFeedbackToMsg(content)
	c.sendHarnessMsg(msg)
}

// WriteCriticFeedbackToMsg converts harness critic feedback to a WSChunkMsg for testing.
//
// Expected:
//   - content describes the critic's feedback on the plan.
//
// Returns:
//   - A WSChunkMsg with event_type set to harness_critic_feedback and content populated.
//
// Side effects:
//   - None.
func (c *WSConsumer) WriteCriticFeedbackToMsg(content string) WSChunkMsg {
	return WSChunkMsg{
		EventType: "harness_critic_feedback",
		Content:   content,
	}
}

// sendHarnessMsg sends a harness event message over the WebSocket, discarding errors
// because the HarnessEventConsumer interface methods do not return errors.
//
// Expected:
//   - msg is a valid WSChunkMsg to send.
//   - c.conn is an open WebSocket connection.
//
// Side effects:
//   - Writes JSON to the WebSocket connection if the connection is available.
func (c *WSConsumer) sendHarnessMsg(msg WSChunkMsg) {
	if err := sendWSMsg(c.ctx, c.conn, msg); err != nil {
		return
	}
}
