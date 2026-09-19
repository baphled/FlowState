package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Engine session lifetime bound", func() {
	It("refuses further turns with an honest terminal once the turn budget is spent", func() {
		store, err := recall.NewFileContextStore(GinkgoT().TempDir()+"/ctx.json", "lifetime-model")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(store.Close)

		prov := &scriptedTodoProvider{name: "lifetime-prov", script: []todoProviderTurn{}}
		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     agent.Manifest{ID: "lifetime-agent", Name: "Lifetime Agent"},
			Tools:        []tool.Tool{},
			Store:        store,
			MaxSessionTurns: 2,
		})

		const sessionID = "lifetime-session"
		ctx := context.WithValue(context.Background(), session.IDKey{}, sessionID)

		first, streamErr := eng.Stream(ctx, sessionID, "Go")
		Expect(streamErr).NotTo(HaveOccurred())
		for range first {
		}
		Expect(eng.SessionLifetimeExceeded(sessionID)).To(BeFalse(),
			"the bound must not trip before the configured turn count is spent")

		second, streamErr := eng.Stream(ctx, sessionID, "Go")
		Expect(streamErr).NotTo(HaveOccurred())
		for range second {
		}
		Expect(eng.SessionLifetimeExceeded(sessionID)).To(BeTrue(),
			"once the turn budget is fully spent the session is flagged")

		refused, streamErr := eng.Stream(ctx, sessionID, "Go")
		Expect(streamErr).NotTo(HaveOccurred(),
			"the refusal must flow through the stream, not fail the Stream call")

		var sawError, sawDone bool
		var terminalError string
		for c := range refused {
			if c.Error != nil {
				sawError = true
				terminalError = c.Error.Error()
			}
			if c.Done {
				sawDone = true
			}
		}
		Expect(sawError).To(BeTrue(),
			"the refused turn must surface an error terminal naming the exhausted budget")
		Expect(sawDone).To(BeTrue(), "the refused turn must still close with a Done chunk")
		Expect(terminalError).To(ContainSubstring("max_session_turns"))
		Expect(eng.SessionLifetimeExceeded(sessionID)).To(BeTrue(),
			"the engine must flag the session so the completion orchestrator can respect the bound")

		stored := store.GetStoredMessages()
		Expect(stored).NotTo(BeEmpty())
		last := stored[len(stored)-1].Message
		Expect(last.Role).To(Equal("assistant"),
			"the refused turn must persist a terminal assistant message")
		Expect(last.Content).To(ContainSubstring("max_session_turns"),
			"the terminal message content must name the exhausted budget")
	})
})
