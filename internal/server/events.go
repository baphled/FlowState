package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// SSE follow tuning: streamPollInterval is the poll cadence (a var so
// specs can shrink it); streamWriteWindow is how far ahead each write
// resets its deadline, so the 30s WriteTimeout cannot end a long follow.
var (
	streamPollInterval = 200 * time.Millisecond
	streamWriteWindow  = 15 * time.Second
)

// streamTerminalStatuses are the CLI follow surface's end states.
var streamTerminalStatuses = map[string]bool{
	"completed": true, "cancelled": true, "failed": true,
}

// eventsHandler answers GET /sessions/{id}/events?after=N with committed
// events past the strictly-greater cursor as the session-events array.
// The cursor defaults to 0; seqs are 0-based, so seq 0 needs cursor -1.
func (s *Server) eventsHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		after, problem := parseAfterCursor(r)
		if problem != "" {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", problem)
			return
		}
		events, err := s.ports.EventsAfter(r.Context(), id, after)
		if err != nil {
			s.writeError(w, err)
			return
		}
		items := make([]eventItem, len(events))
		for i, e := range events {
			items[i] = eventWire(e)
		}
		s.writeJSON(w, http.StatusOK, items)
	}
}

// streamHandler answers GET /sessions/{id}/events/stream?after=N with
// an SSE follow stream: one frame per new committed event, then a
// terminal status frame. Each poll reads status before events: every
// terminal path appends its final event before it sets the status, so
// the events read that follows cannot miss a frame. A failed mid-stream
// poll ends the stream; the pre-header probe keeps unknown ids on 404.
func (s *Server) streamHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		after, problem := parseAfterCursor(r)
		if problem != "" {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", problem)
			return
		}
		if _, err := s.ports.SessionStatus(r.Context(), id); err != nil {
			s.writeError(w, err)
			return
		}
		rc := http.NewResponseController(w)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		_ = rc.Flush()
		cursor := after
		for {
			status, serr := s.ports.SessionStatus(r.Context(), id)
			if serr != nil {
				return
			}
			events, err := s.ports.EventsAfter(r.Context(), id, cursor)
			if err != nil {
				return
			}
			for _, e := range events {
				if !s.writeFrame(rc, w, eventWire(e)) {
					return
				}
				cursor = e.Seq
			}
			if streamTerminalStatuses[status] {
				s.writeFrame(rc, w, streamStatusBody{SessionID: id, Status: status})
				return
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(streamPollInterval):
			}
		}
	}
}

// writeFrame emits one SSE data frame, first resetting the write
// deadline; deadline errors are ignored (test recorders have none).
func (s *Server) writeFrame(rc *http.ResponseController, w http.ResponseWriter, v any) bool {
	body, err := json.Marshal(v)
	if err != nil {
		return false
	}
	_ = rc.SetWriteDeadline(time.Now().Add(streamWriteWindow))
	if _, err := fmt.Fprintf(w, "data: %s\n\n", body); err != nil {
		return false
	}
	_ = rc.Flush()
	return true
}

// parseAfterCursor parses the optional cursor: only an absent key is 0. It
// returns a usage message for a query string that does not parse (whichever
// key is at fault), a cursor given more than once, an empty value and a
// non-integer; "" means the cursor is valid.
func parseAfterCursor(r *http.Request) (after int64, problem string) {
	vals, qerr := url.ParseQuery(r.URL.RawQuery) // not URL.Query: sql-guard selector scan
	if qerr != nil {
		return 0, "invalid query string"
	}
	given := vals["after"]
	if len(given) == 0 {
		return 0, ""
	}
	if len(given) > 1 {
		return 0, "after must be given once"
	}
	n, err := strconv.ParseInt(given[0], 10, 64)
	if err != nil {
		return 0, "after must be an integer"
	}
	return n, ""
}
