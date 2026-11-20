package server

import (
	"encoding/json"
	"net/http"
)

// statusByCode maps the closed error-code set of
// specs/CLI-AND-API-CONTRACT.md §2 onto HTTP statuses; internal is
// explicit so the writeError guard resolves without a silent
// fallback arm.
var statusByCode = map[string]int{
	"usage": http.StatusBadRequest, "session_not_found": http.StatusNotFound,
	"session_terminal": http.StatusConflict, "session_cancelled": http.StatusConflict,
	"budget_exceeded": http.StatusConflict, "receipt_invalid": http.StatusUnprocessableEntity,
	"provider_error": http.StatusBadGateway, "internal": http.StatusInternalServerError}

func statusFor(code string) int {
	if status, ok := statusByCode[code]; ok {
		return status
	}
	return http.StatusInternalServerError
}

func (s *Server) writeErrorStatus(w http.ResponseWriter, status int, code, message string) {
	s.writeJSON(w, status, apiError{Code: code, Message: message})
}

// writeError renders err as the contract {code,message} body. The
// injected ErrCode governs the code; an empty or unknown code for a
// non-nil error is remapped onto internal (→ 500) explicitly instead
// of leaning on statusFor's fallback arm.
func (s *Server) writeError(w http.ResponseWriter, err error) {
	code := s.ports.ErrCode(err)
	if _, known := statusByCode[code]; !known {
		code = "internal"
	}
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
