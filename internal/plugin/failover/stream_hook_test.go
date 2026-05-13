package failover_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

// stripTransitionChunks removes provider_changed and model_active observability
// chunks from a slice of stream chunks so existing fallback assertions can keep
// focusing on user-visible content.
//
// Two metadata chunks are emitted by the failover hook:
//   - "provider_changed" — prepended only when a previous candidate failed and
//     a fallback succeeded. Carries from/to/reason as JSON in chunk.Content.
//     Frontend renders a toast announcing the switch.
//   - "model_active" — prepended on EVERY successful stream, carrying the
//     actual (provider, model) the failover hook chose. Lets the chip pivot
//     from the user's selection to the actual running model the moment
//     streaming starts — addresses the "chip shows what was selected, not
//     what actually ran" reported by the user (May 2026).
//
// Pre-existing tests assert content shapes ("Fallback content", chunk count
// == 1, etc.) and shouldn't have to know about the metadata wrappers.
func stripTransitionChunks(chunks []provider.StreamChunk) []provider.StreamChunk {
	filtered := make([]provider.StreamChunk, 0, len(chunks))
	for _, c := range chunks {
		if c.EventType == "provider_changed" || c.EventType == "model_active" {
			continue
		}
		filtered = append(filtered, c)
	}
	return filtered
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
				chunks = stripTransitionChunks(chunks)
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
				chunks = stripTransitionChunks(chunks)
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
				chunks = stripTransitionChunks(chunks)
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
				chunks = stripTransitionChunks(chunks)
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
				chunks = stripTransitionChunks(chunks)
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
				chunks = stripTransitionChunks(chunks)
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

				var raw []provider.StreamChunk
				for chunk := range ch {
					raw = append(raw, chunk)
				}
				var contents []string
				for _, c := range stripTransitionChunks(raw) {
					contents = append(contents, c.Content)
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
				chunks = stripTransitionChunks(chunks)
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

		// Caller-pin respect — Bug: Failover Stream Hook Ignores Caller
		// Provider Pin (April 2026).
		//
		// retryStreamForToolResult and other in-turn retries pin the
		// same-session provider on the ChatRequest to keep multi-turn
		// agent sessions consistent. Before the fix, StreamHook.Execute
		// iterated its own Candidates() list and unconditionally
		// overwrote req.Provider/req.Model, so the second turn of a
		// tool-use round-trip silently switched providers whenever the
		// candidate ordering disagreed with the pin. The effect was a
		// mid-conversation flip from e.g. anthropic/claude-sonnet-4 to
		// ollama/llama3.2, producing off-role garbage for the rest of
		// the session.
		//
		// Contract: when req.Provider is set and matches a healthy
		// candidate, that candidate is the first one attempted, even
		// if it is not first in the effective preferences. Failover
		// to the remaining candidates still applies if the pinned one
		// genuinely fails — the pin is a priority hint, not a hard
		// single-shot.
		Context("when the caller pins req.Provider mid-session", func() {
			Context("and the pinned candidate is not first in the preference list", func() {
				var attempted []string
				var attemptedMu sync.Mutex

				BeforeEach(func() {
					attempted = nil
					registry.Register(&mockStreamProvider{
						name: "ollama",
						streamFn: successStreamFn(
							provider.StreamChunk{Content: "ollama-reply", Done: true},
						),
					})
					registry.Register(&mockStreamProvider{
						name: "anthropic",
						streamFn: successStreamFn(
							provider.StreamChunk{Content: "anthropic-reply", Done: true},
						),
					})
					// Preference order puts ollama first; a naive
					// loop would pick ollama even when the caller
					// has explicitly pinned anthropic for
					// continuity.
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "ollama", Model: "llama3.2"},
						{Provider: "anthropic", Model: "claude-sonnet-4"},
					})
				})

				It("attempts the pinned provider first and does not silently fall through", func() {
					recordingHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						attemptedMu.Lock()
						attempted = append(attempted, req.Provider)
						attemptedMu.Unlock()
						p, err := registry.Get(req.Provider)
						if err != nil {
							return nil, err
						}
						return p.Stream(ctx, *req)
					}

					handler := sh.Execute(recordingHandler)
					pinnedReq := &provider.ChatRequest{
						Provider: "anthropic",
						Model:    "claude-sonnet-4",
					}
					ch, err := handler(context.Background(), pinnedReq)
					Expect(err).NotTo(HaveOccurred())

					var raw []provider.StreamChunk
					for chunk := range ch {
						raw = append(raw, chunk)
					}
					var contents []string
					for _, c := range stripTransitionChunks(raw) {
						contents = append(contents, c.Content)
					}

					attemptedMu.Lock()
					defer attemptedMu.Unlock()
					Expect(attempted).NotTo(BeEmpty())
					Expect(attempted[0]).To(Equal("anthropic"),
						"caller-pinned provider must be the first attempt")
					Expect(contents).To(Equal([]string{"anthropic-reply"}),
						"pinned provider's stream must be the one served; no silent flip")
					Expect(manager.LastProvider()).To(Equal("anthropic"))
					Expect(manager.LastModel()).To(Equal("claude-sonnet-4"))
				})
			})

			Context("and the pinned candidate genuinely fails", func() {
				var attempted []string
				var attemptedMu sync.Mutex

				BeforeEach(func() {
					attempted = nil
					// Pinned provider fails synchronously; the
					// non-pinned fallback succeeds. Failover must
					// still kick in — the pin is not a single-shot.
					registry.Register(&mockStreamProvider{
						name:     "anthropic",
						streamFn: syncErrorStreamFn(errors.New("pinned provider down")),
					})
					registry.Register(&mockStreamProvider{
						name: "ollama",
						streamFn: successStreamFn(
							provider.StreamChunk{Content: "ollama-fallback", Done: true},
						),
					})
					manager.SetBasePreferences([]provider.ModelPreference{
						{Provider: "ollama", Model: "llama3.2"},
						{Provider: "anthropic", Model: "claude-sonnet-4"},
					})
				})

				It("falls through to the remaining candidates without deadlocking on the dead pin", func() {
					recordingHandler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						attemptedMu.Lock()
						attempted = append(attempted, req.Provider)
						attemptedMu.Unlock()
						p, err := registry.Get(req.Provider)
						if err != nil {
							return nil, err
						}
						return p.Stream(ctx, *req)
					}

					handler := sh.Execute(recordingHandler)
					pinnedReq := &provider.ChatRequest{
						Provider: "anthropic",
						Model:    "claude-sonnet-4",
					}
					ch, err := handler(context.Background(), pinnedReq)
					Expect(err).NotTo(HaveOccurred())

					var contents []string
					var raw []provider.StreamChunk
					for chunk := range ch {
						raw = append(raw, chunk)
					}
					for _, c := range stripTransitionChunks(raw) {
						contents = append(contents, c.Content)
					}

					attemptedMu.Lock()
					defer attemptedMu.Unlock()
					// Pinned provider must be attempted first,
					// then ollama as fallback — two attempts,
					// anthropic before ollama.
					Expect(len(attempted)).To(BeNumerically(">=", 2),
						"must attempt both pinned and fallback")
					Expect(attempted[0]).To(Equal("anthropic"),
						"pinned provider is attempted first")
					Expect(attempted[1]).To(Equal("ollama"),
						"fallback kicks in when pinned provider fails")
					Expect(contents).To(Equal([]string{"ollama-fallback"}))
					Expect(manager.LastProvider()).To(Equal("ollama"))
				})
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

		// Phase 3 #3 — when the provider error carries
		// RateLimit.RetryAfter (parsed from upstream's `retry-after`
		// header), the cooldown must honour the carrier signal rather
		// than the generic per-error-type table. Without this the
		// failover hook would pin a healthy provider to a 1-hour
		// rate-limit cooldown when the carrier asked for 30 seconds.
		Context("when sync error carries RateLimit.RetryAfter override", func() {
			BeforeEach(func() {
				rateLimitErr := &provider.Error{
					ErrorType: provider.ErrorTypeRateLimit,
					Provider:  "anthropic",
					Message:   "rate limited",
					RateLimit: &provider.RateLimit{
						RetryAfter: 30 * time.Second,
						RequestID:  "req_abc",
					},
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

			It("sets the cooldown to RetryAfter, not the default 1-hour rate-limit cooldown", func() {
				handler := sh.Execute(baseHandler(registry))
				before := time.Now()
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				after := time.Now()

				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue(),
					"the failed provider must still be marked unavailable")

				expiry, ok := health.RateLimitedUntil("anthropic", "claude-3")
				Expect(ok).To(BeTrue())
				// Carrier said 30s — so expiry is now+30s give or take
				// the wall-clock drift across the call. Anchor the
				// upper bound well below the 1-hour default so the
				// failure mode is unmistakable.
				Expect(expiry).To(BeTemporally(">=", before.Add(30*time.Second)))
				Expect(expiry).To(BeTemporally("<=", after.Add(30*time.Second)))
				Expect(expiry).To(BeTemporally("<", before.Add(2*time.Minute)),
					"carrier-issued 30s back-off must override the 1h default; landing inside the default window proves the override fired")
			})
		})

		// Phase 3 #3 — when the provider error has no RateLimit
		// metadata (RateLimit is nil — the absent-headers case),
		// the existing per-error-type cooldown must apply unchanged.
		// This guards backwards-compat for non-Anthropic providers
		// and pre-Phase-3 errors.
		Context("when sync error is a provider.Error with no RateLimit metadata", func() {
			BeforeEach(func() {
				rateLimitErr := &provider.Error{
					ErrorType: provider.ErrorTypeRateLimit,
					Provider:  "zai",
					Message:   "rate limited",
					// RateLimit deliberately nil — non-Anthropic
					// provider, no header propagation.
				}
				registry.Register(&mockStreamProvider{
					name:     "zai",
					streamFn: syncErrorStreamFn(rateLimitErr),
				})
				registry.Register(&mockStreamProvider{
					name: "backup",
					streamFn: successStreamFn(
						provider.StreamChunk{Content: "OK", Done: true},
					),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "zai", Model: "glm-4.6"},
					{Provider: "backup", Model: "backup-model"},
				})
			})

			It("falls back to the default 1-hour rate-limit cooldown", func() {
				handler := sh.Execute(baseHandler(registry))
				before := time.Now()
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).NotTo(HaveOccurred())
				after := time.Now()

				Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeTrue())
				expiry, ok := health.RateLimitedUntil("zai", "glm-4.6")
				Expect(ok).To(BeTrue())
				// Default rate-limit cooldown is exactly 1 hour. Since
				// no RateLimit was attached the override must not
				// fire, so expiry is now+1h modulo wall-clock drift.
				Expect(expiry).To(BeTemporally(">=", before.Add(time.Hour)))
				Expect(expiry).To(BeTemporally("<=", after.Add(time.Hour)))
			})
		})

		// H7 — request-level errors (the request itself can never succeed,
		// not the provider) must NOT mark the provider as unavailable. The
		// canonical case is ErrorTypeContextWindowExceeded: an oversized
		// prompt won't fit anywhere in the failover chain, so blackballing
		// providers in turn just empties the chain on a 5-minute (Unknown)
		// or 24-hour cooldown that doesn't recover until well after the
		// request is gone. The failover hook still publishes the error
		// event for observability and propagates the error to the caller —
		// it just stops scribbling on health state.
		Context("when sync error is a provider.Error with context_window_exceeded type (H7)", func() {
			BeforeEach(func() {
				ctxErr := &provider.Error{
					ErrorType:   provider.ErrorTypeContextWindowExceeded,
					Provider:    "anthropic",
					Message:     "prompt is too long for context window",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(ctxErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("does NOT mark the provider as unavailable on a request-level overflow", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred(),
					"the error still surfaces to the caller — only the "+
						"health-marking is skipped")
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse(),
					"request-level context-window overflow must not "+
						"blackball the provider; otherwise an oversized "+
						"prompt cascades through the chain and parks "+
						"every provider on a long cooldown")
			})
		})

		// H7 — same gate must apply when the error arrives async on the
		// stream channel as a Done+Error chunk (the second markProviderHealth
		// call site, post-peek of the first chunk).
		Context("when async first chunk carries context_window_exceeded error (H7)", func() {
			BeforeEach(func() {
				ctxErr := &provider.Error{
					ErrorType:   provider.ErrorTypeContextWindowExceeded,
					Provider:    "anthropic",
					Message:     "prompt is too long for context window",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: asyncErrorStreamFn(ctxErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("does NOT mark the provider as unavailable", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse(),
					"request-level overflow on the async path must "+
						"also skip health-marking — same contract as "+
						"the sync sibling spec")
			})
		})

		// H8 — auth-failure 401/403 must NOT mark the provider as unavailable.
		// The default cooldown for AuthFailure is 24h and the unhealthy state is
		// persisted to disk, so a single typo or rotated key produced a 24-hour
		// blackout that survived restarts. The fault attribute is the user's
		// credential, not the provider — the cure is "fix the key", and the
		// next request should immediately succeed once it's fixed. Blackballing
		// the provider for a day on every keystroke mistake is the wrong
		// trade-off. The error still surfaces and the per-call observability
		// event still fires; only the persistent health-state mutation is
		// skipped. Mirrors H7's request-level gate; extends the named-category
		// set rather than rewriting it.
		Context("when sync error is a provider.Error with auth_failure type (H8)", func() {
			BeforeEach(func() {
				authErr := &provider.Error{
					ErrorType:   provider.ErrorTypeAuthFailure,
					Provider:    "anthropic",
					Message:     "invalid API key",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(authErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("does NOT mark the provider as unavailable on a typo'd or rotated key", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred(),
					"the auth error still surfaces to the caller — "+
						"only the persistent 24h health-mark is skipped")
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse(),
					"a fixable credential mistake must not park the "+
						"provider on a 24h cooldown that survives restart; "+
						"the user fixes the key and the next request must "+
						"succeed without waiting a day")
			})
		})

		// H8 — same gate must apply when the auth error arrives async on the
		// stream channel as a Done+Error chunk. Mirrors the H7 async sibling.
		Context("when async first chunk carries auth_failure error (H8)", func() {
			BeforeEach(func() {
				authErr := &provider.Error{
					ErrorType:   provider.ErrorTypeAuthFailure,
					Provider:    "anthropic",
					Message:     "invalid API key",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: asyncErrorStreamFn(authErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("does NOT mark the provider as unavailable", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse(),
					"auth-failure on the async path must also skip "+
						"health-marking — same contract as the sync "+
						"sibling spec")
			})
		})

		// H8 negative — the gate is intentionally narrow. Categories that are
		// genuinely the provider's fault (rate-limit, server error) or
		// per-credential billing/quota (where re-trying immediately is
		// pointless) must continue to mark health. This guards against a
		// future change collapsing the gate into a blanket `IsRetriable`
		// check, which would silently disable failover for the categories
		// it's actually useful for.
		Context("when sync error is a provider.Error with billing type (H8 negative)", func() {
			BeforeEach(func() {
				billingErr := &provider.Error{
					ErrorType:   provider.ErrorTypeBilling,
					Provider:    "anthropic",
					Message:     "insufficient balance",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(billingErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("DOES mark the provider as unavailable on billing errors", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue(),
					"billing/quota are per-credential exhaustion — the "+
						"next call with the same key will fail the same way, "+
						"so the long cooldown is the right behaviour and "+
						"failover to a different provider is meaningful")
			})
		})

		// H9 — model-not-found 404 must NOT mark the provider as unavailable.
		// A wrong model ID in an agent manifest (e.g. requesting "claude-sonnet-4-7"
		// which does not exist) produces a 404 from the Anthropic API. The previous
		// behaviour classified this as ErrorTypeModelNotFound and applied a 24-hour
		// provider-wide cooldown via CooldownForErrorType. This is wrong: the fault
		// is the caller's model ID, not the provider's health. Every OTHER model
		// on the same provider remains fully functional. Blackballing the entire
		// provider for a day because one manifest had a typo cascades into a total
		// outage when other providers are also unavailable (rate-limited, etc.).
		// The error still surfaces to the caller; only the persistent health-state
		// mutation is skipped. Mirrors H7/H8 — extends the named-category set.
		Context("when sync error is a provider.Error with model_not_found type (H9)", func() {
			BeforeEach(func() {
				modelErr := &provider.Error{
					ErrorType:   provider.ErrorTypeModelNotFound,
					Provider:    "anthropic",
					Message:     "model: claude-sonnet-4-7 not found",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: syncErrorStreamFn(modelErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("does NOT mark the provider as unavailable on a wrong model ID", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred(),
					"the model-not-found error still surfaces to the caller — "+
						"only the persistent 24h health-mark is skipped")
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse(),
					"a wrong model ID must not park the provider on a 24h "+
						"cooldown; other models on the same provider remain "+
						"functional and the next request with the correct model "+
						"ID must succeed immediately")
			})
		})

		// H9 — same gate must apply when the model-not-found error arrives async.
		Context("when async first chunk carries model_not_found error (H9)", func() {
			BeforeEach(func() {
				modelErr := &provider.Error{
					ErrorType:   provider.ErrorTypeModelNotFound,
					Provider:    "anthropic",
					Message:     "model: claude-sonnet-4-7 not found",
					IsRetriable: false,
				}
				registry.Register(&mockStreamProvider{
					name:     "anthropic",
					streamFn: asyncErrorStreamFn(modelErr),
				})
				manager.SetBasePreferences([]provider.ModelPreference{
					{Provider: "anthropic", Model: "claude-3"},
				})
			})

			It("does NOT mark the provider as unavailable", func() {
				handler := sh.Execute(baseHandler(registry))
				_, err := handler(context.Background(), &provider.ChatRequest{})
				Expect(err).To(HaveOccurred())
				Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse(),
					"model-not-found on the async path must also skip "+
						"health-marking — same contract as the sync sibling")
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

// Track B — provider_changed user-visible failover affordance.
//
// Earlier work (commit a49bd56 / Stage 1 thinking-event Track A) plumbed a
// typed-event SSE pattern end-to-end and the failover hook already publishes
// a `provider.error` EventBus event when a candidate fails. But the *success
// half* of a failover transition — "we tried anthropic, it 429'd, we
// switched to zai/glm-4.6 and that's the model now answering you" — was
// invisible to the chat UI. Non-technical users saw the answer arrive
// without any indication that a different model produced it (different
// quality / style / format). The user explicitly asked for a fallback alert
// in Track B.
//
// Wire contract: when a fallback candidate succeeds AFTER one or more
// previous candidates failed, the StreamHook MUST emit a synthetic
// StreamChunk with EventType == "provider_changed" as the FIRST chunk on
// the replay channel, BEFORE the real first chunk from the successful
// provider. The chunk carries the from/to provider+model pair and a plain
// reason string ("rate_limited", "model_not_found", "auth_failure", …)
// derived from the error that retired the previous candidate. Downstream
// the SSE writer in internal/api/server.go routes the chunk to a
// {"type":"provider_changed",...} JSON event by EventType.
//
// What the chunk MUST NOT do:
//   - Carry user-visible content (Content == "" — this is metadata).
//   - Mark the stream Done (the real provider continues after).
//   - Fire when the FIRST candidate succeeded (no failover happened).
var _ = Describe("StreamHook provider_changed event", func() {
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

	Context("when the first provider succeeds (no failover needed)", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Hello", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			})
		})

		It("does NOT emit a provider_changed event — the user is on their primary", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			for _, c := range chunks {
				Expect(c.EventType).NotTo(Equal("provider_changed"),
					"no failover happened, no transition event should fire")
			}
		})
	})

	Context("when the first candidate fails and the second succeeds", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: asyncErrorStreamFn(&provider.Error{
					ErrorType: provider.ErrorTypeRateLimit,
					Provider:  "anthropic",
					Message:   "429 rate limited",
				}),
			})
			registry.Register(&mockStreamProvider{
				name: "zai",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Fallback content", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				{Provider: "zai", Model: "glm-4.6"},
			})
		})

		It("prepends a provider_changed chunk before the real stream", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}

			Expect(len(chunks)).To(BeNumerically(">=", 2),
				"failover must produce at least the transition chunk + the real content chunk; got %d", len(chunks))

			first := chunks[0]
			Expect(first.EventType).To(Equal("provider_changed"),
				"the transition chunk must precede content from the new provider so the chat UI can toast the user before the answer streams in")
			Expect(first.Done).To(BeFalse(),
				"the real provider's stream continues after this chunk")
		})

		// The transition chunk carries from/to/reason as a JSON payload in
		// chunk.Content. This mirrors how harness_* events ride the same
		// field — the dispatcher at internal/api/server.go:816 routes
		// EventType-tagged chunks to typed SSE writers (and their Content
		// is metadata, not assistant text), and parsing/re-emission stays
		// in one place. A new struct field on StreamChunk would also work
		// but is heavier than necessary for a single new event type.
		It("carries from/to/reason metadata as JSON in chunk.Content", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var transition *provider.StreamChunk
			for chunk := range ch {
				c := chunk
				if c.EventType == "provider_changed" {
					transition = &c
					break
				}
			}
			Expect(transition).NotTo(BeNil(),
				"a transition chunk must be present when failover happens")

			var payload struct {
				From   string `json:"from"`
				To     string `json:"to"`
				Reason string `json:"reason"`
			}
			Expect(json.Unmarshal([]byte(transition.Content), &payload)).To(Succeed(),
				"chunk.Content must be parseable JSON; got: %q", transition.Content)
			Expect(payload.From).To(Equal("anthropic+claude-sonnet-4-6"),
				"from is provider+model concatenated so the frontend can show the previous model in the toast")
			Expect(payload.To).To(Equal("zai+glm-4.6"))
			Expect(payload.Reason).To(Equal("rate_limited"),
				"reason is a stable machine-readable string the frontend maps to plain language ('primary model rate-limited')")
		})

		// M3-adjacent — mirror sseModelActive's split provider/model shape.
		//
		// The legacy {from, to} pair uses "<provider>+<model>" joined
		// strings. The sibling model_active payload split provider/model
		// into separate fields specifically to avoid the "+" parse-hop
		// (rare model ids contain "+"; openrouter). The migration ships
		// both shapes simultaneously so consumers can switch over
		// gracefully — the joined fields stay for backwards-compat, the
		// split fields become the preferred read path.
		It("carries split from_provider/from_model/to_provider/to_model alongside the legacy joined fields", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var transition *provider.StreamChunk
			for chunk := range ch {
				c := chunk
				if c.EventType == "provider_changed" {
					transition = &c
					break
				}
			}
			Expect(transition).NotTo(BeNil(),
				"a transition chunk must be present when failover happens")

			var payload struct {
				From         string `json:"from"`
				To           string `json:"to"`
				FromProvider string `json:"from_provider"`
				FromModel    string `json:"from_model"`
				ToProvider   string `json:"to_provider"`
				ToModel      string `json:"to_model"`
				Reason       string `json:"reason"`
			}
			Expect(json.Unmarshal([]byte(transition.Content), &payload)).To(Succeed(),
				"chunk.Content must be parseable JSON; got: %q", transition.Content)

			// Legacy joined fields stay populated — backwards-compat.
			Expect(payload.From).To(Equal("anthropic+claude-sonnet-4-6"))
			Expect(payload.To).To(Equal("zai+glm-4.6"))

			// Split fields populated identically to sseModelActive's
			// (provider, model) pair so the chip skips the "+" parse hop.
			Expect(payload.FromProvider).To(Equal("anthropic"))
			Expect(payload.FromModel).To(Equal("claude-sonnet-4-6"))
			Expect(payload.ToProvider).To(Equal("zai"))
			Expect(payload.ToModel).To(Equal("glm-4.6"))
		})

		It("forwards the real content from the new provider AFTER the transition", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var raw []provider.StreamChunk
			for chunk := range ch {
				raw = append(raw, chunk)
			}

			// Two metadata chunks (provider_changed + model_active) precede
			// the real content; their relative order is unspecified at the
			// consumer (both are control events the frontend handles
			// independently). Strip them and assert the user-visible content
			// arrives intact.
			content := stripTransitionChunks(raw)
			Expect(content).To(HaveLen(1),
				"after stripping metadata chunks the stream must carry exactly the real fallback content")
			Expect(content[0].Content).To(Equal("Fallback content"),
				"the real fallback provider's content must arrive intact after the metadata chunks")
			Expect(content[0].Done).To(BeTrue())

			// Both metadata chunks must precede the real content so the chip
			// has the actual model in hand before the first user-visible
			// token arrives.
			var sawContent bool
			for _, c := range raw {
				if c.EventType == "" && c.Content != "" {
					sawContent = true
				}
				if (c.EventType == "provider_changed" || c.EventType == "model_active") && sawContent {
					Fail("metadata chunk arrived AFTER user-visible content; chip cannot pivot before token streams in")
				}
			}
		})
	})

	Context("when both candidates fail", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name:     "anthropic",
				streamFn: syncErrorStreamFn(errors.New("anthropic exploded")),
			})
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(errors.New("zai exploded")),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				{Provider: "zai", Model: "glm-4.6"},
			})
		})

		It("returns an error (no provider_changed because no candidate succeeded)", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).To(HaveOccurred(),
				"all-failed must surface as an error so the chat UI can render an error state, not a phantom transition")
		})
	})
})

