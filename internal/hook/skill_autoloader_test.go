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

		// Behaviour-Pinned: D1/Item 2 (Agent Runtime Quality plan, May 2026)
		// flips the lean injection from "Your load_skills:" prose to a
		// <system-reminder><available_skills> XML block. The prose was a
		// known hallucination vector (`task-tracker(...)` invented as a
		// tool call); the block + Claude Code anti-hallucination clause
		// pins the model to listed skills only.
		It("prepends a <system-reminder><available_skills> block to the system message", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(HavePrefix("<system-reminder>"))
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			Expect(systemContent).To(ContainSubstring(`<skill name="pre-action"`))
			Expect(systemContent).To(ContainSubstring(`<skill name="memory-keeper"`))
			Expect(systemContent).To(ContainSubstring(`Call skill_load(name="<exact-name>") to invoke.`))
			Expect(systemContent).To(ContainSubstring("Never guess or invent a skill name from training data"))
			Expect(systemContent).To(ContainSubstring("You are a helpful assistant."))
			// Behaviour-Pinned: the old prose format MUST NOT survive.
			Expect(systemContent).NotTo(ContainSubstring("Your load_skills:"))
			Expect(systemContent).NotTo(ContainSubstring("load when relevant"))
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

		It("creates a system message with the <available_skills> block", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			Expect(capturedRequest.Messages).To(HaveLen(2))
			Expect(capturedRequest.Messages[0].Role).To(Equal("system"))
			Expect(capturedRequest.Messages[0].Content).To(HavePrefix("<system-reminder>"))
			Expect(capturedRequest.Messages[0].Content).To(ContainSubstring("<available_skills>"))
			Expect(capturedRequest.Messages[0].Content).To(ContainSubstring(`Call skill_load(name="<exact-name>") to invoke.`))
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

		It("uses the <available_skills> system-reminder format", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("<system-reminder>"))
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			Expect(systemContent).To(ContainSubstring(`Call skill_load(name="<exact-name>") to invoke.`))
			for _, skillName := range config.BaselineSkills {
				Expect(systemContent).To(ContainSubstring(fmt.Sprintf(`<skill name=%q`, skillName)))
			}
			Expect(systemContent).To(ContainSubstring(`<skill name="clean-code"`))
		})
	})

	Context("when the system message already contains an <available_skills> block", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "<system-reminder>\n<available_skills>\n  <skill name=\"pre-action\" tier=\"session-start\" />\n</available_skills>\nCall skill_load(name=\"<exact-name>\") to invoke.\n</system-reminder>\n\nYou are a helpful assistant."},
					{Role: "user", Content: "follow-up after tool call"},
				},
			}
		})

		It("does not double-inject the block into the system message", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			occurrences := strings.Count(systemContent, "<available_skills>")
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
			Expect(systemContent).NotTo(ContainSubstring("<available_skills>"))
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

		It("injects skills as normal via the <available_skills> block", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			Expect(systemContent).To(ContainSubstring(`<skill name="pre-action"`))
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
			Expect(systemContent).NotTo(ContainSubstring("<available_skills>"))
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

	Context("when system message contains legacy 'Your load_skills:' prose but no <available_skills> block", func() {
		BeforeEach(func() {
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "Your load_skills: use skill_load when needed.\n\nYou are the planner agent."},
					{Role: "user", Content: "help me plan"},
				},
			}
		})

		// Behaviour-Pinned: the dedupe key is now the <available_skills>
		// XML marker — legacy "Your load_skills:" prose in a pre-existing
		// system message does NOT short-circuit injection, so the
		// dynamic block lands alongside whatever legacy prose the host
		// carried in. This is intentional: the new format is the source
		// of truth and the legacy prose is harmless leftover noise that
		// will age out as agent manifests rebuild.
		It("injects the <available_skills> block even when legacy prose is present", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("<available_skills>"),
				"dynamic injection should not be suppressed by legacy prose")
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
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			for _, skillName := range config.BaselineSkills {
				Expect(systemContent).To(ContainSubstring(fmt.Sprintf(`<skill name=%q tier="session-start"`, skillName)))
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
			Expect(systemContent).NotTo(ContainSubstring("<available_skills>"))
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

		It("falls back to <available_skills> lean injection when cache is nil", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			// The lean fallback emits structured <skill name="..." tier="..." />
			// entries inside <available_skills>; the cache-driven content
			// path emits free-standing <skill name="..."> blocks with content
			// bodies. The two are distinguishable by the trailing `/>` of the
			// self-closing form.
			Expect(systemContent).To(ContainSubstring(`<skill name="skill-a" tier=`))
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
			// D1/Item 2 (Agent Runtime Quality plan, May 2026): the lean
			// header now emits `<skill name="X" tier="..." />` entries for
			// every selected skill BEFORE the cache content blocks, so a
			// naive `<skill name=` count would double-count. Cache content
			// blocks use `<skill name="X">` (no `tier=` attribute, immediate
			// `>` after the closing quote); we count those specifically.
			contentBlockCount := strings.Count(systemContent, `">`+"\n")
			Expect(contentBlockCount).To(Equal(3))
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

		It("prepends the <available_skills> header before skill content blocks", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, cache)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			Expect(systemContent).To(ContainSubstring(`<skill name="skill-a">`))

			leanIdx := strings.Index(systemContent, "<available_skills>")
			// The cache-driven content block uses the same `<skill name=`
			// prefix as the lean fallback; pick a distinct opening match
			// via `<skill name="skill-a">\n# Skill A` (the content body
			// follows the open tag immediately under the cache path).
			blockIdx := strings.Index(systemContent, `<skill name="skill-a">`+"\n# Skill A")
			Expect(blockIdx).To(BeNumerically(">", 0))
			Expect(leanIdx).To(BeNumerically("<", blockIdx))
		})
	})

	Context("when all selected skills are already baked into the system prompt", func() {
		BeforeEach(func() {
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{"pre-action", "memory-keeper", "token-cost-estimation"},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			manifest = agent.Manifest{
				ID:         "test-agent",
				Name:       "Test Agent",
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

		It("still injects session-start tier entries for baseline skills even when all are baked", func() {
			baked := []string{"pre-action", "memory-keeper", "token-cost-estimation"}
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, baked, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring(`tier="session-start"`))
			Expect(systemContent).To(ContainSubstring(`<skill name="pre-action" tier="session-start"`))
			Expect(systemContent).To(ContainSubstring(`<skill name="memory-keeper" tier="session-start"`))
			Expect(systemContent).NotTo(ContainSubstring(`tier="contextual"`))
		})
	})

	Context("when only some selected skills are baked into the system prompt", func() {
		BeforeEach(func() {
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{"pre-action", "token-cost-estimation"},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns: []hook.KeywordPattern{
					{Pattern: "test", Skills: []string{"golang-testing"}},
				},
			}
			manifest = agent.Manifest{
				ID:         "test-agent",
				Name:       "Test Agent",
				Complexity: "quick",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{},
				},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "write a test for this"},
				},
			}
		})

		It("keeps baseline in the session-start tier and strips baked contextual entries from the contextual tier", func() {
			baked := []string{"pre-action", "token-cost-estimation"}
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, baked, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			// Baseline skills always appear with tier="session-start" even when baked
			Expect(systemContent).To(ContainSubstring(`<skill name="pre-action" tier="session-start"`))
			Expect(systemContent).To(ContainSubstring(`<skill name="token-cost-estimation" tier="session-start"`))
			// Contextual (keyword-matched) skill still injected under contextual tier
			Expect(systemContent).To(ContainSubstring(`<skill name="golang-testing" tier="contextual"`))
		})
	})

	Context("when bakedSkillNames is nil (backwards compatible)", func() {
		BeforeEach(func() {
			config = &hook.SkillAutoLoaderConfig{
				BaselineSkills:   []string{"pre-action", "memory-keeper"},
				MaxAutoSkills:    6,
				CategoryMappings: map[string][]string{},
				KeywordPatterns:  []hook.KeywordPattern{},
			}
			manifest = agent.Manifest{
				ID:         "test-agent",
				Name:       "Test Agent",
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

		It("injects every selected skill into the <available_skills> block", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			Expect(systemContent).To(ContainSubstring(`<skill name="pre-action"`))
			Expect(systemContent).To(ContainSubstring(`<skill name="memory-keeper"`))
		})
	})

	// D1/Item 2 (Agent Runtime Quality plan, May 2026) format pin —
	// asserts the exact <available_skills> XML shape so subsequent
	// edits cannot silently drift the format that the model has been
	// trained on.
	Context("when the manifest produces session-start + contextual skills", func() {
		BeforeEach(func() {
			config = hook.DefaultSkillAutoLoaderConfig()
			manifest = agent.Manifest{
				ID:         "format-pin-agent",
				Name:       "Format Pin",
				Complexity: "quick",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"clean-code"},
				},
			}
			request = &provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "system", Content: "You are a helpful assistant."},
					{Role: "user", Content: "Hello"},
				},
			}
		})

		It("emits the expected <system-reminder> + <available_skills> shape with tier attributes", func() {
			autoloader := hook.SkillAutoLoaderHook(config, func() agent.Manifest { return manifest }, nil, nil)
			wrapped := autoloader(passthrough)

			_, err := wrapped(ctx, request)
			Expect(err).NotTo(HaveOccurred())

			systemContent := capturedRequest.Messages[0].Content
			// Open tag pins
			Expect(systemContent).To(HavePrefix("<system-reminder>"))
			Expect(systemContent).To(ContainSubstring("<available_skills>"))
			Expect(systemContent).To(ContainSubstring("</available_skills>"))
			Expect(systemContent).To(ContainSubstring("</system-reminder>"))

			// Tier attribute pins
			Expect(systemContent).To(ContainSubstring(`<skill name="pre-action" tier="session-start" />`))
			Expect(systemContent).To(ContainSubstring(`<skill name="clean-code" tier="contextual" />`))

			// Anti-hallucination clause pin (verbatim Claude Code text)
			Expect(systemContent).To(ContainSubstring("Names are case-sensitive and must match exactly."))
			Expect(systemContent).To(ContainSubstring("Skills are NOT tools — do not attempt to call them directly."))
			Expect(systemContent).To(ContainSubstring("Only invoke a skill that appears in the <available_skills> list"))
			Expect(systemContent).To(ContainSubstring("Never guess or invent a skill name from training data"))

			// Behaviour-Pinned: legacy prose surface is gone.
			Expect(systemContent).NotTo(ContainSubstring("Your load_skills:"))
			Expect(systemContent).NotTo(ContainSubstring("Use skill_load(name) to invoke."))
			Expect(systemContent).NotTo(ContainSubstring("load when relevant:"))
			Expect(systemContent).NotTo(ContainSubstring("session start — invoke before first response"))
		})
	})
})

