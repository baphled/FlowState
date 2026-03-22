package hook_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("SkillAutoLoaderHook", func() {
	var (
		ctx             context.Context
		request         *provider.ChatRequest
		config          *hook.SkillAutoLoaderConfig
		manifest        agent.Manifest
		capturedRequest *provider.ChatRequest
		passthrough     hook.HandlerFunc
	)

	BeforeEach(func() {
		ctx = context.Background()
		config = hook.DefaultSkillAutoLoaderConfig()
		manifest = agent.Manifest{
			ID:         "test-agent",
			Name:       "Test Agent",
			Complexity: "quick",
			Capabilities: agent.Capabilities{
				AlwaysActiveSkills: []string{"clean-code"},
			},
		}

		passthrough = func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			capturedRequest = req
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Content: "ok", Done: true}
			close(ch)
			return ch, nil
		}
	})

	Context("when a system message exists", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("prepends lean skill names to the system message", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(HavePrefix("Your load_skills: ["))
			Expect(systemContent).To(ContainSubstring("pre-action"))
			Expect(systemContent).To(ContainSubstring("memory-keeper"))
			Expect(systemContent).To(ContainSubstring("]. Call skill_load(name) for each before starting work."))
			Expect(systemContent).To(ContainSubstring("You are a helpful assistant."))
		})

		It("includes baseline skills regardless of prompt content", func() {
			request.Messages[1].Content = "something unrelated"
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("pre-action"))
			Expect(systemContent).To(ContainSubstring("memory-keeper"))
		})

		It("includes agent always-active skills", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("clean-code"))
		})
	})

	Context("when reading the last user message for keyword matching", func() {
		BeforeEach(func() {
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "test", Skills: []string{"golang-testing"}},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "first message"},
					{Role: "assistant", Content: "response"},
					{Role: "user", Content: "write a test for this"},
				},
			}
		})

		It("uses the last user message for keyword matching", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("golang-testing"))
		})
	})

	Context("when no system message exists", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("creates a system message with lean injection", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedRequest.Messages).To(HaveLen(2))
			Expect(capturedRequest.Messages[0].Role).To(Equal("system"))
			Expect(capturedRequest.Messages[0].Content).To(HavePrefix("Your load_skills: ["))
			Expect(capturedRequest.Messages[0].Content).To(ContainSubstring("]. Call skill_load(name) for each before starting work."))
		})
	})

	Context("when calling through to next handler", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "System prompt."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("calls through to the next handler without error", func() {
			var handlerCalled bool
			handler := func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				handlerCalled = true
				capturedRequest = req
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			}

			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(handler)

			resultChan, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())

			var chunks []provider.StreamChunk
			for chunk := range resultChan {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("ok"))
		})
	})

	Context("lean injection format", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "System prompt."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("uses the expected lean format", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring(
				"Your load_skills: [pre-action, memory-keeper, clean-code]. Call skill_load(name) for each before starting work.",
			))
		})
	})

	Context("metadata propagation", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "System prompt."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("sets loaded_skills in request metadata", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedRequest.Metadata).NotTo(BeNil())
			skills, ok := capturedRequest.Metadata["loaded_skills"]
			Expect(ok).To(BeTrue())
			skillNames, ok := skills.([]string)
			Expect(ok).To(BeTrue())
			Expect(skillNames).To(ContainElement("pre-action"))
			Expect(skillNames).To(ContainElement("memory-keeper"))
			Expect(skillNames).To(ContainElement("clean-code"))
		})

		It("initialises metadata map when nil", func() {
			request.Metadata = nil
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest })
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedRequest.Metadata).NotTo(BeNil())
		})
	})
})
