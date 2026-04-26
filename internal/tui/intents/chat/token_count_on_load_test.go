package chat_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
)

// Regression: tokenCount used to be set only by finaliseStreamIfDone when
// a live stream completed. handleSessionLoaded swapped the engine's store
// out from under it but never repopulated tokenCount, so a freshly loaded
// session showed a stale count — typically 0 on a brand-new TUI launch.
//
// After the fix, handleSessionLoaded counts tokens off the restored
// messages immediately so the status bar reflects the loaded context's
// size before the next stream runs.
var _ = Describe("Token count on session load", Label("integration"), func() {
	BeforeEach(func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })
	})

	It("populates tokenCount from the restored session's messages on load", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "session-fresh",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  4096,
		})
		Expect(intent.TokenCount()).To(Equal(0),
			"sanity check: a brand-new intent has zero tokens accumulated")

		// Build a store carrying enough message text that any reasonable
		// token counter has to return >0 — but keep the fixture concrete
		// (no random/long-string magic) so the regression assertion is
		// easy to reason about.
		store := recall.NewEmptyContextStore("test-model")
		store.LoadFromSession([]recall.StoredMessage{
			{
				ID: "m1",
				Message: provider.Message{
					Role:    "user",
					Content: "Walk me through the deterministic planning loop end-to-end.",
				},
			},
			{
				ID: "m2",
				Message: provider.Message{
					Role:    "assistant",
					Content: "The planner orchestrates explorer, librarian, analyst, plan-writer, and plan-reviewer through the coordination_store. Each stage emits a verdict that gates the next step.",
				},
			},
			{
				ID: "m3",
				Message: provider.Message{
					Role: "assistant",
					ToolCalls: []provider.ToolCall{
						{
							ID:   "call-1",
							Name: "delegate",
							Arguments: map[string]any{
								"agent":   "explorer",
								"message": "Locate the deterministic planning loop driver in the harness package.",
							},
						},
					},
				},
			},
		}, nil, "test-model")

		intent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "session-loaded",
			Store:     store,
		})

		Expect(intent.TokenCount()).To(BeNumerically(">", 0),
			"after handleSessionLoaded the status-bar token count must reflect "+
				"the loaded session's content; the previous behaviour left it "+
				"at zero until the next stream completed")
	})

	It("returns zero when the loaded session is empty", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "session-fresh",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  4096,
		})

		intent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "session-empty",
			Store:     recall.NewEmptyContextStore("test-model"),
		})

		Expect(intent.TokenCount()).To(Equal(0),
			"an empty loaded session must not invent tokens out of nowhere")
	})

	It("clears the previous response token count on load", func() {
		intent := chat.NewIntent(chat.IntentConfig{
			AgentID:      "test-agent",
			SessionID:    "session-fresh",
			ProviderName: "test-provider",
			ModelName:    "test-model",
			TokenBudget:  4096,
		})

		// Simulate a prior in-flight response in the previous session.
		intent.HandleStreamChunkForTest(chat.StreamChunkMsg{Content: "stale partial output", Done: false})
		Expect(intent.ResponseTokenCountForTest()).To(BeNumerically(">", 0),
			"sanity check: response token count is non-zero after a chunk")

		intent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "session-loaded",
			Store:     recall.NewEmptyContextStore("test-model"),
		})

		Expect(intent.ResponseTokenCountForTest()).To(Equal(0),
			"loading a session must reset the in-flight response counter; "+
				"otherwise the next status-bar update double-counts the prior session's chunks")
	})
})
