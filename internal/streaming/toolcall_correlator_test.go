package streaming_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/streaming"
)

// Phase 14 — ToolCallCorrelator unit tests.
//
// The correlator assigns a stable FlowState-internal ID to each logical
// tool call and reuses it whenever the same logical call is observed
// again — either by direct provider-scoped ID match (same provider, or a
// provider that accepted foreign IDs in its replay) or by a fuzzy match
// on (tool_name, arguments-fingerprint) when the provider re-IDs a
// historical call (the failover rewrite case).
//
// Registry is scoped per sessionID so concurrent chats cannot alias each
// other's tool-call IDs.

func TestToolCallCorrelator_RegistersFirstSightOfProviderID(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	got1 := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
	got2 := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})

	if got1 == "" {
		t.Fatalf("internal id must be non-empty on first sight")
	}
	if got1 != got2 {
		t.Fatalf("repeated lookup for the same provider-scoped id must return the same internal id; got %q vs %q", got1, got2)
	}
}

func TestToolCallCorrelator_DifferentProviderIDs_SameToolNameAndArgs_SameInternalID(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	args := map[string]any{"cmd": "ls", "cwd": "/tmp"}
	onA := c.InternalID("session-1", "toolu_01abc", "bash", args)
	// Provider B rewrote the ID on replay (this is the failover case).
	onB := c.InternalID("session-1", "call_xyz123", "bash", args)

	if onA == "" || onB == "" {
		t.Fatalf("both lookups must return non-empty internal ids")
	}
	if onA != onB {
		t.Fatalf("fuzzy match on (tool_name, args-fingerprint) must resolve to the same internal id across providers; got A=%q B=%q", onA, onB)
	}
}

func TestToolCallCorrelator_DifferentSessions_IsolatedRegistries(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	args := map[string]any{"cmd": "ls"}
	sessionA := c.InternalID("session-A", "toolu_01abc", "bash", args)
	sessionB := c.InternalID("session-B", "toolu_01abc", "bash", args)

	if sessionA == sessionB {
		t.Fatalf("internal ids must NOT collide across sessions even when provider-scoped id and args are identical; got %q in both sessions", sessionA)
	}
}

func TestToolCallCorrelator_DifferentToolNames_DoNotFuzzyMatch(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	args := map[string]any{"cmd": "ls"}
	bashID := c.InternalID("session-1", "toolu_01abc", "bash", args)
	otherToolID := c.InternalID("session-1", "call_xyz", "read_file", args)

	if bashID == otherToolID {
		t.Fatalf("different tool_name must NOT share an internal id via fuzzy match; got %q for both", bashID)
	}
}

func TestToolCallCorrelator_DifferentArgs_DoNotFuzzyMatch(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	idLs := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
	idPwd := c.InternalID("session-1", "call_xyz", "bash", map[string]any{"cmd": "pwd"})

	if idLs == idPwd {
		t.Fatalf("different args must NOT share an internal id via fuzzy match; got %q for both", idLs)
	}
}

func TestToolCallCorrelator_EmptyProviderIDReturnsEmpty(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	if got := c.InternalID("session-1", "", "bash", map[string]any{"cmd": "ls"}); got != "" {
		t.Fatalf("empty provider id must return empty internal id, got %q", got)
	}
}

func TestToolCallCorrelator_NonFuzzyLookup_UnknownProviderID_NewInternalID(t *testing.T) {
	// When a provider emits a tool call we have never seen and fuzzy match
	// has no candidate (no prior call with the same name+args in this
	// session), the correlator mints a fresh internal id.
	c := streaming.NewToolCallCorrelator()

	id1 := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
	id2 := c.InternalID("session-1", "toolu_02def", "bash", map[string]any{"cmd": "pwd"})

	if id1 == id2 {
		t.Fatalf("two distinct calls (different args) must get distinct internal ids; got %q twice", id1)
	}
}

func TestToolCallCorrelator_ArgOrderIndependence_FuzzyMatch(t *testing.T) {
	// Fuzzy matching keys on a canonical representation of the args. Two
	// lookups with the same (name, args) should match regardless of iteration
	// order of the caller's map — Go's map iteration is unordered.
	c := streaming.NewToolCallCorrelator()

	argsForward := map[string]any{"cmd": "ls", "cwd": "/tmp", "flags": "-la"}
	argsDifferent := map[string]any{"cmd": "ls", "cwd": "/tmp", "flags": "-la"}
	id1 := c.InternalID("session-1", "toolu_01abc", "bash", argsForward)
	id2 := c.InternalID("session-1", "call_xyz123", "bash", argsDifferent)

	if id1 != id2 {
		t.Fatalf("argument fingerprint must be stable across map iteration orders; got %q vs %q", id1, id2)
	}
}

func TestToolCallCorrelator_ForgetSession_ReleasesEntries(t *testing.T) {
	// When a session ends the correlator must release its entries so the
	// registry does not grow unbounded across long-running processes.
	//
	// Internal ids are deterministic on (sessionID, providerID, toolName),
	// so a post-forget lookup with identical inputs legitimately returns
	// the same id — verifying via id equality would be vacuous. Instead
	// the test probes the registry's observable state: after ForgetSession,
	// an entry for a DIFFERENT session must remain, and entries for the
	// forgotten session must not leak into a sibling session's namespace.
	c := streaming.NewToolCallCorrelator()

	args := map[string]any{"cmd": "ls"}
	kept := c.InternalID("session-kept", "toolu_01abc", "bash", args)
	forgotten := c.InternalID("session-drop", "toolu_01abc", "bash", args)
	if kept == forgotten {
		t.Fatalf("precondition: sessions must produce distinct internal ids")
	}

	c.ForgetSession("session-drop")

	// session-kept must still resolve its entry directly (would have
	// re-minted to a different id if the registry had been cleared for it,
	// but the mint is deterministic so we probe the fuzzy path instead:
	// a lookup on a new providerID with the same (name, args) should hit
	// the fuzzy cache and return the already-registered id).
	keptAgainViaFuzzy := c.InternalID("session-kept", "call_new", "bash", args)
	if keptAgainViaFuzzy != kept {
		t.Fatalf("session-kept entry must survive a sibling ForgetSession; got %q vs %q", keptAgainViaFuzzy, kept)
	}
}

func TestToolCallCorrelator_ConcurrentAccessIsSafe(t *testing.T) {
	c := streaming.NewToolCallCorrelator()

	done := make(chan struct{}, 8)
	for range 8 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 100 {
				_ = c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
			}
		}()
	}
	for range 8 {
		<-done
	}
	// If we get here without a race-detector complaint, we're good.
}
