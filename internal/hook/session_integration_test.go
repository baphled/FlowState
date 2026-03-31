package hook_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("SkillAutoLoaderHook session integration", Label("integration"), func() {
	var (
		cfg          *hook.SkillAutoLoaderConfig
		testManifest agent.Manifest
	)

	runSessionHook := func(ctx context.Context, req *provider.ChatRequest) (*provider.ChatRequest, bool) {
		h := hook.SkillAutoLoaderHook(cfg, func() agent.Manifest { return testManifest })
		var captured *provider.ChatRequest
		called := false
		chain := hook.NewChain(h)
		handler := chain.Execute(func(_ context.Context, r *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			captured = r
			called = true
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "ok", Done: true}
			close(ch)
			return ch, nil
		})
		resultChan, err := handler(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		for v := range resultChan {
			_ = v
		}
		return captured, called
	}

	Context("when streaming with agent-provided skills", func() {
		BeforeEach(func() {
			cfg = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			testManifest = agent.Manifest{
				ID: "test-agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"golang", "testing"},
				},
			}
		})

		It("injects agent skills into the system prompt during stream", func() {
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Write some code"},
				},
			}
			captured, called := runSessionHook(context.Background(), req)

			Expect(called).To(BeTrue())
			Expect(captured.Messages[0].Content).To(ContainSubstring("Your load_skills: [golang, testing]"))
		})
	})

	Context("when streaming with no skills configured", func() {
		BeforeEach(func() {
			cfg = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			testManifest = agent.Manifest{
				ID: "empty-agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{},
				},
			}
		})

		It("does not inject into the system prompt", func() {
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Hello"},
				},
			}
			captured, called := runSessionHook(context.Background(), req)

			Expect(called).To(BeTrue())
			Expect(captured.Messages[0].Content).To(Equal("You are helpful."))
		})
	})
})

var _ = Describe("SkillAutoLoaderHook session resumption", Label("integration"), func() {
	var (
		cfg          *hook.SkillAutoLoaderConfig
		testManifest agent.Manifest
	)

	runSessionHook := func(req *provider.ChatRequest) *provider.ChatRequest {
		h := hook.SkillAutoLoaderHook(cfg, func() agent.Manifest { return testManifest })
		var captured *provider.ChatRequest
		chain := hook.NewChain(h)
		handler := chain.Execute(func(_ context.Context, r *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			captured = r
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "ok", Done: true}
			close(ch)
			return ch, nil
		})
		resultChan, err := handler(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		for v := range resultChan {
			_ = v
		}
		return captured
	}

	BeforeEach(func() {
		cfg = &hook.SkillAutoLoaderConfig{
			BaselineSkills:   []string{},
			MaxAutoSkills:    6,
			CategoryMappings: map[string][]string{},
			KeywordPatterns:  []hook.KeywordPattern{},
		}
		testManifest = agent.Manifest{
			ID: "test-agent",
			Capabilities: agent.Capabilities{
				AlwaysActiveSkills: []string{"golang", "testing"},
			},
		}
	})

	Context("when messages include prior assistant replies (continuation)", func() {
		It("skips skill injection entirely", func() {
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Write some code"},
					{Role: "assistant", Content: "Here is some code..."},
					{Role: "user", Content: "Can you improve it?"},
				},
			}
			captured := runSessionHook(req)

			Expect(captured.Messages[0].Content).NotTo(ContainSubstring("Your load_skills:"))
			Expect(captured.Messages[0].Content).To(Equal("You are helpful."))
		})
	})

	Context("when system message already contains load_skills (tool-call follow-up)", func() {
		It("does not double-inject", func() {
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "Your load_skills: [golang]. Use skill_load(name) only when relevant to the current task.\n\nYou are helpful."},
					{Role: "user", Content: "Continue working"},
				},
			}
			captured := runSessionHook(req)

			count := strings.Count(captured.Messages[0].Content, "Your load_skills:")
			Expect(count).To(Equal(1))
		})
	})
})

var _ = Describe("Hook chain session context propagation", Label("integration"), func() {
	Context("when context carries a session ID", func() {
		It("session ID is accessible in hook handlers", func() {
			var capturedSessionID string
			spyHook := hook.Hook(func(next hook.HandlerFunc) hook.HandlerFunc {
				return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
					val, ok := ctx.Value(session.IDKey{}).(string)
					if ok {
						capturedSessionID = val
					}
					return next(ctx, req)
				}
			})

			cfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			manifest := agent.Manifest{
				ID: "test-agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"golang"},
				},
			}
			autoloaderHook := hook.SkillAutoLoaderHook(cfg, func() agent.Manifest { return manifest })

			chain := hook.NewChain(spyHook, autoloaderHook)
			handler := chain.Execute(func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			})

			ctx := context.WithValue(context.Background(), session.IDKey{}, "test-session-123")
			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Hello"},
				},
			}
			resultChan, err := handler(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			for v := range resultChan {
				_ = v
			}

			Expect(capturedSessionID).To(Equal("test-session-123"))
		})
	})

	Context("when context has no session ID", func() {
		It("hooks execute without error", func() {
			cfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			manifest := agent.Manifest{
				ID: "test-agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"golang"},
				},
			}
			autoloaderHook := hook.SkillAutoLoaderHook(cfg, func() agent.Manifest { return manifest })

			handlerCalled := false
			chain := hook.NewChain(autoloaderHook)
			handler := chain.Execute(func(_ context.Context, _ *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				handlerCalled = true
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			})

			req := &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "Hello"},
				},
			}
			resultChan, err := handler(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())
			for v := range resultChan {
				_ = v
			}

			Expect(handlerCalled).To(BeTrue())
		})
	})
})
