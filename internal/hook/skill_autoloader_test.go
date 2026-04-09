package hook_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(HavePrefix("Your load_skills: ["))
			Expect(systemContent).To(ContainSubstring("pre-action"))
			Expect(systemContent).To(ContainSubstring("memory-keeper"))
			Expect(systemContent).To(ContainSubstring("]. Use skill_load(name) only when relevant to the current task."))
			Expect(systemContent).To(ContainSubstring("You are a helpful assistant."))
		})

		It("includes baseline skills regardless of prompt content", func() {
			request.Messages[1].Content = "something unrelated"
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("pre-action"))
			Expect(systemContent).To(ContainSubstring("memory-keeper"))
		})

		It("includes agent always-active skills", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("clean-code"))
		})
	})

	Context("when the user message matches keyword patterns", func() {
		BeforeEach(func() {
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "test", Skills: []string{"golang-testing"}},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "write a test for this"},
				},
			}
		})

		It("includes keyword-matched skills in the injection", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
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
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedRequest.Messages).To(HaveLen(2))
			Expect(capturedRequest.Messages[0].Role).To(Equal("system"))
			Expect(capturedRequest.Messages[0].Content).To(HavePrefix("Your load_skills: ["))
			Expect(capturedRequest.Messages[0].Content).To(ContainSubstring("]. Use skill_load(name) only when relevant to the current task."))
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

			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
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
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("Your load_skills: ["))
			Expect(systemContent).To(ContainSubstring("]. Use skill_load(name) only when relevant to the current task."))
			for _, skill := range config.BaselineSkills {
				Expect(systemContent).To(ContainSubstring(skill))
			}
			Expect(systemContent).To(ContainSubstring("clean-code"))
		})
	})

	Context("when system message already contains load_skills", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "Your load_skills: [pre-action, memory-keeper, token-cost-estimation, retrospective, note-taking, knowledge-base, discipline, skill-discovery, agent-discovery, clean-code]. Use skill_load(name) only when relevant to the current task.\n\nYou are a helpful assistant."},
					{Role: "user", Content: "follow-up after tool call"},
				},
			}
		})

		It("does not double-inject skills into the system message", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			occurrences := strings.Count(systemContent, "Your load_skills:")
			Expect(occurrences).To(Equal(1))
		})
	})

	Context("when messages contain an assistant reply (continuation)", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "first message"},
					{Role: "assistant", Content: "I can help with that."},
					{Role: "user", Content: "second message"},
				},
			}
		})

		It("skips skill injection entirely", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(Equal("You are a helpful assistant."))
			Expect(systemContent).NotTo(ContainSubstring("Your load_skills:"))
		})

		It("does not modify the system message when baseline skills are empty", func() {
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:        []string{},
				MaxAutoSkills:         6,
				MaxAutoSkillsBytes:    35840,
				PerSkillMaxBytes:      5120,
				SkipOnSessionContinue: true,
				CategoryMappings:      map[string][]string{},
				KeywordPatterns:       []hook.KeywordPattern{},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are helpful."},
					{Role: "user", Content: "first message"},
					{Role: "assistant", Content: "first response"},
					{Role: "user", Content: "follow-up"},
				},
			}
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(Equal("You are helpful."))
		})

		It("still calls through to the next handler", func() {
			var handlerCalled bool
			handler := func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				handlerCalled = true
				capturedRequest = req
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			}

			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(handler)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})
	})

	Context("when no assistant messages exist (first message)", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("injects skills as normal", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("Your load_skills:"))
			Expect(systemContent).To(ContainSubstring("pre-action"))
		})
	})

	Context("when no skills are selected (empty baseline, no agent skills, no keyword match)", func() {
		var emptyManifest agent.Manifest

		BeforeEach(func() {
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			emptyManifest = agent.Manifest{
				ID:         "bare-agent",
				Name:       "Bare Agent",
				Complexity: "quick",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{},
				},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("does not inject anything into the system message", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return emptyManifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(Equal("You are a helpful assistant."))
			Expect(systemContent).NotTo(ContainSubstring("Your load_skills:"))
		})

		It("still calls through to the next handler", func() {
			var handlerCalled bool
			handler := func(_ context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
				handlerCalled = true
				capturedRequest = req
				ch := make(chan provider.StreamChunk, 1)
				ch <- provider.StreamChunk{Content: "ok", Done: true}
				close(ch)
				return ch, nil
			}

			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return emptyManifest }, nil, nil)
			wrapped := autoloader(handler)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())
			Expect(handlerCalled).To(BeTrue())
		})
	})

	Context("when system message contains a static 'Your load_skills:' placeholder (no bracket)", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "Your load_skills: use skill_load when needed.\n\nYou are the planner agent."},
					{Role: "user", Content: "help me plan"},
				},
			}
		})

		It("still injects the dynamic skills list because static placeholder lacks opening bracket", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("Your load_skills: ["),
				"dynamic injection should replace static placeholder")
		})
	})

	Context("when SkipOnSessionContinue is true and assistant messages are present", func() {
		BeforeEach(func() {
			config.SkipOnSessionContinue = true
			config.KeywordPatterns = []hook.KeywordPattern{
				{Pattern: "test", Skills: []string{"golang-testing"}},
			}
			config.CategoryMappings = map[string][]string{
				"quick": {"pragmatic-problem-solving"},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "write a test for me"},
					{Role: "assistant", Content: "Sure, I can help with that."},
					{Role: "user", Content: "also add error handling"},
				},
			}
		})

		It("injects only baseline skills, skipping Tier 2 and Tier 3", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("Your load_skills: ["))
			for _, skill := range config.BaselineSkills {
				Expect(systemContent).To(ContainSubstring(skill))
			}
			Expect(systemContent).NotTo(ContainSubstring("golang-testing"))
			Expect(systemContent).NotTo(ContainSubstring("pragmatic-problem-solving"))
			Expect(systemContent).NotTo(ContainSubstring("clean-code"))
		})
	})

	Context("when SkipOnSessionContinue is false (default) and assistant messages are present", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "write a test for me"},
					{Role: "assistant", Content: "Sure, I can help with that."},
					{Role: "user", Content: "also add error handling"},
				},
			}
		})

		It("skips all injection preserving current behaviour", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(Equal("You are a helpful assistant."))
			Expect(systemContent).NotTo(ContainSubstring("Your load_skills:"))
		})
	})

	Context("when cache is provided (direct content injection)", func() {
		var (
			cache    *hook.SkillContentCache
			cacheDir string
		)

		BeforeEach(func() {
			var err error
			cacheDir, err = os.MkdirTemp("", "skill-cache-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(cacheDir) })

			skillDir := filepath.Join(cacheDir, "skill-a")
			Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# My Skill\nDo things."), 0o600)).To(Succeed())

			cache = hook.NewSkillContentCache(cacheDir)
			Expect(cache.Init()).To(Succeed())

			manifest = agent.Manifest{
				ID:   "test-agent",
				Name: "Test Agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"skill-a"},
				},
			}
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("injects skill content blocks when cache is provided", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, cache)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring(`<skill name="skill-a">`))
			Expect(systemContent).To(ContainSubstring("# My Skill"))
			Expect(systemContent).To(ContainSubstring("</skill>"))
		})

		It("falls back to lean injection when cache is nil", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("Your load_skills: ["))
			Expect(systemContent).NotTo(ContainSubstring(`<skill name=`))
		})
	})

	Context("when cache is provided with ceiling enforcement", func() {
		var (
			cache    *hook.SkillContentCache
			cacheDir string
		)

		BeforeEach(func() {
			var err error
			cacheDir, err = os.MkdirTemp("", "skill-ceiling-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(cacheDir) })

			for i := range 5 {
				name := fmt.Sprintf("skill-%d", i)
				dir := filepath.Join(cacheDir, name)
				Expect(os.MkdirAll(dir, 0o755)).To(Succeed())
				content := strings.Repeat("X", 10240)
				Expect(os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600)).To(Succeed())
			}

			cache = hook.NewSkillContentCache(cacheDir)
			Expect(cache.Init()).To(Succeed())

			manifest = agent.Manifest{
				ID:   "test-agent",
				Name: "Test Agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"skill-0", "skill-1", "skill-2", "skill-3", "skill-4"},
				},
			}
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:     []string{},
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 35840,
				CategoryMappings:   map[string][]string{},
				KeywordPatterns:    []hook.KeywordPattern{},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("respects ceiling when cache provided — drops excess skills", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, cache)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			blockCount := strings.Count(systemContent, "<skill name=")
			Expect(blockCount).To(Equal(3))
		})
	})

	Context("when cache threads through to SelectSkills for byte-budget enforcement", func() {
		var (
			cache    *hook.SkillContentCache
			cacheDir string
		)

		BeforeEach(func() {
			var err error
			cacheDir, err = os.MkdirTemp("", "skill-budget-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(cacheDir) })

			oversizedDir := filepath.Join(cacheDir, "oversized-skill")
			Expect(os.MkdirAll(oversizedDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(oversizedDir, "SKILL.md"), []byte(strings.Repeat("X", 8000)), 0o600)).To(Succeed())

			smallDir := filepath.Join(cacheDir, "small-skill")
			Expect(os.MkdirAll(smallDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(smallDir, "SKILL.md"), []byte("# Small\nFits."), 0o600)).To(Succeed())

			cache = hook.NewSkillContentCache(cacheDir)
			Expect(cache.Init()).To(Succeed())

			manifest = agent.Manifest{
				ID:   "test-agent",
				Name: "Test Agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"oversized-skill", "small-skill"},
				},
			}
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:     []string{},
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 30000,
				PerSkillMaxBytes:   5120,
				CategoryMappings:   map[string][]string{},
				KeywordPatterns:    []hook.KeywordPattern{},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("drops oversized skills via PerSkillMaxBytes enforcement in SelectSkills", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, cache)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring(`<skill name="small-skill">`))
			Expect(systemContent).NotTo(ContainSubstring(`<skill name="oversized-skill">`))
		})
	})

	Context("when cache injects lean header alongside skill blocks", func() {
		var (
			cache    *hook.SkillContentCache
			cacheDir string
		)

		BeforeEach(func() {
			var err error
			cacheDir, err = os.MkdirTemp("", "skill-lean-header-test-*")
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(cacheDir) })

			skillDir := filepath.Join(cacheDir, "skill-a")
			Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Skill A\nContent."), 0o600)).To(Succeed())

			cache = hook.NewSkillContentCache(cacheDir)
			Expect(cache.Init()).To(Succeed())

			manifest = agent.Manifest{
				ID:   "test-agent",
				Name: "Test Agent",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"skill-a"},
				},
			}
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("prepends lean header before skill content blocks", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, cache)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("Your load_skills: ["))
			Expect(systemContent).To(ContainSubstring(`<skill name="skill-a">`))

			leanIdx := strings.Index(systemContent, "Your load_skills: [")
			blockIdx := strings.Index(systemContent, `<skill name="skill-a">`)
			Expect(leanIdx).To(BeNumerically("<", blockIdx))
		})
	})

})
