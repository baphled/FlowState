package server

import (
	"context"
	"encoding/json"
	"errors"
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

// writeError renders err as the contract {code,message} body; a read
// cancelled by a client disconnect writes nothing and one cancelled by
// shutdown is 503 "unavailable" (see cancelAware). The
// injected ErrCode governs the code; an empty or unknown code for a
// non-nil error is remapped onto internal (→ 500) explicitly instead
// of leaning on statusFor's fallback arm.
func (s *Server) writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errClientGone) { // nobody is left to read an answer
		return
	}
	if errors.Is(err, errShuttingDown) {
		s.writeErrorStatus(w, http.StatusServiceUnavailable, "unavailable", err.Error())
		return
	}
	code := s.ports.ErrCode(err)
	if _, known := statusByCode[code]; !known {
		code = "internal"
	}
	s.writeErrorStatus(w, statusFor(code), code, err.Error())
}

// errClientGone and errShuttingDown classify a read that was cut short
// by cancellation; cancelAware produces them and writeError renders them.
var (
	errClientGone   = errors.New("client disconnected")
	errShuttingDown = errors.New("server is shutting down")
)

// cancelAware wraps the read ports so a context error raised while the
// request context is done becomes errShuttingDown (the serve context
// was cancelled; see Serve) or errClientGone (the connection closed).
// Writes run on a detached context and keep their own errors, so they
// are not wrapped.
func cancelAware(p Ports) Ports {
	p.ListSessions = guardTwo(p.ListSessions)
	inspect := p.InspectSession
	p.InspectSession = func(ctx context.Context, id string) (Session, []Event, error) {
		sess, events, err := inspect(ctx, id)
		return sess, events, classifyCancel(ctx, err)
	}
	p.DiagnoseSession = guardTwo(p.DiagnoseSession)
	p.BudgetOverview = guardTwo(p.BudgetOverview)
	p.ValidateSwarm = guardTwo(p.ValidateSwarm)
	p.ValidateReceipt = guardTwo(p.ValidateReceipt)
	p.SessionStatus = guardTwo(p.SessionStatus)
	p.EventsAfter = guardThree(p.EventsAfter)
	return p
}

// classifyCancel maps err onto errShuttingDown or errClientGone when it
// is a context error and ctx is done; any other error passes through.
func classifyCancel(ctx context.Context, err error) error {
	if err == nil || ctx.Err() == nil ||
		(!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
		return err
	}
	if errors.Is(context.Cause(ctx), errShuttingDown) {
		return errShuttingDown
	}
	return errClientGone
}

func guardTwo[A, R any](f func(context.Context, A) (R, error)) func(context.Context, A) (R, error) {
	return func(ctx context.Context, a A) (R, error) {
		r, err := f(ctx, a)
		return r, classifyCancel(ctx, err)
	}
}

func guardThree[A, B, R any](f func(context.Context, A, B) (R, error)) func(context.Context, A, B) (R, error) {
	return func(ctx context.Context, a A, b B) (R, error) {
		r, err := f(ctx, a, b)
		return r, classifyCancel(ctx, err)
	}
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