// Item 3 of the Agent Runtime Quality plan (May 2026) exposes the
// autoloader's catalogue so the engine's tool-not-found path can
// detect hallucinated skill-as-tool calls (`task-tracker`,
// `parallel-execution`) and redirect to skill_load. KnownSkills is
// the catalogue accessor — it returns the SUPERSET of every name
// the autoloader could surface for the given manifest, including
// keyword-pattern and category-mapping entries whose contextual
// tier may not be selected for any single request.
var _ = Describe("hook.KnownSkills", func() {
	var (
		cfg      *hook.SkillAutoLoaderConfig
		manifest agent.Manifest
	)

	BeforeEach(func() {
		cfg = &hook.SkillAutoLoaderConfig{
			BaselineSkills: []string{"pre-action", "memory-keeper"},
			KeywordPatterns: []hook.KeywordPattern{
				{Pattern: "test", Skills: []string{"golang-testing", "jest"}},
				{Pattern: "review", Skills: []string{"code-reviewer"}},
			},
			CategoryMappings: map[string][]string{
				"complex": {"architecture", "design-patterns"},
			},
		}
		manifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Capabilities: agent.Capabilities{
				AlwaysActiveSkills: []string{"clean-code", "pre-action"},
			},
		}
	})

	It("returns the union of baseline, agent always-active, keyword-pattern, and category-mapping skills", func() {
		result := hook.KnownSkills(cfg, manifest)

		// Baseline.
		Expect(result).To(ContainElements("pre-action", "memory-keeper"))
		// Agent always-active.
		Expect(result).To(ContainElement("clean-code"))
		// Keyword-pattern skills (every pattern, not just the ones that match a prompt).
		Expect(result).To(ContainElements("golang-testing", "jest", "code-reviewer"))
		// Category-mapping skills.
		Expect(result).To(ContainElements("architecture", "design-patterns"))
	})

	It("deduplicates names that appear in multiple sources", func() {
		// `pre-action` is in both BaselineSkills and AlwaysActiveSkills — must
		// appear exactly once in the output, otherwise the engine's redirect
		// loop would do redundant comparisons.
		result := hook.KnownSkills(cfg, manifest)

		count := 0
		for _, name := range result {
			if name == "pre-action" {
				count++
			}
		}
		Expect(count).To(Equal(1),
			"a name present in baseline + agent always-active must surface exactly once")
	})

	It("returns sorted output for deterministic ordering across calls", func() {
		// The engine's redirect path iterates the catalogue once per
		// tool-not-found event; stable ordering keeps log output
		// reproducible and lets goldens pin specific positions if any
		// downstream consumer needs it.
		result := hook.KnownSkills(cfg, manifest)
		Expect(result).To(BeAssignableToTypeOf([]string{}))

		for i := 1; i < len(result); i++ {
			Expect(result[i-1] <= result[i]).To(BeTrue(),
				"KnownSkills output must be sorted; got %q at %d > %q at %d",
				result[i-1], i-1, result[i], i)
		}
	})

	It("returns nil when neither cfg nor manifest provide any names", func() {
		result := hook.KnownSkills(nil, agent.Manifest{})
		Expect(result).To(BeNil(),
			"empty catalogue → nil, so engine's `for range knownSkillsFunc()` short-circuits cleanly")
	})

	It("tolerates a nil cfg by returning only the manifest's always-active skills", func() {
		result := hook.KnownSkills(nil, manifest)
		Expect(result).To(ConsistOf("clean-code", "pre-action"))
	})

	It("trims whitespace and discards empty names", func() {
		// Defensive against stray whitespace in YAML config — an empty
		// skill name would match an empty tool call, which the model
		// cannot legitimately emit but is worth guarding against.
		dirtyCfg := &hook.SkillAutoLoaderConfig{
			BaselineSkills: []string{"  spaced-skill  ", "", "valid"},
		}
		result := hook.KnownSkills(dirtyCfg, agent.Manifest{})
		Expect(result).To(ConsistOf("spaced-skill", "valid"))
	})
})
