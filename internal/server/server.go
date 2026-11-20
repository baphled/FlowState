// Package server is the HTTP transport adapter (ADR-007): it owns the
// net/http surface cmd is banned from and serves read-only over narrow Ports.
package server

import (
	"context"
	"crypto/subtle"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// TokenEnv names the optional bearer-token variable; empty disables auth.
const TokenEnv = "FLOWSTATE_SERVE_TOKEN"

// New wires a Server; a non-empty FLOWSTATE_SERVE_TOKEN enables bearer auth.
func New(p Ports) *Server {
	return &Server{ports: p, token: os.Getenv(TokenEnv)}
}

// Handler routes /health, /sessions, the /sessions/{id}[/action]
// subtree and the path-based swarm/receipt endpoints behind the
// token middleware.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.getOnly(s.handleHealth))
	mux.HandleFunc("/sessions", s.getOnly(s.handleList))
	mux.HandleFunc("/sessions/", s.handleSessions)
	mux.HandleFunc("/swarm/validate", s.methodOnly(http.MethodPost, s.swarmValidateHandler))
	mux.HandleFunc("/swarm/run", s.methodOnly(http.MethodPost, s.swarmRunHandler))
	mux.HandleFunc("/receipt/validate", s.methodOnly(http.MethodPost, s.receiptValidateHandler))
	mux.HandleFunc("/", s.getOnly(func(w http.ResponseWriter, _ *http.Request) {
		s.writeErrorStatus(w, http.StatusNotFound, "usage", "unknown route")
	}))
	return s.withToken(mux)
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

func (s *Server) getOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			s.writeErrorStatus(w, http.StatusMethodNotAllowed, "usage", "method not allowed")
			return
		}
		h(w, r)
	}
}
