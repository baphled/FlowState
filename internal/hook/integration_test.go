package hook_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("SkillAutoLoaderHook E2E", Label("integration"), func() {
	var (
		cfg           *hook.SkillAutoLoaderConfig
		testManifest  agent.Manifest
		capturedReq   *provider.ChatRequest
		handlerCalled bool
	)

	BeforeEach(func() {
		defaultCfg := hook.DefaultSkillAutoLoaderConfig()
		cfg = &hook.SkillAutoLoaderConfig{
			BaselineSkills:   defaultCfg.BaselineSkills,
			MaxAutoSkills:    4,
			CategoryMappings: map[string][]string{},
			KeywordPatterns: []hook.KeywordPattern{
				{Pattern: "golang", Skills: []string{"golang", "ginkgo-gomega"}},
				{Pattern: "database", Skills: []string{"golang-database"}},
			},
		}
		testManifest = agent.Manifest{
			ID:         "test-agent",
			Complexity: "deep",
			Capabilities: agent.Capabilities{
				AlwaysActiveSkills: []string{"clean-code"},
			},
		}
		capturedReq = nil
		handlerCalled = false
	})

	runHook := func(userMsg string) *provider.ChatRequest {
		req := &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: userMsg},
			},
		}
		h := hook.SkillAutoLoaderHook(cfg, func() agent.Manifest { return testManifest }, nil, nil)
		chain := hook.NewChain(h)
		handler := chain.Execute(func(ctx context.Context, r *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			capturedReq = r
			handlerCalled = true
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
		return capturedReq
	}

	Context("when injecting baseline skills", func() {
		It("injects baseline skills into system prompt", func() {
			req := runHook("Hello")
			for _, skill := range cfg.BaselineSkills {
				Expect(req.Messages[0].Content).To(ContainSubstring(skill))
			}
		})

		It("injects lean format with load_skills directive", func() {
			req := runHook("Hello")
			Expect(req.Messages[0].Content).To(ContainSubstring("Your load_skills:"))
			Expect(req.Messages[0].Content).To(ContainSubstring("Use skill_load(name) to invoke."))
		})
	})

	Context("when matching keyword patterns", func() {
		It("adds keyword-matched skills for a Go prompt", func() {
			req := runHook("I need help with golang testing")
			Expect(req.Messages[0].Content).To(ContainSubstring("golang"))
		})

		It("does not add keyword skills for an unrelated prompt", func() {
			req := runHook("Tell me about cooking")
			Expect(req.Messages[0].Content).NotTo(ContainSubstring("golang-database"))
		})
	})

	Context("when enforcing MaxAutoSkills cap", func() {
		It("limits non-baseline skills to the cap", func() {
			req := runHook("golang database testing")
			content := req.Messages[0].Content

			// Non-baseline skills appear in the "load when relevant: [...]" tier.
			const marker = "load when relevant: ["
			idx := strings.Index(content, marker)
			if idx == -1 {
				// No contextual skills injected — cap is trivially respected.
				return
			}
			closeIdx := strings.Index(content[idx:], "]")
			Expect(closeIdx).NotTo(Equal(-1))
			skillList := content[idx+len(marker) : idx+closeIdx]
			skills := strings.Split(skillList, ", ")
			nonBaselineCount := len(skills)
			Expect(nonBaselineCount).To(BeNumerically("<=", cfg.MaxAutoSkills))
		})
	})

	Context("when passing through to the handler", func() {
		It("calls the next handler in the chain", func() {
			runHook("Hello")
			Expect(handlerCalled).To(BeTrue())
		})
	})
})
