package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// secretBodyKeyPattern matches credential-looking request keys,
// mirroring the CLI config scan so the same key names are rejected
// in serve bodies (secrets are environment-only).
var secretBodyKeyPattern = regexp.MustCompile(`(?i)key|token|secret|password`)

// createHandler answers POST /sessions {"provider","model","task"}
// with the created row in the session-new wire shape.
// Empty provider/model defer to the port's serve-config defaults, a
// missing task is usage, and secret-looking body keys are rejected
// with 400 (the body is client input, unlike the config document).
func (s *Server) createHandler(w http.ResponseWriter, r *http.Request) {
	var data []byte
	if r.Body != nil { // absent only on a request built without one; then the body is empty
		var rerr error
		if data, rerr = io.ReadAll(r.Body); rerr != nil {
			s.badBody(w, rerr, "invalid create body")
			return
		}
	}
	var raw any
	if uerr := decodeStrict(bytes.NewReader(data), &raw); uerr != nil {
		s.badBody(w, uerr, "invalid create body")
		return
	}
	if serr := rejectSecretBodyKeys(raw); serr != nil {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", serr.Error())
		return
	}
	var body createBody
	if derr := decodeStrict(bytes.NewReader(data), &body); derr != nil {
		s.badBody(w, derr, "invalid create body")
		return
	}
	if strings.TrimSpace(body.Task) == "" {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage", "task is required")
		return
	}
	sess, err := s.ports.CreateSession(writeContext(r), CreateRequest(body))
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, sessionNewBody{ID: sess.ID, Status: sess.Status, ParentID: sess.ParentID})
}

// rejectSecretBodyKeys walks a decoded request body and rejects any
// object key that looks like a credential at any depth, mirroring the
// CLI config secret scan's rule for the serve surface.
func rejectSecretBodyKeys(v any) error {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			if secretBodyKeyPattern.MatchString(k) {
				return fmt.Errorf("body key %q looks like a secret; secrets must come from the environment", k)
			}
			if err := rejectSecretBodyKeys(child); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range t {
			if err := rejectSecretBodyKeys(item); err != nil {
				return err
			}
		}
	}
	return nil
}

// closeHandler answers POST /sessions/{id}/close with the closed
// session row. Closing is not idempotent by contract: closing an
// already-closed/terminal session surfaces the app's session_terminal
// error, which the shared code table maps onto 409.
func (s *Server) closeHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.ports.CloseSession(writeContext(r), id)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, sessionWire(sess))
	}
}

// cancelHandler answers POST /sessions/{id}/cancel with the cancelled
// session row. As with close, cancelling a terminal session surfaces
// session_terminal → 409 rather than a second terminal transition.
func (s *Server) cancelHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.ports.CancelSession(writeContext(r), id)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, sessionWire(sess))
	}
}

// doctorHandler answers GET /sessions/{id}/doctor with the read-only
// diagnosis in the session-doctor wire shape (schemas/cli.json key
// session-doctor, shared with the CLI doctor output).
func (s *Server) doctorHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d, err := s.ports.DiagnoseSession(r.Context(), id)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, diagnosisWire(d))
	}
}

// diagnosisWire translates the diagnosis DTO into its wire object; a
// nil pool renders budget_pool/available as JSON null.
func diagnosisWire(d Diagnosis) diagnosisBody {
	return diagnosisBody{SessionID: d.SessionID, Status: d.Status, Crashed: d.Crashed,
		RunActivity: d.RunActivity, PendingMessages: d.PendingMessages, Reserved: d.Reserved,
		BudgetPool: d.Pool, Available: d.Available, FinishReason: d.FinishReason, Truncated: d.Truncated}
}

// runWriteWindow extends the run route's write deadline for one
// synchronous turn, which can outlive the global 30s WriteTimeout.
const runWriteWindow = 10 * time.Minute

// runHandler answers POST /sessions/{id}/run: one synchronous
// provider turn behind the run port, mirroring `session run <id>`
// including the detect-crash → recover → resume-pause preamble, with
// the projected event log answered as the session-events array. The
// optional body {"budget":N,"max_tokens":N} mirrors the CLI flags
// (an empty body means no options; unknown fields are usage); negative
// values are usage. Failure classes ride the shared code table
// (provider 502, budget stop 409, terminal 409). The run's
// EventWriter holds no connection across the turn, so polls proceed.
func (s *Server) runHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req runRequest
		if err := decodeStrict(r.Body, &req); err != nil && !errors.Is(err, io.EOF) {
			s.badBody(w, err, "invalid run body")
			return
		}
		if req.Budget < 0 || req.MaxTokens < 0 {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", "budget and max_tokens must not be negative")
			return
		}
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(runWriteWindow))
		events, err := s.ports.RunSession(r.Context(), id, RunOptions(req))
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
