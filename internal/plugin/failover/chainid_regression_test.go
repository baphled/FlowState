package failover_test

import (
	"context"
	"errors"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
)

// P5/B9 — failover regression gates.
//
// Background (from `project_flowstate_failover_bugs` memory note):
// when a provider failover happens mid-stream, the tool_use_id (Anthropic
// `block.id` / OpenAI `tool_calls[].id`) is NOT translated to the new
// provider's ID space. The proper fix is a separate, larger piece of work
// tracked in that note. This phase (P5) only pins down the current invariants
// and documents the known-broken case so a future phase can fix it without
// reintroducing regressions somewhere else.
//
// What P5 commits to:
//
//  1. Delegation ChainID is a FlowState-assigned identifier (not a
//     provider-scoped ID). It must survive any number of provider failovers
//     end-to-end because the ChainID travels in the DelegationInfo payload,
//     not as a provider tool_use id. This is asserted as a guarded invariant
//     so any future refactor that accidentally re-derives ChainID from the
//     provider breaks the gate.
//
//  2. Tool_use_id is a provider-scoped opaque string. A tool_call started on
//     provider A and completed on provider B will legitimately surface two
//     uncorrelated ToolCallIDs. The coalesce path in P3 pairs these by ID,
//     so the current observable is "one tool_call with no matching result"
//     on provider A and "one tool_result with no matching call" on
//     provider B. This test pins the current broken behaviour explicitly so
//     a future fix can flip the assertion, rather than silently reshaping it.
var _ = Describe("Failover event identity invariants (P5/B9 regression gate)", func() {
	var (
		manager  *failover.Manager
		registry *provider.Registry
		health   *failover.HealthManager
		sh       *failover.StreamHook
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		manager = failover.NewManager(registry, health, 2*time.Second)
		sh = failover.NewStreamHook(manager, nil, "")
	})

	// --- Contract 1: ChainID survives failover ---------------------------
	//
	// ChainID is set at the delegation engine (internal/engine/delegation.go)
	// and travels on every DelegationInfo payload that the engine emits. It
	// is the same identifier across providers because it is not provider-
	// scoped — a failover mid-delegation must not change it.
	//
	// This regression gate uses two mocked providers, A and B. A fails at
	// connect time so the stream-hook fails over to B. B emits a delegation
	// chunk carrying a ChainID. The gate asserts that the ChainID surfaced to
	// the consumer equals the ChainID the test planted on B's chunk —
	// i.e. neither the failover path nor the hook mutates it.
	Describe("ChainID survives provider failover", func() {
		const expectedChainID = "chain-xyz-42"

		BeforeEach(func() {
			// Provider A: synchronously errors, triggering failover to B.
			registry.Register(&mockStreamProvider{
				name:     "providerA",
				streamFn: syncErrorStreamFn(errors.New("providerA unavailable")),
			})
			// Provider B: emits one delegation chunk with the test's
			// expectedChainID, then a Done marker.
			registry.Register(&mockStreamProvider{
				name: "providerB",
				streamFn: successStreamFn(
					provider.StreamChunk{
						DelegationInfo: &provider.DelegationInfo{
							SourceAgent:  "planner",
							TargetAgent:  "engineer",
							ChainID:      expectedChainID,
							Status:       "started",
							ProviderName: "providerB",
							ModelName:    "model-b",
						},
					},
					provider.StreamChunk{Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "providerA", Model: "model-a"},
				{Provider: "providerB", Model: "model-b"},
			})
		})

		It("keeps the same ChainID on the delegation chunk served from the failover provider", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(ch).NotTo(BeNil())

			var seenChainIDs []string
			for chunk := range ch {
				if chunk.DelegationInfo != nil {
					seenChainIDs = append(seenChainIDs, chunk.DelegationInfo.ChainID)
				}
				if chunk.Done {
					break
				}
			}

			Expect(seenChainIDs).To(HaveLen(1),
				"exactly one delegation chunk expected from providerB")
			Expect(seenChainIDs[0]).To(Equal(expectedChainID),
				"ChainID must survive provider failover unchanged; "+
					"regressions here indicate the failover path is "+
					"mutating FlowState-assigned identifiers.")
		})
	})

	// --- Contract 2: tool_use_id translates across providers (P14 FIXED) --
	//
	// P14 landed: a FlowState-internal ToolCallCorrelator (lives in
	// internal/streaming) assigns a stable InternalToolCallID to every
	// logical tool call and reuses it whenever the same logical call is
	// observed again — whether by the same provider on a later chunk or
	// by a different provider after a failover under its own id scheme.
	// The engine stamps this id on StreamChunk.InternalToolCallID on
	// the way to the consumer.
	//
	// Historical note (P5 pinned this as KNOWN ISSUE): before P14, the
	// raw provider-scoped ToolCallID on failover was disjoint across the
	// two providers' ID spaces, which broke downstream coalesce in the
	// activity pane and the Ctrl+E details modal. This test previously
	// asserted that disjointness as a regression gate against silent
	// reshaping. With P14 in place, the gate flips: the internal id
	// resolved via the correlator must be identical on both sides.
	//
	// The underlying provider-scoped ToolCallID is still disjoint —
	// that is intentional. The audit-trail contract keeps ToolCallID as
	// the native id the provider actually used; the correlator provides
	// the cross-provider identity on InternalToolCallID.
	Describe("tool_use_id translation across providers (P14 FIXED)", func() {
		It("resolves the call on provider A and the result on provider B to the same InternalToolCallID", func() {
			const callID = "A-tool-123"
			const resultID = "B-tool-456"
			const sessionID = "session-P14-failover"

			registry.Register(&mockStreamProvider{
				name: "providerA-tool",
				streamFn: successStreamFn(
					provider.StreamChunk{
						ToolCallID: callID,
						ToolCall: &provider.ToolCall{
							ID:        callID,
							Name:      "bash",
							Arguments: map[string]any{"cmd": "ls"},
						},
					},
				),
			})
			registry.Register(&mockStreamProvider{
				name: "providerB-tool",
				streamFn: successStreamFn(
					provider.StreamChunk{
						ToolCallID: resultID,
						ToolCall: &provider.ToolCall{
							ID:        resultID,
							Name:      "bash",
							Arguments: map[string]any{"cmd": "ls"},
						},
						ToolResult: &provider.ToolResultInfo{
							Content: "output",
						},
						Done: true,
					},
				),
			})

			// Drive both providers through the same correlator (the role
			// the engine plays in the real failover path — see engine.go
			// processStreamChunks + the tool_result emission site). The
			// correlator is session-scoped; the same sessionID on both
			// sides is what unlocks cross-provider translation.
			correlator := streaming.NewToolCallCorrelator()

			a, _ := registry.Get("providerA-tool")
			chA, _ := a.Stream(context.Background(), provider.ChatRequest{})
			b, _ := registry.Get("providerB-tool")
			chB, _ := b.Stream(context.Background(), provider.ChatRequest{})

			var internalA, internalB []string
			for chunk := range chA {
				if chunk.ToolCall == nil {
					continue
				}
				internalA = append(internalA, correlator.InternalID(
					sessionID, chunk.ToolCallID, chunk.ToolCall.Name, chunk.ToolCall.Arguments,
				))
			}
			for chunk := range chB {
				if chunk.ToolCall == nil {
					continue
				}
				internalB = append(internalB, correlator.InternalID(
					sessionID, chunk.ToolCallID, chunk.ToolCall.Name, chunk.ToolCall.Arguments,
				))
			}

			// The gate (post-P14): the correlator resolves both sides to
			// the same InternalToolCallID via fuzzy match on
			// (tool_name, args-fingerprint), even though the native
			// ToolCallID values remain disjoint. Regressions here point
			// to either the correlator's fuzzy path breaking or the
			// engine failing to consult it on the emission sites.
			Expect(internalA).To(HaveLen(1))
			Expect(internalB).To(HaveLen(1))
			Expect(internalA).To(Equal(internalB),
				"P14 contract: the ToolCallCorrelator must resolve a "+
					"call on provider A and the same logical call on "+
					"provider B (different native ids, same tool_name "+
					"and args) to the same InternalToolCallID. A "+
					"regression here breaks downstream cross-provider "+
					"coalesce on the activity pane and Ctrl+E modal.")
		})
	})
})
