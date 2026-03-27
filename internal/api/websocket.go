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
		Content: chunk.Content,
		Done:    chunk.Done,
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
//   - Closes the connection when the engine stream is complete or an error occurs.
func (s *Server) handleSessionWebSocket(w http.ResponseWriter, r *http.Request) {
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
	defer closeWebSocket(conn)

	ctx := r.Context()
	for {
		incoming, ok := readWSMessage(ctx, conn)
		if !ok {
			return
		}
		if !s.serveWSSession(ctx, conn, id, incoming) {
			return
		}
	}
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

// serveWSSession forwards an incoming message to the session engine and streams the response.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - conn is an open WebSocket connection.
//   - sessionID identifies an existing session.
//   - msg contains the content to send to the engine.
//
// Returns:
//   - true to continue the read loop, false to close the connection.
//
// Side effects:
//   - Calls sessionManager.SendMessage and forwards response chunks to conn.
func (s *Server) serveWSSession(ctx context.Context, conn *websocket.Conn, sessionID string, msg wsIncomingMsg) bool {
	if msg.Content == "" {
		return true
	}
	chunks, err := s.sessionManager.SendMessage(ctx, sessionID, msg.Content)
	if err != nil {
		return false
	}
	return s.forwardWSChunks(ctx, conn, chunks)
}

// forwardWSChunks reads from a chunk channel and writes each chunk to the WebSocket connection.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - conn is an open WebSocket connection.
//   - chunks is a readable channel of provider.StreamChunk values.
//
// Returns:
//   - true to continue the read loop, false when streaming is complete or an error occurs.
//
// Side effects:
//   - Writes JSON-encoded chunks to conn.
func (s *Server) forwardWSChunks(ctx context.Context, conn *websocket.Conn, chunks <-chan provider.StreamChunk) bool {
	for chunk := range chunks {
		msg := BuildWSChunkMsg(chunk)
		if sendErr := sendWSMsg(ctx, conn, msg); sendErr != nil {
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

// WSConsumer implements streaming.DelegationConsumer and streaming.DelegationProgressConsumer
// by forwarding events as JSON over a WebSocket connection.
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
