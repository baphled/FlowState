package server

import (
	"encoding/json"
	"net/http"
)

// statusByCode maps the closed error-code set of
// specs/CLI-AND-API-CONTRACT.md §2 onto HTTP statuses.
var statusByCode = map[string]int{
	"usage": http.StatusBadRequest, "session_not_found": http.StatusNotFound,
	"session_terminal": http.StatusConflict, "session_cancelled": http.StatusConflict,
	"budget_exceeded": http.StatusConflict, "receipt_invalid": http.StatusUnprocessableEntity,
	"provider_error": http.StatusBadGateway}

func statusFor(code string) int {
	if status, ok := statusByCode[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

func (s *Server) writeErrorStatus(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, apiError{Code: code, Message: message})
}

func (s *Server) writeError(w http.ResponseWriter, err error) {
	code := s.ports.ErrCode(err)
	s.writeErrorStatus(w, statusFor(code), code, err.Error())
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil { // unreachable for S1's closed wire shapes
		http.Error(w, `{"code":"internal","message":"encode response"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
