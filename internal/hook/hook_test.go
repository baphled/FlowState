package hook_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
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

	Describe("ContextInjectionHook", func() {
		It("adds skill content to system message", func() {
			skills := []skill.Skill{
				{Name: "skill1", Content: "Skill 1 instructions"},
				{Name: "skill2", Content: "Skill 2 instructions"},
				{Name: "skill3", Content: "Skill 3 instructions"},
			}
			activeSkillNames := []string{"skill1", "skill3"}

			var capturedMessages []provider.Message

			handler := func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				capturedMessages = req.Messages
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Done: true}
				close(ch)
				return ch, nil
			}

			chain := hook.NewChain(hook.ContextInjectionHook(skills, activeSkillNames))
			wrappedHandler := chain.Execute(handler)

			_, err := wrappedHandler(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedMessages).To(HaveLen(2))
			Expect(capturedMessages[0].Role).To(Equal("system"))
			Expect(capturedMessages[0].Content).To(ContainSubstring("You are a helpful assistant."))
			Expect(capturedMessages[0].Content).To(ContainSubstring("Skill 1 instructions"))
			Expect(capturedMessages[0].Content).To(ContainSubstring("Skill 3 instructions"))
			Expect(capturedMessages[0].Content).NotTo(ContainSubstring("Skill 2 instructions"))
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
