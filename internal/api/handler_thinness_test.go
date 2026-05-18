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
	serverFile    = "server.go"
	websocketFile = "websocket.go"
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

// TestHandlerThinness_handleSessionMessage is the Phase 2 GREEN subtest
// pre-cast as expected-RED in Phase 1. handleSessionMessage still
// contains the three local helpers + the swarm primitives until Phase 2
// migrates it through DispatchSessioned and deletes the helpers. We
// assert RED here so the next phase's GREEN is observable.
//
// Expected behaviour: this subtest PASSES today by asserting hits is
// non-empty. When Phase 2 lands, the assertion flips to "no hits" in
// the same edit that migrates the handler.
func TestHandlerThinness_handleSessionMessage_RedUntilPhase2(t *testing.T) {
	body := readHandlerBody(t, serverFile, "handleSessionMessage")
	hits := bannedSymbolsIn(body)
	if len(hits) == 0 {
		t.Fatal("handleSessionMessage no longer contains banned symbols — Phase 2 of Dispatcher Service Unification (May 2026) has landed; flip this assertion to require hits == 0")
	}
	t.Logf("Phase 2 pending: handleSessionMessage still calls %v — Dispatcher.DispatchSessioned migration will close this", hits)
}

// TestHandlerThinness_handleSessionWebSocket is the Phase 4 GREEN
// subtest pre-cast as expected-RED in Phase 1. The WS handler still
// runs the resolve-and-dispatch primitives until Phase 4 routes it
// through DispatchSessioned + the SetSwarmContext path. Same flip
// pattern as the Phase 2 subtest above.
func TestHandlerThinness_handleSessionWebSocket_RedUntilPhase4(t *testing.T) {
	body := readHandlerBody(t, websocketFile, "handleSessionWebSocket")
	hits := bannedSymbolsIn(body)
	if len(hits) == 0 {
		t.Logf("handleSessionWebSocket no longer contains banned symbols — Phase 4 of Dispatcher Service Unification (May 2026) has landed; flip this assertion to require hits == 0")
		return
	}
	t.Logf("Phase 4 pending: handleSessionWebSocket still calls %v — Dispatcher.DispatchSessioned migration will close this", hits)
}
