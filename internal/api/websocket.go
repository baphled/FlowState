package api

import (
	"net/http"

	"github.com/coder/websocket"
)

// handleSessionWebSocket upgrades the connection to WebSocket and handles bidirectional messaging.
//
// Expected:
//   - The request can be upgraded to a WebSocket connection.
//
// Side effects:
//   - Reads messages from the WebSocket connection.
//   - Echoes received messages back to the client.
func (s *Server) handleSessionWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		http.Error(w, "websocket upgrade failed", http.StatusInternalServerError)
		return
	}
	defer closeWebSocket(conn)

	ctx := r.Context()
	for {
		_, msg, err := conn.Read(ctx)
		if err != nil {
			closeWebSocket(conn)
			return
		}
		err = conn.Write(ctx, websocket.MessageText, msg)
		if err != nil {
			return
		}
	}
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
