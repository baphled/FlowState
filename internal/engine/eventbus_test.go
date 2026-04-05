package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("EventBus Integration", func() {
	var (
		chatProvider *mockProvider
		manifest     agent.Manifest
	)

	BeforeEach(func() {
		chatProvider = &mockProvider{
			name: "test-chat-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Hello", Done: true},
			},
		}
		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a helpful assistant.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}
	})

	Describe("EventBus accessor", func() {
		It("returns a non-nil EventBus created at construction", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			Expect(eng.EventBus()).NotTo(BeNil())
		})
		It("returns the same EventBus instance on repeated calls", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			Expect(eng.EventBus()).To(BeIdenticalTo(eng.EventBus()))
		})
	})

	Describe("Config.EventBus injection", func() {
		Context("when Config.EventBus is provided", func() {
			It("uses the provided EventBus instead of creating a new one", func() {
				sharedBus := eventbus.NewEventBus()
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					EventBus:     sharedBus,
				})
				Expect(eng.EventBus()).To(BeIdenticalTo(sharedBus))
			})

			It("delivers events to subscribers on the provided bus", func() {
				sharedBus := eventbus.NewEventBus()
				var mu sync.Mutex
				var received []events.SessionEventData
				sharedBus.Subscribe("session.created", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
					EventBus:     sharedBus,
				})
				store := newTempFileContextStore()
				DeferCleanup(func() { cleanupStore(store) })
				eng.SetContextStore(store, "shared-session")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(HaveLen(1))
				Expect(received[0].SessionID).To(Equal("shared-session"))
			})
		})

		Context("when Config.EventBus is nil", func() {
			It("creates a new EventBus as fallback", func() {
				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     manifest,
				})
				Expect(eng.EventBus()).NotTo(BeNil())
			})
		})
	})

	Describe("session events", func() {
		Context("when SetContextStore is called with a non-nil store", func() {
			It("publishes a session.created event", func() {
				eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
				var mu sync.Mutex
				var received []events.SessionEventData
				eng.EventBus().Subscribe("session.created", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				store := newTempFileContextStore()
				DeferCleanup(func() { cleanupStore(store) })
				eng.SetContextStore(store, "test-session")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(HaveLen(1))
				Expect(received[0].Action).To(Equal("created"))
			})
		})
		Context("when SetContextStore is called with nil", func() {
			It("publishes a session.ended event", func() {
				eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
				var mu sync.Mutex
				var received []events.SessionEventData
				eng.EventBus().Subscribe("session.ended", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				store := newTempFileContextStore()
				DeferCleanup(func() { cleanupStore(store) })
				eng.SetContextStore(store, "test-session")
				eng.SetContextStore(nil, "")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(HaveLen(1))
				Expect(received[0].Action).To(Equal("ended"))
			})
		})
		Context("when SetContextStore is called with nil but no previous store", func() {
			It("does not publish a session.ended event", func() {
				eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
				var mu sync.Mutex
				var received []events.SessionEventData
				eng.EventBus().Subscribe("session.ended", func(event any) {
					if se, ok := event.(*events.SessionEvent); ok {
						mu.Lock()
						received = append(received, se.Data)
						mu.Unlock()
					}
				})
				eng.SetContextStore(nil, "")
				mu.Lock()
				defer mu.Unlock()
				Expect(received).To(BeEmpty())
			})
		})
	})

	Describe("tool events", func() {
		var (
			testTool     *executableMockTool
			seqProvider  *streamSequenceProvider
			toolManifest agent.Manifest
		)
		BeforeEach(func() {
			testTool = &executableMockTool{name: "test_tool", description: "A test tool", execResult: tool.Result{Output: "tool output"}}
			seqProvider = &streamSequenceProvider{
				name: "test-provider",
				sequences: [][]provider.StreamChunk{
					{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_evt_test", Name: "test_tool", Arguments: map[string]interface{}{"key": "value"}}}},
					{{Content: "Done.", Done: true}},
				},
			}
			toolManifest = agent.Manifest{ID: "test-agent", Name: "Test Agent", Instructions: agent.Instructions{SystemPrompt: "You are a helpful assistant."}, ContextManagement: agent.DefaultContextManagement()}
		})

		It("publishes tool.execute.before before tool execution", func() {
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var beforeEvents []*events.ToolEvent
			eng.EventBus().Subscribe("tool.execute.before", func(event any) {
				if te, ok := event.(*events.ToolEvent); ok {
					mu.Lock()
					beforeEvents = append(beforeEvents, te)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(beforeEvents).To(HaveLen(1))
			Expect(beforeEvents[0].Data.ToolName).To(Equal("test_tool"))
			Expect(beforeEvents[0].Data.Args).To(HaveKeyWithValue("key", "value"))
		})

		It("publishes tool.execute.after after tool execution", func() {
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var afterEvents []*events.ToolEvent
			eng.EventBus().Subscribe("tool.execute.after", func(event any) {
				if te, ok := event.(*events.ToolEvent); ok {
					mu.Lock()
					afterEvents = append(afterEvents, te)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(afterEvents).To(HaveLen(1))
			Expect(afterEvents[0].Data.ToolName).To(Equal("test_tool"))
			Expect(afterEvents[0].Data.Result).To(Equal("tool output"))
		})

		It("publishes tool.execute.after with error when tool fails", func() {
			testTool.execErr = errors.New("tool failed")
			seqProvider.sequences = [][]provider.StreamChunk{
				{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_fail", Name: "test_tool", Arguments: map[string]interface{}{}}}},
				{{Content: "Tool failed.", Done: true}},
			}
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var afterEvents []*events.ToolEvent
			eng.EventBus().Subscribe("tool.execute.after", func(event any) {
				if te, ok := event.(*events.ToolEvent); ok {
					mu.Lock()
					afterEvents = append(afterEvents, te)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(afterEvents).To(HaveLen(1))
			Expect(afterEvents[0].Data.Error).To(MatchError("tool failed"))
		})

		It("publishes before event prior to after event", func() {
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var order []string
			eng.EventBus().Subscribe("tool.execute.before", func(_ any) { mu.Lock(); order = append(order, "before"); mu.Unlock() })
			eng.EventBus().Subscribe("tool.execute.after", func(_ any) { mu.Lock(); order = append(order, "after"); mu.Unlock() })
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(order).To(Equal([]string{"before", "after"}))
		})
	})

	Describe("provider error events", func() {
		It("publishes provider.error when provider stream fails", func() {
			chatProvider.streamErr = errors.New("provider unavailable")
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			var mu sync.Mutex
			var providerErrors []*events.ProviderErrorEvent
			eng.EventBus().Subscribe("provider.error", func(event any) {
				if pe, ok := event.(*events.ProviderErrorEvent); ok {
					mu.Lock()
					providerErrors = append(providerErrors, pe)
					mu.Unlock()
				}
			})
			_, err := eng.Stream(context.Background(), "test-agent", "Hello")
			Expect(err).To(HaveOccurred())
			mu.Lock()
			defer mu.Unlock()
			Expect(providerErrors).To(HaveLen(1))
			Expect(providerErrors[0].Data.ProviderName).To(Equal("test-chat-provider"))
			Expect(providerErrors[0].Data.Error).To(MatchError("provider unavailable"))
		})

		It("publishes provider.error when provider fails during tool loop", func() {
			fp := &failOnSecondCallProvider{
				name: "fail-on-second",
				firstChunks: []provider.StreamChunk{
					{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_err", Name: "test_tool", Arguments: map[string]interface{}{}}},
				},
				secondErr: errors.New("provider down on retry"),
			}
			tt := &executableMockTool{name: "test_tool", description: "A test tool", execResult: tool.Result{Output: "ok"}}
			eng := engine.New(engine.Config{ChatProvider: fp, Manifest: manifest, Tools: []tool.Tool{tt}})
			var mu sync.Mutex
			var providerErrors []*events.ProviderErrorEvent
			eng.EventBus().Subscribe("provider.error", func(event any) {
				if pe, ok := event.(*events.ProviderErrorEvent); ok {
					mu.Lock()
					providerErrors = append(providerErrors, pe)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(providerErrors).To(HaveLen(1))
			Expect(providerErrors[0].Data.Error).To(MatchError("provider down on retry"))
		})
	})

	Describe("provider request events", func() {
		It("publishes provider.request before the initial provider stream call", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			var mu sync.Mutex
			var requestEvents []*events.ProviderRequestEvent
			eng.EventBus().Subscribe("provider.request", func(event any) {
				if pe, ok := event.(*events.ProviderRequestEvent); ok {
					mu.Lock()
					requestEvents = append(requestEvents, pe)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(requestEvents).To(HaveLen(1))
			Expect(requestEvents[0].EventType()).To(Equal("provider.request"))
			Expect(requestEvents[0].Data.AgentID).To(Equal("test-agent"))
			Expect(requestEvents[0].Data.ProviderName).To(Equal("test-chat-provider"))
			Expect(requestEvents[0].Data.Request.Messages).NotTo(BeEmpty())
			Expect(requestEvents[0].Data.Request.Provider).To(Equal("test-chat-provider"))
		})

		It("publishes provider.request before each tool loop retry", func() {
			testTool := &executableMockTool{name: "test_tool", description: "A test tool", execResult: tool.Result{Output: "tool output"}}
			seqProvider := &streamSequenceProvider{
				name: "test-provider",
				sequences: [][]provider.StreamChunk{
					{{EventType: "tool_call", ToolCall: &provider.ToolCall{ID: "call_req_test", Name: "test_tool", Arguments: map[string]interface{}{"key": "value"}}}},
					{{Content: "Done.", Done: true}},
				},
			}
			toolManifest := agent.Manifest{ID: "test-agent", Name: "Test Agent", Instructions: agent.Instructions{SystemPrompt: "You are a helpful assistant."}, ContextManagement: agent.DefaultContextManagement()}
			eng := engine.New(engine.Config{ChatProvider: seqProvider, Manifest: toolManifest, Tools: []tool.Tool{testTool}})
			var mu sync.Mutex
			var requestEvents []*events.ProviderRequestEvent
			eng.EventBus().Subscribe("provider.request", func(event any) {
				if pe, ok := event.(*events.ProviderRequestEvent); ok {
					mu.Lock()
					requestEvents = append(requestEvents, pe)
					mu.Unlock()
				}
			})
			chunks, err := eng.Stream(context.Background(), "test-agent", "Use the tool")
			Expect(err).NotTo(HaveOccurred())
			for range chunks { //nolint:revive // drain channel
			}
			mu.Lock()
			defer mu.Unlock()
			Expect(requestEvents).To(HaveLen(2))
			Expect(requestEvents[0].Data.AgentID).To(Equal("test-agent"))
			Expect(requestEvents[1].Data.AgentID).To(Equal("test-agent"))
			Expect(requestEvents[1].Data.Request.Messages).NotTo(BeEmpty())
		})
	})

	Describe("agent.switched events", func() {
		Context("when Stream triggers a manifest switch", func() {
			It("includes the session ID from context in the agent.switched event", func() {
				initialManifest := agent.Manifest{
					ID:   "initial-agent",
					Name: "Initial Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the initial agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				switchedManifest := agent.Manifest{
					ID:   "switched-agent",
					Name: "Switched Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the switched agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				registry := agent.NewRegistry()
				registry.Register(&switchedManifest)

				eng := engine.New(engine.Config{
					ChatProvider:  chatProvider,
					Manifest:      initialManifest,
					AgentRegistry: registry,
				})

				var mu sync.Mutex
				var switchedEvents []*events.AgentSwitchedEvent
				eng.EventBus().Subscribe(events.EventAgentSwitched, func(event any) {
					if se, ok := event.(*events.AgentSwitchedEvent); ok {
						mu.Lock()
						switchedEvents = append(switchedEvents, se)
						mu.Unlock()
					}
				})

				ctx := context.WithValue(context.Background(), session.IDKey{}, "test-session-id")
				chunks, err := eng.Stream(ctx, "switched-agent", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}

				mu.Lock()
				defer mu.Unlock()
				Expect(switchedEvents).To(HaveLen(1))
				Expect(switchedEvents[0].Data.SessionID).To(Equal("test-session-id"))
				Expect(switchedEvents[0].Data.FromAgent).To(Equal("initial-agent"))
				Expect(switchedEvents[0].Data.ToAgent).To(Equal("switched-agent"))
			})
		})

		Context("when SetManifest is called after Stream has established a session", func() {
			It("includes the last known session ID in the agent.switched event", func() {
				initialManifest := agent.Manifest{
					ID:   "base-agent",
					Name: "Base Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the base agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				newManifest := agent.Manifest{
					ID:   "new-agent",
					Name: "New Agent",
					Instructions: agent.Instructions{
						SystemPrompt: "You are the new agent.",
					},
					ContextManagement: agent.DefaultContextManagement(),
				}

				eng := engine.New(engine.Config{
					ChatProvider: chatProvider,
					Manifest:     initialManifest,
				})

				var mu sync.Mutex
				var switchedEvents []*events.AgentSwitchedEvent
				eng.EventBus().Subscribe(events.EventAgentSwitched, func(event any) {
					if se, ok := event.(*events.AgentSwitchedEvent); ok {
						mu.Lock()
						switchedEvents = append(switchedEvents, se)
						mu.Unlock()
					}
				})

				ctx := context.WithValue(context.Background(), session.IDKey{}, "established-session-id")
				chunks, err := eng.Stream(ctx, "", "Hello")
				Expect(err).NotTo(HaveOccurred())
				for range chunks { //nolint:revive // drain channel
				}

				eng.SetManifest(newManifest)

				mu.Lock()
				defer mu.Unlock()
				Expect(switchedEvents).To(HaveLen(1))
				Expect(switchedEvents[0].Data.SessionID).To(Equal("established-session-id"))
				Expect(switchedEvents[0].Data.FromAgent).To(Equal("base-agent"))
				Expect(switchedEvents[0].Data.ToAgent).To(Equal("new-agent"))
			})
		})
	})

	Describe("EventBus does not affect existing hook chain", func() {
		It("does not interfere with normal stream operation", func() {
			eng := engine.New(engine.Config{ChatProvider: chatProvider, Manifest: manifest})
			Expect(eng.EventBus()).NotTo(BeNil())
			chunks, err := eng.Stream(context.Background(), "test-agent", "Hello")
			Expect(err).NotTo(HaveOccurred())
			var received []provider.StreamChunk
			for chunk := range chunks {
				received = append(received, chunk)
			}
			Expect(received).To(HaveLen(1))
			Expect(received[0].Content).To(Equal("Hello"))
		})
	})
})

func newTempFileContextStore() *recall.FileContextStore {
	tmpDir, err := os.MkdirTemp("", "engine-eventbus-test-*")
	Expect(err).NotTo(HaveOccurred())
	storePath := filepath.Join(tmpDir, "context.json")
	store, err := recall.NewFileContextStore(storePath, "test-model")
	Expect(err).NotTo(HaveOccurred())
	return store
}

func cleanupStore(_ *recall.FileContextStore) {}

type failOnSecondCallProvider struct {
	name        string
	firstChunks []provider.StreamChunk
	secondErr   error
	callCount   int
}

func (p *failOnSecondCallProvider) Name() string { return p.name }
func (p *failOnSecondCallProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.callCount++
	if p.callCount > 1 {
		return nil, p.secondErr
	}
	ch := make(chan provider.StreamChunk, len(p.firstChunks))
	go func() {
		defer close(ch)
		for i := range p.firstChunks {
			ch <- p.firstChunks[i]
		}
	}()
	return ch, nil
}
func (p *failOnSecondCallProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *failOnSecondCallProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (p *failOnSecondCallProvider) Models() ([]provider.Model, error) { return nil, nil }
