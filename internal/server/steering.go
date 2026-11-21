package server

import (
	"net/http"
	"strings"
)

// messageWire translates the message DTO into its wire object.
func messageWire(m Message) messageBody {
	return messageBody(m)
}

// messageHandler answers POST /sessions/{id}/message
// {"from": sender, "body": text} with the recorded steering directive
// in the session-message wire shape (schemas/cli.json key
// session-message, shared with the CLI message output). Required
// fields are validated in the transport, mirroring the CLI's
// --from/--body gate (a whitespace-only body is empty). Error
// semantics note: app.SendSteeringMessage wraps every store failure —
// unknown session and delegation-scope rejections alike — into
// ErrUsage, and ErrorCode matches the usage sentinel before the
// session sentinels, so this endpoint answers 400 usage for all of
// them and a 404 is unreachable by construction; that S2 app
// wrapping is left as-is.
func (s *Server) messageHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req messageRequest
		if !s.decodeBody(w, r, &req, "invalid message body") {
			return
		}
		if req.From == "" || strings.TrimSpace(req.Body) == "" {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", "from and body are required")
			return
		}
		m, err := s.ports.SendSteeringMessage(writeContext(r), id, req.From, req.Body)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, messageWire(m))
	}
}
