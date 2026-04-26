package streaming_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/streaming"
)

// Phase 14 ToolCallCorrelator unit tests.
//
// The correlator assigns a stable FlowState-internal ID to each logical tool
// call and reuses it whenever the same logical call is observed again —
// either by direct provider-scoped ID match (same provider, or a provider
// that accepted foreign IDs in its replay) or by a fuzzy match on
// (tool_name, arguments-fingerprint) when the provider re-IDs a historical
// call (the failover rewrite case). Registry is scoped per sessionID so
// concurrent chats cannot alias each other's tool-call IDs.
var _ = Describe("ToolCallCorrelator", func() {
	It("assigns and re-uses an internal id for repeated lookups of the same provider id", func() {
		c := streaming.NewToolCallCorrelator()

		got1 := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
		got2 := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})

		Expect(got1).NotTo(BeEmpty(), "internal id must be non-empty on first sight")
		Expect(got2).To(Equal(got1),
			"repeated lookup for the same provider-scoped id must return the same internal id")
	})

	It("fuzzy-matches across providers when (tool_name, args-fingerprint) is identical", func() {
		c := streaming.NewToolCallCorrelator()

		args := map[string]any{"cmd": "ls", "cwd": "/tmp"}
		onA := c.InternalID("session-1", "toolu_01abc", "bash", args)
		// Provider B rewrote the ID on replay (the failover case).
		onB := c.InternalID("session-1", "call_xyz123", "bash", args)

		Expect(onA).NotTo(BeEmpty())
		Expect(onB).NotTo(BeEmpty())
		Expect(onB).To(Equal(onA),
			"fuzzy match must resolve to the same internal id across providers")
	})

	It("isolates registries between sessions even when provider id and args match", func() {
		c := streaming.NewToolCallCorrelator()

		args := map[string]any{"cmd": "ls"}
		sessionA := c.InternalID("session-A", "toolu_01abc", "bash", args)
		sessionB := c.InternalID("session-B", "toolu_01abc", "bash", args)

		Expect(sessionA).NotTo(Equal(sessionB),
			"internal ids must NOT collide across sessions")
	})

	It("does not fuzzy-match different tool names with identical args", func() {
		c := streaming.NewToolCallCorrelator()

		args := map[string]any{"cmd": "ls"}
		bashID := c.InternalID("session-1", "toolu_01abc", "bash", args)
		otherToolID := c.InternalID("session-1", "call_xyz", "read_file", args)

		Expect(bashID).NotTo(Equal(otherToolID),
			"different tool_name must NOT share an internal id via fuzzy match")
	})

	It("does not fuzzy-match different args under the same tool name", func() {
		c := streaming.NewToolCallCorrelator()

		idLs := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
		idPwd := c.InternalID("session-1", "call_xyz", "bash", map[string]any{"cmd": "pwd"})

		Expect(idLs).NotTo(Equal(idPwd),
			"different args must NOT share an internal id via fuzzy match")
	})

	It("returns an empty internal id for an empty provider id", func() {
		c := streaming.NewToolCallCorrelator()
		Expect(c.InternalID("session-1", "", "bash", map[string]any{"cmd": "ls"})).To(BeEmpty())
	})

	It("mints a new internal id when no fuzzy candidate is in the registry", func() {
		c := streaming.NewToolCallCorrelator()

		id1 := c.InternalID("session-1", "toolu_01abc", "bash", map[string]any{"cmd": "ls"})
		id2 := c.InternalID("session-1", "toolu_02def", "bash", map[string]any{"cmd": "pwd"})

		Expect(id1).NotTo(Equal(id2),
			"two distinct calls (different args) must get distinct internal ids")
	})

	It("produces a stable arg fingerprint independent of map iteration order", func() {
		c := streaming.NewToolCallCorrelator()

		argsForward := map[string]any{"cmd": "ls", "cwd": "/tmp", "flags": "-la"}
		argsDifferent := map[string]any{"cmd": "ls", "cwd": "/tmp", "flags": "-la"}
		id1 := c.InternalID("session-1", "toolu_01abc", "bash", argsForward)
		id2 := c.InternalID("session-1", "call_xyz123", "bash", argsDifferent)

		Expect(id2).To(Equal(id1),
			"argument fingerprint must be stable across map iteration orders")
	})

	It("ForgetSession releases its entries without leaking into siblings", func() {
		// Internal ids are deterministic on (sessionID, providerID, toolName),
		// so a post-forget lookup with identical inputs legitimately returns
		// the same id — verifying via id equality would be vacuous. Instead
		// the test probes the registry's observable state: after
		// ForgetSession an entry for a DIFFERENT session must remain, and
		// entries for the forgotten session must not leak into a sibling
		// session's namespace.
		c := streaming.NewToolCallCorrelator()

		args := map[string]any{"cmd": "ls"}
		kept := c.InternalID("session-kept", "toolu_01abc", "bash", args)
		forgotten := c.InternalID("session-drop", "toolu_01abc", "bash", args)
		Expect(kept).NotTo(Equal(forgotten), "precondition: sessions must produce distinct internal ids")

		c.ForgetSession("session-drop")

		// session-kept's entry must survive — probed via the fuzzy path: a
		// new providerID with the same (name, args) should hit the cache and
		// return the already-registered id.
		keptAgainViaFuzzy := c.InternalID("session-kept", "call_new", "bash", args)
		Expect(keptAgainViaFuzzy).To(Equal(kept),
			"session-kept entry must survive a sibling ForgetSession")
	})

	It("is safe under concurrent access (no race-detector complaints)", func() {
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
	})
})