// model_active addresses the user complaint
// (May 2026): "the chip shows what was selected, not what actually ran".
//
// The user's selection updates `currentModelId` / `currentProviderId`
// optimistically (so the picker stays responsive). Until this event existed,
// the chat UI had no signal during streaming that distinguished the
// selection from the actual model running — only after the assistant turn
// finished and the post-stream reconcile pulled the engine-stamped
// (model, provider) pair from the persisted message.
//
// The fix: the failover hook prepends a "model_active" chunk to EVERY
// successful stream — not just on failover transitions, where the
// "provider_changed" affordance fires. The chunk carries the actual
// (provider, model) the failover hook chose, so the chip can pivot to
// the truth the moment streaming starts. When the actual matches the
// user's selection (the common case) this is a no-op for the user; when
// they differ (failover, agent override, manifest override), the chip
// snaps to the actual model immediately rather than waiting on reconcile.
//
// Wire contract:
//   - EventType == "model_active" on a synthetic StreamChunk.
//   - Content carries a JSON payload {"provider":"<id>","model":"<id>"}.
//   - Position: immediately before the upstream's first real chunk. When a
//     "provider_changed" chunk also fires (failover happened), the order
//     between the two metadata chunks is unspecified — both must arrive
//     before any user-visible content.
//   - Done == false (real provider continues after).
//   - No content (chunk.Content is metadata, not assistant text).
var _ = Describe("StreamHook model_active event", func() {
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

	Context("when the first provider succeeds (no failover needed)", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Hello", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			})
		})

		It("prepends a model_active chunk before the real stream so the chip can pivot to actual on first chunk", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}

			Expect(len(chunks)).To(BeNumerically(">=", 2),
				"the prepend must produce at least the model_active chunk + the real content chunk; got %d", len(chunks))
			first := chunks[0]
			Expect(first.EventType).To(Equal("model_active"),
				"the first chunk must announce the actual model so the frontend chip can pivot from selection to actual the moment streaming starts")
			Expect(first.Done).To(BeFalse(),
				"the real provider's stream continues after this chunk")
		})

		It("carries provider/model metadata as JSON in chunk.Content", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var active *provider.StreamChunk
			for chunk := range ch {
				c := chunk
				if c.EventType == "model_active" {
					active = &c
					break
				}
			}
			Expect(active).NotTo(BeNil(),
				"a model_active chunk must be present on every successful stream")

			var payload struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			}
			Expect(json.Unmarshal([]byte(active.Content), &payload)).To(Succeed(),
				"chunk.Content must be parseable JSON; got: %q", active.Content)
			Expect(payload.Provider).To(Equal("anthropic"),
				"provider must carry the actual provider id used (not the user's selection)")
			Expect(payload.Model).To(Equal("claude-sonnet-4-6"),
				"model must carry the actual model id used so the chip can render `<model> · <provider>` truthfully")
		})
	})

	Context("when the first candidate fails and the second succeeds", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: asyncErrorStreamFn(&provider.Error{
					ErrorType: provider.ErrorTypeRateLimit,
					Provider:  "anthropic",
					Message:   "429 rate limited",
				}),
			})
			registry.Register(&mockStreamProvider{
				name: "zai",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Fallback content", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				{Provider: "zai", Model: "glm-4.6"},
			})
		})

		It("emits model_active with the FALLBACK provider/model, not the original", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var active *provider.StreamChunk
			for chunk := range ch {
				c := chunk
				if c.EventType == "model_active" {
					active = &c
					break
				}
			}
			Expect(active).NotTo(BeNil(),
				"model_active must fire even when failover happens — the chip needs to know the *actual* model that's about to produce content, not the one that failed")

			var payload struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			}
			Expect(json.Unmarshal([]byte(active.Content), &payload)).To(Succeed())
			Expect(payload.Provider).To(Equal("zai"),
				"the surviving provider, not the rate-limited one")
			Expect(payload.Model).To(Equal("glm-4.6"))
		})
	})

	Context("when both candidates fail", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name:     "anthropic",
				streamFn: syncErrorStreamFn(errors.New("anthropic exploded")),
			})
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(errors.New("zai exploded")),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				{Provider: "zai", Model: "glm-4.6"},
			})
		})

		It("does NOT emit a model_active event when no candidate succeeded", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).To(HaveOccurred(),
				"all-failed must surface as an error; there is no actual model to announce")
		})
	})
})

