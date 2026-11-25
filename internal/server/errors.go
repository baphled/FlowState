package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
)

// ErrReservationUnsettled is wrapped by a CancelSession port error when the
// session was cancelled but settling its budget reservation failed; the port
// returns the cancelled row with it. The cancel route answers 500
// reservation_unsettled and names the cancelled status in a structured field.
var ErrReservationUnsettled = errors.New("reservation_unsettled")

// statusByCode maps the error-code set onto HTTP statuses (ADR-011 D11 added session_busy
// to the set in specs/CLI-AND-API-CONTRACT.md §2, which is outside the repository); internal is
// explicit so the writeError guard resolves without a silent
// fallback arm.
var statusByCode = map[string]int{
	"usage": http.StatusBadRequest, "session_not_found": http.StatusNotFound,
	"session_terminal": http.StatusConflict, "session_busy": http.StatusConflict, "session_cancelled": http.StatusConflict,
	"budget_exceeded": http.StatusConflict, "max_turns": http.StatusConflict, "receipt_invalid": http.StatusUnprocessableEntity,
	"provider_error": http.StatusBadGateway, "internal": http.StatusInternalServerError,
	"reservation_unsettled": http.StatusInternalServerError}

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
	errClientGone   error = &cancelCause{"client disconnected", "client_disconnected"}
	errShuttingDown error = &cancelCause{"server is shutting down", "shutdown"}
)

// Error implements error.
func (c *cancelCause) Error() string { return c.message }

// CancelReason is the reason a run cancelled by this cause records.
func (c *cancelCause) CancelReason() string { return c.reason }

// runContext detaches the run route's context from the request's and
// cancels it with errShuttingDown or errClientGone as its cause when the
// request ends, so the run records why it was cancelled. A child of the
// request context would inherit the request's cause instead, which is
// empty for a disconnect. A deadline on the request context is not visible
// through ctx.Deadline() here (expiry still cancels the run).
func runContext(r *http.Request) (context.Context, context.CancelFunc) {
	parent := r.Context()
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	stop := context.AfterFunc(parent, func() { cancel(cancelledBy(parent)) })
	return ctx, func() { stop(); cancel(nil) }
}

// cancelledBy is errShuttingDown when ctx was cancelled by shutdown and
// errClientGone otherwise.
func cancelledBy(ctx context.Context) error {
	if errors.Is(context.Cause(ctx), errShuttingDown) {
		return errShuttingDown
	}
	return errClientGone
}

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
	return cancelledBy(ctx)
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
