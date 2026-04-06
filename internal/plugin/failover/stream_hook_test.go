package failover_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

type mockStreamProvider struct {
	name     string
	streamFn func(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error)
}

func (m *mockStreamProvider) Name() string { return m.name }

func (m *mockStreamProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return m.streamFn(ctx, req)
}

func (m *mockStreamProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, errMockNotImplemented
}

func (m *mockStreamProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, errMockNotImplemented
}

func (m *mockStreamProvider) Models() ([]provider.Model, error) {
	return nil, errMockNotImplemented
}

func successStreamFn(chunks ...provider.StreamChunk) func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk, len(chunks))
		go func() {
			defer close(ch)
			for i := range chunks {
				ch <- chunks[i]
			}
		}()
		return ch, nil
	}
}

func syncErrorStreamFn(err error) func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		return nil, err
	}
}

func asyncErrorStreamFn(err error) func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk, 1)
		go func() {
			defer close(ch)
			ch <- provider.StreamChunk{Error: err, Done: true}
		}()
		return ch, nil
	}
}

func closedChannelStreamFn() func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	return func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		ch := make(chan provider.StreamChunk)
		close(ch)
		return ch, nil
	}
}

func baseHandler(registry *provider.Registry) hook.HandlerFunc {
	return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		if req.Provider == "" {
			return nil, fmt.Errorf("no provider specified in request")
		}
		p, err := registry.Get(req.Provider)
		if err != nil {
			return nil, fmt.Errorf("provider %q not found: %w", req.Provider, err)
		}
		return p.Stream(ctx, *req)
	}
}

