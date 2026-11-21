// Package server is the HTTP transport adapter (ADR-007): it owns the
// net/http surface cmd is banned from and serves reads, session
// creation/lifecycle writes and synchronous turn runs over narrow Ports.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// maxBodyBytes caps every request body (1 MiB); Handler enforces it once
// for all routes and decodeStrict reports an over-limit read as usage.
const maxBodyBytes = 1 << 20

// TokenEnv names the optional bearer-token variable; empty disables auth.
const TokenEnv = "FLOWSTATE_SERVE_TOKEN"

// New wires a Server; a non-empty FLOWSTATE_SERVE_TOKEN enables bearer auth.
func New(p Ports) *Server {
	return &Server{ports: cancelAware(p), token: os.Getenv(TokenEnv)}
}

// Handler routes /health, /sessions, the /sessions/{id}[/action]
// subtree and the path-based swarm/receipt endpoints behind the
// token middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.methodOnly(http.MethodGet, s.handleHealth))
	mux.HandleFunc("/sessions", s.handleSessionsRoot)
	mux.HandleFunc("/sessions/", s.handleSessions)
	mux.HandleFunc("/swarm/validate", s.methodOnly(http.MethodPost, s.swarmValidateHandler))
	mux.HandleFunc("/swarm/run", s.methodOnly(http.MethodPost, s.swarmRunHandler))
	mux.HandleFunc("/receipt/validate", s.methodOnly(http.MethodPost, s.receiptValidateHandler))
	mux.HandleFunc("/", s.methodOnly(http.MethodGet, func(w http.ResponseWriter, _ *http.Request) {
		s.writeErrorStatus(w, http.StatusNotFound, "usage", "unknown route")
	}))
	return s.withToken(s.withBodyLimit(mux))
}

// withBodyLimit wraps every request body in http.MaxBytesReader so no
// handler can read more than maxBodyBytes.
func (s *Server) withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// decodeStrict decodes exactly one JSON value from rd into dst,
// rejecting unknown fields and any data after the value. An empty body
// yields io.EOF so callers that allow it can tell it apart.
func decodeStrict(rd io.Reader, dst any) error {
	if rd == nil { // a request built without a body, as a direct handler call may do
		return io.EOF
	}
	dec := json.NewDecoder(rd)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after JSON value")
		}
		return err
	}
	return nil
}

// badBody answers a failed body read or decode as 400 usage: an
// over-limit body names the limit, anything else the route's message.
func (s *Server) badBody(w http.ResponseWriter, err error, invalid string) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		s.writeErrorStatus(w, http.StatusBadRequest, "usage",
			fmt.Sprintf("request body exceeds the %d byte limit", tooBig.Limit))
		return
	}
	s.writeErrorStatus(w, http.StatusBadRequest, "usage", invalid)
}

// decodeBody strictly decodes the request body into dst, answering 400
// usage itself on failure; it reports whether the handler may continue.
func (s *Server) decodeBody(w http.ResponseWriter, r *http.Request, dst any, invalid string) bool {
	if err := decodeStrict(r.Body, dst); err != nil {
		s.badBody(w, err, invalid)
		return false
	}
	return true
}

// httpServer builds the transport's net/http server with one timeout
// envelope: read headers 5s, writes 30s and idle 30s. Serve and the
// construction spec share this seam.
func (s *Server) httpServer() *http.Server {
	return &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
}

// Serve runs Handler on l until ctx is done, then drains within 5s.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	srv := s.httpServer()
	// Requests inherit the serve context, so shutdown cancels in-flight
	// reads instead of waiting the whole grace period on them; writes
	// opt out through writeContext and are drained by Shutdown.
	// A request's context is cancelled with errShuttingDown as its cause when
	// ctx ends, whereas a client disconnect carries no cause.
	baseCtx, cancelBase := context.WithCancelCause(context.WithoutCancel(ctx))
	defer cancelBase(nil)
	stop := context.AfterFunc(ctx, func() { cancelBase(errShuttingDown) })
	defer stop()
	srv.BaseContext = func(net.Listener) context.Context { return baseCtx }
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// writeContext detaches a write from request cancellation: neither a
// client disconnect nor shutdown may stop a multi-step store operation
// half way, and Shutdown's grace period still bounds the wait for it.
func writeContext(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) withToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" &&
			subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")), []byte(s.token)) != 1 {
			s.writeErrorStatus(w, http.StatusUnauthorized, "usage", "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}
