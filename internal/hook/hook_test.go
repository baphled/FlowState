package hook_test

import (
	"bytes"
	"context"
	"log"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Hook", func() {
	var (
		ctx     context.Context
		request *provider.ChatRequest
	)

	BeforeEach(func() {
		ctx = context.Background()
		request = &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: "You are a helpful assistant."},
				{Role: "user", Content: "Hello"},
			},
		}
	})

	Describe("Chain", func() {
		Context("with no hooks", func() {
			It("passes through to the handler unchanged", func() {
				handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					ch := make(chan provider.StreamChunk, 1)
					ch <- provider.StreamChunk{Content: "response", Done: true}
					close(ch)
					return ch, nil
				}

				chain := hook.NewChain()
				wrappedHandler := chain.Execute(handler)

				resultChan, err := wrappedHandler(ctx, request)
				Expect(err).NotTo(HaveOccurred())

				var chunks []provider.StreamChunk
				for chunk := range resultChan {
					chunks = append(chunks, chunk)
				}

				Expect(chunks).To(HaveLen(1))
				Expect(chunks[0].Content).To(Equal("response"))
			})
		})

		Context("with a before hook", func() {
			It("modifies the request before the handler is called", func() {
				var capturedContent string

				beforeHook := func(next hook.HandlerFunc) hook.HandlerFunc {
					return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						req.Messages = append(req.Messages, provider.Message{Role: "user", Content: "injected"})
						return next(ctx, req)
					}
				}

				handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					capturedContent = req.Messages[len(req.Messages)-1].Content
					ch := make(chan provider.StreamChunk, 1)
					ch <- provider.StreamChunk{Content: "ok", Done: true}
					close(ch)
					return ch, nil
				}

				chain := hook.NewChain(beforeHook)
				wrappedHandler := chain.Execute(handler)

				_, err := wrappedHandler(ctx, request)
				Expect(err).NotTo(HaveOccurred())
				Expect(capturedContent).To(Equal("injected"))
			})
		})

		Context("with an after hook", func() {
			It("runs after the handler completes", func() {
				var afterRan bool

				afterHook := func(next hook.HandlerFunc) hook.HandlerFunc {
					return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						resultChan, err := next(ctx, req)
						if err != nil {
							return nil, err
						}

						outChan := make(chan provider.StreamChunk, 16)
						go func() {
							defer close(outChan)
							for chunk := range resultChan {
								outChan <- chunk
							}
							afterRan = true
						}()
						return outChan, nil
					}
				}

				handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					ch := make(chan provider.StreamChunk, 1)
					ch <- provider.StreamChunk{Content: "done", Done: true}
					close(ch)
					return ch, nil
				}

				chain := hook.NewChain(afterHook)
				wrappedHandler := chain.Execute(handler)

				resultChan, err := wrappedHandler(ctx, request)
				Expect(err).NotTo(HaveOccurred())

				for v := range resultChan {
					_ = v
					_ = 0
				}

				Eventually(func() bool { return afterRan }).Should(BeTrue())
			})
		})

		Context("with multiple hooks", func() {
			It("executes hooks in order", func() {
				var order []int

				hook1 := func(next hook.HandlerFunc) hook.HandlerFunc {
					return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						order = append(order, 1)
						return next(ctx, req)
					}
				}

				hook2 := func(next hook.HandlerFunc) hook.HandlerFunc {
					return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
						order = append(order, 2)
						return next(ctx, req)
					}
				}

				handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					order = append(order, 3)
					ch := make(chan provider.StreamChunk, 1)
					ch <- provider.StreamChunk{Done: true}
					close(ch)
					return ch, nil
				}

				chain := hook.NewChain(hook1, hook2)
				wrappedHandler := chain.Execute(handler)

				_, err := wrappedHandler(ctx, request)
				Expect(err).NotTo(HaveOccurred())

				Expect(order).To(Equal([]int{1, 2, 3}))
			})
		})
	})

	Describe("LoggingHook", func() {
		It("does not modify request or response", func() {
			originalMessages := len(request.Messages)

			handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				Expect(req.Messages).To(HaveLen(originalMessages))
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "test response", Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(hook.LoggingHook())
			wrappedHandler := chain.Execute(handler)

			resultChan, err := wrappedHandler(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range resultChan {
				chunks = append(chunks, chunk)
			}

			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("test response"))
		})

		// Diagnostic bug evidence (session-1776611908809856897): when the planner
		// emitted tool-call-shaped JSON as plain text content, operators could not
		// tell from the log whether req.Tools was empty at stream time. The log
		// must surface the tool count so "no tools attached" bugs surface on the
		// very first line without reading a JSON payload or a core dump.
		It("logs the tool count alongside the message count on request start", func() {
			var logBuf bytes.Buffer
			origOutput := log.Writer()
			log.SetOutput(&logBuf)
			DeferCleanup(func() { log.SetOutput(origOutput) })

			req := &provider.ChatRequest{
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
				Tools: []provider.Tool{
					{Name: "delegate"},
					{Name: "coordination_store"},
					{Name: "skill_load"},
					{Name: "todowrite"},
				},
			}

			handler := func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(hook.LoggingHook())
			wrappedHandler := chain.Execute(handler)

			resultChan, err := wrappedHandler(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			for v := range resultChan {
				_ = v
			}

			Expect(logBuf.String()).To(ContainSubstring("tools=4"),
				"LoggingHook must emit tools=<count> so operators can see at a "+
					"glance whether tool schemas reached the provider request.")
		})

		It("logs tools=0 when the request carries no tools", func() {
			var logBuf bytes.Buffer
			origOutput := log.Writer()
			log.SetOutput(&logBuf)
			DeferCleanup(func() { log.SetOutput(origOutput) })

			req := &provider.ChatRequest{
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
			}

			handler := func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(hook.LoggingHook())
			wrappedHandler := chain.Execute(handler)

			resultChan, err := wrappedHandler(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			for v := range resultChan {
				_ = v
			}

			Expect(logBuf.String()).To(ContainSubstring("tools=0"))
		})
	})

	Describe("LearningHook", func() {
		It("captures interaction to the store after response completes", func() {
			store := &mockLearningStore{entries: []learning.Entry{}}

			handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 2)
				ch <- provider.StreamChunk{Content: "Hello "}
				ch <- provider.StreamChunk{Content: "world", Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(hook.LearningHook(store))
			wrappedHandler := chain.Execute(handler)

			resultChan, err := wrappedHandler(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			for v := range resultChan {
				_ = v
				_ = 0
			}

			Eventually(func() int { return len(store.entries) }).Should(Equal(1))
			Expect(store.entries[0].UserMessage).To(Equal("Hello"))
			Expect(store.entries[0].Response).To(Equal("Hello world"))
		})
	})

})

type mockLearningStore struct {
	entries []learning.Entry
}

func (m *mockLearningStore) Capture(entry learning.Entry) error {
	m.entries = append(m.entries, entry)
	return nil
}

func (m *mockLearningStore) Query(_ string) []learning.Entry {
	return m.entries
}

var _ learning.Store = (*mockLearningStore)(nil)
