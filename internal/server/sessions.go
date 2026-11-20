package server

import (
	"net/http"
	"strings"
)

// wireTimeFormat is the CLI RFC3339Nano UTC wire format.
const wireTimeFormat = "2006-01-02T15:04:05.999999999Z07:00"

// sessionWire translates the DTO into its wire object.
func sessionWire(s Session) sessionItem {
	return sessionItem{
		ID: s.ID, CreatedAtUTC: s.CreatedAt.UTC().Format(wireTimeFormat),
		UpdatedAtUTC: s.UpdatedAt.UTC().Format(wireTimeFormat), Status: s.Status,
		Provider: s.Provider, ModelID: s.ModelID, Harness: s.Harness,
		ConfigSHA: s.ConfigSHA, ParentID: s.ParentID,
	}
}

// eventWire translates the DTO into its wire object.
func eventWire(e Event) eventItem {
	return eventItem{
		SessionID: e.SessionID, Seq: e.Seq, CreatedAtUTC: e.CreatedAt.UTC().Format(wireTimeFormat),
		Actor: e.Actor, Type: e.Type, Payload: e.Payload, PayloadSHA: e.PayloadSHA,
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.ports.ListSessions(r.Context(), r.FormValue("status"))
	if err != nil {
		s.writeError(w, err)
		return
	}
	items := make([]sessionItem, len(rows))
	for i, row := range rows {
		items[i] = sessionWire(row)
	}
	s.writeJSON(w, http.StatusOK, items)
}

// handleSessions routes the /sessions/ subtree: /{id} and /{id}/doctor
// are GET reads, /{id}/close, /{id}/cancel and /{id}/run are POST
// writes, /{id}/events serves the GET snapshot and /{id}/events/stream
// the SSE follow, /{id}/budget GET+POST, /{id}/message POST; any other
// shape stays the S1 404 usage unknown route.
func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	id, action, ok := splitSessionPath(r.URL.Path)
	if !ok {
		s.writeErrorStatus(w, http.StatusNotFound, "usage", "unknown route")
		return
	}
	switch action {
	case "":
		s.methodOnly(http.MethodGet, s.inspectHandler(id))(w, r)
	case "doctor":
		s.methodOnly(http.MethodGet, s.doctorHandler(id))(w, r)
	case "close":
		s.methodOnly(http.MethodPost, s.closeHandler(id))(w, r)
	case "cancel":
		s.methodOnly(http.MethodPost, s.cancelHandler(id))(w, r)
	case "budget":
		s.budgetRoute(id)(w, r)
	case "message":
		s.methodOnly(http.MethodPost, s.messageHandler(id))(w, r)
	case "events":
		s.methodOnly(http.MethodGet, s.eventsHandler(id))(w, r)
	case "events/stream":
		s.methodOnly(http.MethodGet, s.streamHandler(id))(w, r)
	case "run":
		s.methodOnly(http.MethodPost, s.runHandler(id))(w, r)
	default:
		s.writeErrorStatus(w, http.StatusNotFound, "usage", "unknown route")
	}
}

// handleSessionsRoot routes the collection: GET lists sessions and
// POST creates one (S5); other methods stay 405 usage.
func (s *Server) handleSessionsRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleList(w, r)
	case http.MethodPost:
		s.createHandler(w, r)
	default:
		s.writeErrorStatus(w, http.StatusMethodNotAllowed, "usage", "method not allowed")
	}
}

// splitSessionPath splits "/sessions/{id}[/action[/sub]]" into the
// session id and action path; the only three-segment shape is the
// events follow stream. Ok is false for any other path shape.
func splitSessionPath(path string) (id, action string, ok bool) {
	parts := strings.Split(strings.TrimPrefix(path, "/sessions/"), "/")
	switch len(parts) {
	case 1:
		return parts[0], "", parts[0] != ""
	case 2:
		if parts[0] != "" && parts[1] != "" {
			return parts[0], parts[1], true
		}
	case 3:
		if parts[0] != "" && parts[1] == "events" && parts[2] == "stream" {
			return parts[0], "events/stream", true
		}
	}
	return "", "", false
}

// methodOnly answers 405 usage unless the request carries the route's
// single allowed method.
func (s *Server) methodOnly(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != method {
			s.writeErrorStatus(w, http.StatusMethodNotAllowed, "usage", "method not allowed")
			return
		}
		h(w, r)
	}
}

// inspectHandler answers GET /sessions/{id} with the session and its
// ordered event log.
func (s *Server) inspectHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, events, err := s.ports.InspectSession(r.Context(), id)
		if err != nil {
			s.writeError(w, err)
			return
		}
		items := make([]eventItem, len(events))
		for i, e := range events {
			items[i] = eventWire(e)
		}
		s.writeJSON(w, http.StatusOK, inspectBody{sessionItem: sessionWire(sess), Events: items})
	}
}
