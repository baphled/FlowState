package engine_test

// Coverage fix: the plain (non-delegated) engine turn-completion path
// must publish a turn_complete NotificationEvent so the SSE
// /api/v1/notifications/events stream covers every turn lifecycle —
// not only delegated ones (DelegateTool.publishNotification). Fires
// from completeResponse, the shared terminal seam in engine.go.

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("plain-engine turn_complete notification", func() {
	It("publishes a turn_complete notification when a non-delegated turn completes", func() {
		chatProvider := &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello"},
				{Content: " World", Done: true},
			},
		}
		bus := eventbus.NewEventBus()
		captured := make(chan *events.NotificationEvent, 1)
		bus.Subscribe(events.EventNotification, func(event any) {
			if notif, ok := event.(*events.NotificationEvent); ok {
				captured <- notif
			}
		})

		eng := engine.New(engine.Config{
			ChatProvider: chatProvider,
			Manifest: agent.Manifest{
				ID:   "test-agent",
				Name: "Test Agent",
			},
			EventBus: bus,
		})

		ctx := context.Background()
		chunks, err := eng.Stream(ctx, "test-agent", "Hello")
		Expect(err).NotTo(HaveOccurred())
		for range chunks {
			// drain to terminal Done
		}

		var notif *events.NotificationEvent
		Eventually(captured, 2*time.Second).Should(Receive(&notif))
		Expect(notif.Data.Type).To(Equal(events.NotificationTypeTurnComplete))
		Expect(notif.Data.Severity).To(Equal(events.NotificationSeverityInfo))
		Expect(notif.Data.Message).To(ContainSubstring("Turn complete"))
		Expect(notif.Data.ID).To(ContainSubstring("test-agent"))
	})
})
