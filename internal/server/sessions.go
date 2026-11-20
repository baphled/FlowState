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

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/sessions/")
	if id == "" || strings.Contains(id, "/") {
		s.writeErrorStatus(w, http.StatusNotFound, "usage", "unknown route")
		return
	}
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