var _ = Describe("StreamHook", func() {
	var (
		manager  *failover.Manager
		registry *provider.Registry
		health   *failover.HealthManager
		sh       *failover.StreamHook
		timeout  time.Duration
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		timeout = 2 * time.Second
		manager = failover.NewManager(registry, health, timeout)
		sh = failover.NewStreamHook(manager, nil, "")
	})

	Describe("Execute", func() {
		Context("when first provider succeeds", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name: "anthropic",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Hello"},
						provider.StreamChunk{Content: " World", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("returns the stream from that provider", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				Expect(ch).NotTo(BeNil())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(2))
				Expect(chunks[0].Content).To(Equal("Hello"))
				Expect(chunks[1].Content).To(Equal(" World"))
				Expect(chunks[1].Done).To(BeTrue())
			})

			It("calls SetLast on the manager", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}
				Expect(manager.LastProvider()).To(Equal("anthropic"))
				Expect(manager.LastModel()).To(Equal("claude-3"))
			})
		})

		Context("when first provider returns sync error and second succeeds", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(errors.New("auth failed")),
				})
				registry.Register(&mockStreamProvider{
					name: "ollama",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Fallback response", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "ollama", Model: "llama3.2"},
				})
			})

			It("falls back to the second provider", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("Fallback response"))
			})

			It("records the fallback provider as last", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}
				Expect(manager.LastProvider()).To(Equal("ollama"))
				Expect(manager.LastModel()).To(Equal("llama3.2"))
			})
		})

		Context("when first provider returns async error and second succeeds", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: asyncErrorStreamFn(errors.New("model not found")),
				})
				registry.Register(&mockStreamProvider{
					name: "ollama",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Async fallback", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "ollama", Model: "llama3.2"},
				})
			})

			It("detects the async error and falls back", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("Async fallback"))
			})
		})

		Context("when first provider channel closes immediately and second succeeds", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: closedChannelStreamFn(),
				})
				registry.Register(&mockStreamProvider{
					name: "ollama",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Closed fallback", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "ollama", Model: "llama3.2"},
				})
			})

			It("detects immediate close and falls back", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("Closed fallback"))
			})
		})

		Context("when all providers fail", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(errors.New("anthropic down")),
				})
				registry.Register(&mockStreamProvider{
					name:     "ollama",
					streamFn: syncErrorStreamFn(errors.New("ollama down")),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "ollama", Model: "llama3.2"},
				})
			})

			It("returns the last error", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("all providers failed"))
			})
		})

		Context("when first provider returns sync rate-limit error", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(errors.New("rate_limit: quota exceeded")),
				})
				registry.Register(&mockStreamProvider{
					name: "ollama",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Fallback response", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "ollama", Model: "llama3.2"},
				})
			})

			It("marks the failed provider as rate-limited in health", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
			})

			It("falls back to the next provider", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("Fallback response"))
			})
		})

		Context("when first provider returns async rate-limit error", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: asyncErrorStreamFn(errors.New("rate_limit: too many requests")),
				})
				registry.Register(&mockStreamProvider{
					name: "ollama",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Async fallback", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "ollama", Model: "llama3.2"},
				})
			})

			It("marks the failed provider as rate-limited in health", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
			})

			It("falls back to the next provider", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("Async fallback"))
			})
		})

		Context("when no candidates are available", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(errors.New("should not be called")),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
				health.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
			})

			It("returns an error about no healthy providers", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no healthy providers available"))
			})
		})

		Context("when replay channel delivers first chunk then remaining", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name: "anthropic",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "First"},
						provider.StreamChunk{Content: "Second"},
						provider.StreamChunk{Content: "Third", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("replays the first chunk and forwards remaining chunks in order", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var contents []string
				for chunk := range ch {
					contents = append(contents, chunk.Content)
				}
				Expect(contents).To(Equal([]string{"First", "Second", "Third"}))
			})
		})

		Context("when per-attempt timeout is applied", func() {
			BeforeEach(func() {
				shortTimeout := 50 * time.Millisecond
				manager = failover.NewManager(registry, health, shortTimeout)
				sh = failover.NewStreamHook(manager, nil, "")

				registry.Register(&mockStreamProvider{
					name: "slow",
					streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						ch := make(chan provider.StreamChunk)
						return ch, nil
					},
				})
				registry.Register(&mockStreamProvider{
					name: "fast",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Fast response", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "slow", Model: "slow-model"},
					{Provider: "fast", Model: "fast-model"},
				})
			})

			It("times out the slow provider and falls back to fast", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}
				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("Fast response"))
				Expect(manager.LastProvider()).To(Equal("fast"))
			})
		})

		Context("when request fields are set for each candidate", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name: "anthropic",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Response", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("sets provider and model on the request", func() {
				var capturedProvider, capturedModel string
				captureHandler := func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					capturedProvider = req.Provider
					capturedModel = req.Model
					p, _ := registry.Get(req.Provider)
					return p.Stream(context.Background(), *req)
				}

				handler := sh.Execute(captureHandler)
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				Expect(capturedProvider).To(Equal("anthropic"))
				Expect(capturedModel).To(Equal("claude-3"))
			})
		})

		Context("when event bus is configured and a candidate fails synchronously", func() {
			var (
				bus      *eventbus.EventBus
				captured []any
				mu       sync.Mutex
			)

			BeforeEach(func() {
				bus = eventbus.NewEventBus()
				captured = nil
				bus.Subscribe("provider.error", func(event any) {
					mu.Lock()
					defer mu.Unlock()
					captured = append(captured, event)
				})
				sh = failover.NewStreamHook(manager, bus, "test-agent")

				registry.Register(&mockStreamProvider{
					name: "failing",
					streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						return nil, errMockNotImplemented
					},
				})
				registry.Register(&mockStreamProvider{
					name: "backup",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "OK", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "failing", Model: "fail-model"},
					{Provider: "backup", Model: "backup-model"},
				})
			})

			It("publishes a provider error event with failover phase", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				mu.Lock()
				defer mu.Unlock()
				Expect(captured).To(HaveLen(1))
				evt, ok := captured[0].(*events.ProviderErrorEvent)
				Expect(ok).To(BeTrue())
				Expect(evt.Data.Phase).To(Equal("failover"))
				Expect(evt.Data.ProviderName).To(Equal("failing"))
				Expect(evt.Data.ModelName).To(Equal("fail-model"))
				Expect(evt.Data.AgentID).To(Equal("test-agent"))
				Expect(evt.Data.Error).To(MatchError(ContainSubstring("not implemented")))
			})
		})

		Context("when event bus is configured and a candidate stream closes immediately", func() {
			var (
				bus      *eventbus.EventBus
				captured []any
				mu       sync.Mutex
			)

			BeforeEach(func() {
				bus = eventbus.NewEventBus()
				captured = nil
				bus.Subscribe("provider.error", func(event any) {
					mu.Lock()
					defer mu.Unlock()
					captured = append(captured, event)
				})
				sh = failover.NewStreamHook(manager, bus, "test-agent")

				registry.Register(&mockStreamProvider{
					name: "empty-stream",
					streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						ch := make(chan provider.StreamChunk)
						close(ch)
						return ch, nil
					},
				})
				registry.Register(&mockStreamProvider{
					name: "fallback",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "Fallback", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "empty-stream", Model: "empty-model"},
					{Provider: "fallback", Model: "fallback-model"},
				})
			})

			It("publishes a provider error event for stream closed immediately", func() {
				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				mu.Lock()
				defer mu.Unlock()
				Expect(captured).To(HaveLen(1))
				evt, ok := captured[0].(*events.ProviderErrorEvent)
				Expect(ok).To(BeTrue())
				Expect(evt.Data.Phase).To(Equal("failover"))
				Expect(evt.Data.ProviderName).To(Equal("empty-stream"))
				Expect(evt.Data.Error).To(MatchError(ContainSubstring("stream closed immediately")))
			})
		})

		Context("child session failover with fresh FailoverManager", func() {
			It("publishes failover events with correct SessionID from context", func() {
				// Child sessions get their own fresh FailoverManager to avoid state coupling
				bus := eventbus.NewEventBus()
				var captured []any
				var mu sync.Mutex

				bus.Subscribe(events.EventProviderError, func(msg any) {
					mu.Lock()
					defer mu.Unlock()
					captured = append(captured, msg)
				})

				// Simulate child session with its own manager
				childManager := failover.NewManager(registry, health, 5*time.Minute)
				childManager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "failing", Model: "child-model"},
					{Provider: "backup", Model: "backup-model"},
				})

				childSessionID := "child-session-123"
				ctx := context.WithValue(context.Background(), session.IDKey{}, childSessionID)

				sh := failover.NewStreamHook(childManager, bus, "child-agent")

				registry.Register(&mockStreamProvider{
					name: "failing",
					streamFn: func(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						return nil, fmt.Errorf("child session failover test")
					},
				})
				registry.Register(&mockStreamProvider{
					name: "backup",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "OK", Done: true},
					),
				})

				handler := sh.Execute(baseHandler(registry))
				ch, err := handler(ctx, &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				mu.Lock()
				defer mu.Unlock()
				Expect(captured).To(HaveLen(1))
				evt, ok := captured[0].(*events.ProviderErrorEvent)
				Expect(ok).To(BeTrue())
				Expect(evt.Data.SessionID).To(Equal(childSessionID))
				Expect(evt.Data.AgentID).To(Equal("child-agent"))
				Expect(evt.Data.Phase).To(Equal("failover"))
			})
		})
	})

	Describe("differentiated error handling", func() {
		var (
			health   *failover.HealthManager
			manager  *failover.Manager
			registry *provider.Registry
			sh       *failover.StreamHook
		)

		BeforeEach(func() {
			registry = provider.NewRegistry()
			health = failover.NewHealthManager()
			manager = failover.NewManager(registry, health, 2*time.Second)
			sh = failover.NewStreamHook(manager, nil, "")
		})

		Context("when sync error is a provider.Error with billing type", func() {
			BeforeEach(func() {
				billingErr := &provider.Error{
					ErrorType: provider.ErrorTypeBilling,
					Provider:  "zai",
					Message:   "insufficient balance",
				}
				registry.Register(&mockStreamProvider{
					name:     "zai",
					streamFn: syncErrorStreamFn(billingErr),
				})
				registry.Register(&mockStreamProvider{
					name: "backup",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "OK", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "zai", Model: "glm-5"},
					{Provider: "backup", Model: "backup-model"},
				})
			})

			It("marks the provider as unavailable", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
			})
		})

		Context("when sync error is a provider.Error with rate_limit type", func() {
			BeforeEach(func() {
				rateLimitErr := &provider.Error{
					ErrorType: provider.ErrorTypeRateLimit,
					Provider:  "anthropic",
					Message:   "rate limited",
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(rateLimitErr),
				})
				registry.Register(&mockStreamProvider{
					name: "backup",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "OK", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "backup", Model: "backup-model"},
				})
			})

			It("marks the provider as unavailable", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
			})
		})

		Context("when sync error is a plain string containing rate_limit keyword", func() {
			BeforeEach(func() {
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(errors.New("rate_limit exceeded")),
				})
				registry.Register(&mockStreamProvider{
					name: "backup",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "OK", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
					{Provider: "backup", Model: "backup-model"},
				})
			})

			It("still marks the provider via legacy detection", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())

				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
			})
		})
	})
})