// M2 — prependProviderChangedChunk and prependModelActiveChunk wrap the
// upstream replay channel with a goroutine that forwards chunks to a new
// (buffered, size 17) channel. Before this fix the inner forward loop used a
// bare `out <- chunk` with no ctx-awareness:
//
//	go func() {
//		defer close(out)
//		out <- transition          // unguarded
//		for chunk := range upstream {
//			out <- chunk            // unguarded
//		}
//	}()
//
// Leak shape: when the SSE consumer disconnects mid-stream the chat handler
// stops draining `out`. After at most 17 buffered chunks the wrapper
// goroutine blocks on `out <- chunk`. Nothing in the wrapper observes the
// parent ctx, so the wrapper holds the upstream alive (and itself blocked)
// until `streamWithReplay`'s own per-attempt timeoutCtx fires (default
// stream-timeout — tens of seconds to minutes). The wrapper goroutine is
// pinned the whole time even though the consumer is long gone.
//
// Fix shape (pinned by these specs): each `out <- ...` send must be a
// ctx-aware select. When the parent ctx cancels (consumer disconnect, user
// Escape, navigation away), the wrapper exits promptly and closes `out`.
//
// Observable: with consumer-stopped-draining + parent-ctx-cancelled, the
// `out` channel must close within a short window (well below the
// per-attempt stream timeout). If the bug regressed the goroutine would
// stay parked on the send and the channel would never close.
var _ = Describe("StreamHook M2 — prepend* goroutines exit on ctx cancel", func() {
	var (
		manager  *failover.Manager
		registry *provider.Registry
		health   *failover.HealthManager
		sh       *failover.StreamHook
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		manager = failover.NewManager(registry, health, 30*time.Second)
		sh = failover.NewStreamHook(manager, nil, "")
	})

	// slowStreamFn produces a stream that emits chunks until ctx is done or
	// `count` chunks have been emitted, whichever first. Chunks are emitted
	// without artificial delay; capacity is 1 so producer naturally waits on
	// consumer drain. This shape is enough to overflow the wrapper's 17-slot
	// buffer when the consumer stops draining.
	slowStreamFn := func(count int, sent *int64) func(context.Context, provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		return func(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			ch := make(chan provider.StreamChunk, 1)
			go func() {
				defer close(ch)
				for i := 0; i < count; i++ {
					select {
					case <-ctx.Done():
						return
					case ch <- provider.StreamChunk{Content: fmt.Sprintf("chunk-%d", i)}:
						atomic.AddInt64(sent, 1)
					}
				}
			}()
			return ch, nil
		}
	}

	Context("first provider succeeds (only prependModelActiveChunk wraps)", func() {
		var sent int64

		BeforeEach(func() {
			atomic.StoreInt64(&sent, 0)
			registry.Register(&mockStreamProvider{
				name:     "anthropic",
				streamFn: slowStreamFn(200, &sent),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
			})
		})

		It("closes the wrapper channel promptly after parent ctx cancels and consumer stops draining", func() {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()

			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(parent, &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			// Drain a small handful of chunks (enough to leave many more
			// queued / in flight), then stop draining.
			drained := 0
			for drained < 3 {
				select {
				case _, ok := <-ch:
					if !ok {
						Fail("upstream closed before we could stage the leak")
					}
					drained++
				case <-time.After(2 * time.Second):
					Fail("timed out staging the leak — did not receive initial chunks")
				}
			}

			// Cancel the parent ctx — this is what a real SSE disconnect /
			// user Escape would do upstream.
			cancel()

			// After cancel + consumer-stopped-draining, the wrapper goroutine
			// MUST exit and close `ch` within a short window. Before the fix
			// the goroutine is blocked on `out <- chunk` and ignores ctx, so
			// `ch` stays open until streamWithReplay's per-attempt timeoutCtx
			// fires — orders of magnitude beyond this budget.
			Eventually(func() bool {
				select {
				case _, ok := <-ch:
					// Either we received a queued chunk (still alive,
					// keep polling) or the channel closed (goroutine exited
					// — the contract).
					return !ok
				default:
					return false
				}
			}, 1*time.Second, 10*time.Millisecond).Should(BeTrue(),
				"prependModelActiveChunk goroutine must exit on parent ctx cancel; otherwise it leaks for the per-attempt stream timeout")
		})
	})

	Context("first candidate fails, second succeeds (BOTH prependProviderChangedChunk AND prependModelActiveChunk wrap)", func() {
		var sent int64

		BeforeEach(func() {
			atomic.StoreInt64(&sent, 0)
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: asyncErrorStreamFn(&provider.Error{
					ErrorType: provider.ErrorTypeRateLimit,
					Provider:  "anthropic",
					Message:   "429 rate limited",
				}),
			})
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: slowStreamFn(200, &sent),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				{Provider: "zai", Model: "glm-4.6"},
			})
		})

		It("closes both wrapper channels promptly after parent ctx cancels and consumer stops draining", func() {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()

			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(parent, &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			// Drain a couple chunks (we expect provider_changed +
			// model_active to land first, then real content).
			drained := 0
			for drained < 3 {
				select {
				case _, ok := <-ch:
					if !ok {
						Fail("upstream closed before we could stage the leak")
					}
					drained++
				case <-time.After(2 * time.Second):
					Fail("timed out staging the leak — did not receive initial chunks")
				}
			}

			cancel()

			Eventually(func() bool {
				select {
				case _, ok := <-ch:
					return !ok
				default:
					return false
				}
			}, 1*time.Second, 10*time.Millisecond).Should(BeTrue(),
				"the outermost wrapper goroutine (prependProviderChangedChunk wrapping prependModelActiveChunk wrapping streamWithReplay) must exit on parent ctx cancel; otherwise each wrapper layer leaks for the per-attempt stream timeout")
		})

		It("the outermost wrapper channel drains to close after ctx cancel (no parked sender)", func() {
			// Belt-and-braces companion to the close-channel pin: instead
			// of polling for a closed-receive, this test drains the
			// channel to its terminus after cancelling. Before the fix
			// the consumer drain would hang forever (wrapper goroutine
			// parked on a full `out`); after the fix the wrapper exits
			// promptly and the receiver loop terminates.
			//
			// We deliberately do NOT assert runtime.NumGoroutine here:
			// streamWithReplay and the mock provider's emitter sit on a
			// detached timeoutCtx (per the Bug #2 fix) and only release
			// on per-attempt timeout, so the global goroutine count is
			// not the right observable for M2. The wrapper-channel-close
			// IS the M2 contract.
			parent, cancel := context.WithCancel(context.Background())
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(parent, &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			// Stage the wedge: drain a few, then stop.
			for i := 0; i < 3; i++ {
				select {
				case <-ch:
				case <-time.After(2 * time.Second):
					Fail("could not stage leak — stream stalled")
				}
			}

			cancel()

			drainDone := make(chan struct{})
			go func() {
				for range ch {
				}
				close(drainDone)
			}()
			select {
			case <-drainDone:
			case <-time.After(2 * time.Second):
				Fail("wrapper-channel drain did not terminate within 2s of ctx cancel — goroutine leak (M2 regression)")
			}
		})
	})
})
