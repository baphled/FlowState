package api_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Handler-thinness regression pin per "Dispatcher Service Unification
// (May 2026)" Acceptance Criterion #4: handleChat, handleSessionMessage
// and handleSessionWebSocket must make ZERO direct calls to the
// resolve-and-dispatch primitives. Every "user-input → engine-stream"
// path goes through internal/dispatch.Dispatcher.
//
// The six banned symbols are the three local helpers that grew on
// /messages this session (and will be deleted in Phase 2) plus the
// three swarm-package calls that should live inside Dispatcher only.
//
// Phase 1 ships handleChat green; handleSessionMessage stays RED until
// Phase 2 migrates it, and handleSessionWebSocket stays RED until
// Phase 4 routes it through DispatchSessioned. The Phase 1 subtest
// asserts handleChat is already clean; the Phase 2-4 subtests are
// expected RED (asserted to be RED so the next phase's GREEN is
// observable).

const (
	serverFile = "server.go"
)

var bannedSymbols = []string{
	// Local helpers (Phase 2 deletes them from server.go).
	"resolveAutoDispatchSwarm",
	"resolveInContentMention",
	"wrapWithSwarmLifecycle",
	// Swarm-package primitives that belong inside Dispatcher only.
	"swarm.ExtractAtMentions",
	"swarm.DispatchSwarm",
	"swarm.ResolveTarget",
}

// readHandlerBody extracts the body of the named method by brace-balance
// scan starting from the "func (s *Server) <name>(" prefix. Plain
// string scanning; no go/ast; matches the plan's "no runtime.FuncForPC,
// no go/ast" constraint.
//
// Returns the substring between the opening and matching closing brace,
// inclusive of the body's text but not the signature. Returns "" when
// the function is not found.
func readHandlerBody(t *testing.T, file, funcName string) string {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	apiDir := filepath.Dir(thisFile)
	path := filepath.Join(apiDir, file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(data)
	needle := "func (s *Server) " + funcName + "("
	start := strings.Index(src, needle)
	if start < 0 {
		t.Fatalf("function %s not found in %s", funcName, file)
	}
	// Advance to the first { after the signature.
	open := strings.Index(src[start:], "{")
	if open < 0 {
		t.Fatalf("no opening brace for %s in %s", funcName, file)
	}
	open += start
	depth := 1
	for i := open + 1; i < len(src); i++ {
		switch src[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return src[open+1 : i]
			}
		}
	}
	t.Fatalf("unbalanced braces for %s in %s", funcName, file)
	return ""
}

func bannedSymbolsIn(body string) []string {
	var hits []string
	for _, sym := range bannedSymbols {
		if strings.Contains(body, sym) {
			hits = append(hits, sym)
		}
	}
	return hits
}

// TestHandlerThinness_handleChat is the Phase 1 GREEN subtest. It
// verifies the /api/chat handler routes through the Dispatcher and
// makes no direct calls to the banned symbols.
func TestHandlerThinness_handleChat(t *testing.T) {
	body := readHandlerBody(t, serverFile, "handleChat")
	hits := bannedSymbolsIn(body)
	if len(hits) > 0 {
		t.Fatalf("handleChat must not call %v directly — route through dispatch.Dispatcher per Dispatcher Service Unification (May 2026) Phase 1", hits)
	}
}

// TestHandlerThinness_handleSessionMessage is the Phase 2 GREEN subtest.
// Phase 2 of "Dispatcher Service Unification (May 2026)" routes
// /messages through dispatch.Dispatcher.DispatchSessioned, deleting the
// three local helpers (resolveAutoDispatchSwarm, resolveInContentMention,
// wrapWithSwarmLifecycle). The handler must make ZERO direct calls to
// the banned symbols — all resolve + dispatch + lifecycle logic lives
// once in Dispatcher.
func TestHandlerThinness_handleSessionMessage(t *testing.T) {
	body := readHandlerBody(t, serverFile, "handleSessionMessage")
	hits := bannedSymbolsIn(body)
	if len(hits) > 0 {
		t.Fatalf("handleSessionMessage must not call %v directly — route through dispatch.Dispatcher.DispatchSessioned per Dispatcher Service Unification (May 2026) Phase 2", hits)
	}
}

// TestHandlerThinness_handleSessionWebSocket retired in Phase-4-Commit-2
// of "Turn-Based Post-Then-Poll Architecture (May 2026)" — the
// handleSessionWebSocket handler and its source file were deleted with
// the session-scoped WebSocket route. The negative-contract spec at
// internal/api/server_test.go pins that the route is no longer
// registered on the mux.
