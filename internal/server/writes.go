package server

import "net/http"

// closeHandler answers POST /sessions/{id}/close with the closed
// session row. Closing is not idempotent by contract: closing an
// already-closed/terminal session surfaces the app's session_terminal
// error, which the shared code table maps onto 409.
func (s *Server) closeHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.ports.CloseSession(r.Context(), id)
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
		sess, err := s.ports.CancelSession(r.Context(), id)
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
		BudgetPool: d.Pool, Available: d.Available}
}
