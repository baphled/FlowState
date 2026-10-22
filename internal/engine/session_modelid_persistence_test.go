package engine_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
)

// modelIDTestManifest returns a manifest used by ModelID-persistence tests.
func modelIDTestManifest() agent.Manifest {
	return agent.Manifest{
		ID:   "model-id-agent",
		Name: "Model ID Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful assistant.",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}
}

// drainChunks blocks until the channel closes, discarding the chunks. Tests
// only care about what the engine persists, not the chunks themselves.
func drainChunks(ch <-chan provider.StreamChunk) {
	for range ch {
	}
}

// firstAssistantMessage returns the first stored message with Role == "assistant".
// Fails the spec when no assistant message is present.
func firstAssistantMessage(messages []provider.Message) provider.Message {
	for _, m := range messages {
		if m.Role == "assistant" {
			return m
		}
	}
	Fail("no assistant message persisted")
	return provider.Message{}
}

// firstMessageByRole returns the first stored message matching the given role.
// Returns the zero value when none is found.
func firstMessageByRole(messages []provider.Message, role string) provider.Message {
	for _, m := range messages {
		if m.Role == role {
			return m
		}
	}
	return provider.Message{}
}

var _ = Describe("Session ModelID persistence", func() {
	Context("when a single user→assistant turn streams to completion", func() {
		It("stamps the assistant message with the engine's last-used model", func() {
			chat := &mockProvider{
				name: "model-id-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "hello", Done: true},
				},
			}
			store := recall.NewEmptyContextStore("test-model")
			eng := engine.New(engine.Config{
				ChatProvider: chat,
				Manifest:     modelIDTestManifest(),
				Store:        store,
			})
			eng.SetModelPreference("model-id-provider", "claude-sonnet-4-6")

			ch, err := eng.Stream(context.Background(), "model-id-agent", "hi")
			Expect(err).NotTo(HaveOccurred())
			drainChunks(ch)

			messages := store.AllMessages()
			assistant := firstAssistantMessage(messages)
			Expect(assistant.ModelID).To(Equal("claude-sonnet-4-6"))
		})

		It("leaves user messages with an empty ModelID", func() {
			chat := &mockProvider{
				name: "model-id-provider",
				streamChunks: []provider.StreamChunk{
					{Content: "hello", Done: true},
				},
			}
			store := recall.NewEmptyContextStore("test-model")
			eng := engine.New(engine.Config{
				ChatProvider: chat,
				Manifest:     modelIDTestManifest(),
				Store:        store,
			})
			eng.SetModelPreference("model-id-provider", "claude-sonnet-4-6")

			ch, err := eng.Stream(context.Background(), "model-id-agent", "hi")
			Expect(err).NotTo(HaveOccurred())
			drainChunks(ch)

			user := firstMessageByRole(store.AllMessages(), "user")
			Expect(user.Role).To(Equal("user"))
			Expect(user.ModelID).To(BeEmpty())
		})
	})
})
