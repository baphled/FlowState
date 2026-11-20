package server

import (
	"encoding/json"
	"net/http"
)

// budgetWire translates the budget DTO into its wire object; a nil
// pool renders budget_pool/available as JSON null, as the CLI does.
func budgetWire(b Budget) budgetBody {
	return budgetBody{SessionID: b.SessionID, Reserved: b.Reserved,
		BudgetPool: b.Pool, Available: b.Available}
}

// budgetRoute answers the two /sessions/{id}/budget methods — GET for
// the overview, POST for the pool anchor — and 405 usage otherwise.
func (s *Server) budgetRoute(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.budgetHandler(id)(w, r)
		case http.MethodPost:
			s.setBudgetHandler(id)(w, r)
		default:
			s.writeErrorStatus(w, http.StatusMethodNotAllowed, "usage", "method not allowed")
		}
	}
}

// budgetHandler answers GET /sessions/{id}/budget with the session's
// budget overview in the session-budget wire shape (schemas/cli.json
// key session-budget, shared with the CLI budget output). An unknown
// session surfaces session_not_found → 404: the port keeps the store
// sentinel unwrapped.
func (s *Server) budgetHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := s.ports.BudgetOverview(r.Context(), id)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, budgetWire(b))
	}
}

// setBudgetHandler answers POST /sessions/{id}/budget {"pool": N} by
// anchoring the session's shared pool and returning the updated
// overview in the session-budget wire shape. Body-contract validation
// lives in the transport — not the port closure — so the ports mirror
// app semantics 1:1: a malformed body, a missing pool or a
// non-positive pool is 400 usage. Positivity mirrors the run
// command's anchor seam (an explicit budget anchors only when > 0);
// SetBudgetPool's <= 0 clear semantics are deliberately not exposed.
func (s *Server) setBudgetHandler(id string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req budgetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", "invalid budget body")
			return
		}
		if req.Pool == nil {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", "pool is required")
			return
		}
		if *req.Pool <= 0 {
			s.writeErrorStatus(w, http.StatusBadRequest, "usage", "pool must be positive")
			return
		}
		b, err := s.ports.SetBudgetPool(r.Context(), id, *req.Pool)
		if err != nil {
			s.writeError(w, err)
			return
		}
		s.writeJSON(w, http.StatusOK, budgetWire(b))
	}
}
