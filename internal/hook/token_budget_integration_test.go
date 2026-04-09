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
	fscontext "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/hook"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Token/Byte Budget Enforcement", Label("integration"), func() {

	writeSkillFile := func(dir, name string, sizeBytes int) {
		skillDir := filepath.Join(dir, name)
		Expect(os.MkdirAll(skillDir, 0o755)).To(Succeed())
		content := strings.Repeat("x", sizeBytes)
		Expect(os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600)).To(Succeed())
	}

	runHookWithCache := func(
		cfg *hook.SkillAutoLoaderConfig,
		manifest agent.Manifest,
		cache *hook.SkillContentCache,
		userMsg string,
	) *provider.ChatRequest {
		req := &provider.ChatRequest{
			Messages: []provider.Message{
				{Role: "system", Content: "You are helpful."},
				{Role: "user", Content: userMsg},
			},
		}
		var captured *provider.ChatRequest
		passthrough := func(_ context.Context, r *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			captured = r
			ch := make(chan provider.StreamChunk, 1)
			ch <- provider.StreamChunk{Done: true}
			close(ch)
			return ch, nil
		}
		hookFn := hook.SkillAutoLoaderHook(cfg, func() agent.Manifest { return manifest }, nil, cache)
		wrapped := hookFn(passthrough)
		resultChan, err := wrapped(context.Background(), req)
		Expect(err).NotTo(HaveOccurred())
		for v := range resultChan {
			_ = v
		}
		return captured
	}

	Context("ceiling enforcement", func() {
		It("drops skills that would exceed MaxAutoSkillsBytes", func() {
			skillDir := GinkgoT().TempDir()
			skillNames := []string{"skill-a", "skill-b", "skill-c", "skill-d", "skill-e", "skill-f"}
			for _, name := range skillNames {
				writeSkillFile(skillDir, name, 7000)
			}

			cache := hook.NewSkillContentCache(skillDir)
			Expect(cache.Init()).To(Succeed())

			cfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills:     []string{},
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 35840,
				PerSkillMaxBytes:   0,
				CategoryMappings:   map[string][]string{},
				KeywordPatterns:    []hook.KeywordPattern{},
			}

			manifest := agent.Manifest{
				ID:         "test-agent",
				Complexity: "deep",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: skillNames,
				},
			}

			captured := runHookWithCache(cfg, manifest, cache, "test message")
			systemContent := captured.Messages[0].Content

			Expect(strings.Count(systemContent, "<skill name=")).To(Equal(5))
			Expect(systemContent).NotTo(ContainSubstring(`<skill name="skill-f"`))

			var totalContentBytes int
			for _, name := range skillNames {
				if strings.Contains(systemContent, fmt.Sprintf(`<skill name=%q>`, name)) {
					totalContentBytes += cache.ByteSize(name)
				}
			}
			Expect(totalContentBytes).To(BeNumerically("<=", cfg.MaxAutoSkillsBytes))
		})
	})

	Context("per-skill cap", func() {
		It("excludes skills exceeding PerSkillMaxBytes", func() {
			skillDir := GinkgoT().TempDir()
			writeSkillFile(skillDir, "small-skill", 1000)
			writeSkillFile(skillDir, "big-skill", 10000)

			cache := hook.NewSkillContentCache(skillDir)
			Expect(cache.Init()).To(Succeed())

			cfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills:     []string{},
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 20000,
				PerSkillMaxBytes:   5120,
				CategoryMappings:   map[string][]string{},
				KeywordPatterns:    []hook.KeywordPattern{},
			}

			manifest := agent.Manifest{
				ID:         "test-agent",
				Complexity: "deep",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{"small-skill", "big-skill"},
				},
			}

			captured := runHookWithCache(cfg, manifest, cache, "test message")
			systemContent := captured.Messages[0].Content

			Expect(systemContent).To(ContainSubstring(`<skill name="small-skill"`))
			Expect(systemContent).NotTo(ContainSubstring(`<skill name="big-skill"`))
		})
	})

	Context("token reduction vs unbounded baseline", func() {
		It("bounds system message tokens within the ceiling estimate", func() {
			skillDir := GinkgoT().TempDir()
			skillNames := []string{"token-a", "token-b", "token-c", "token-d", "token-e"}
			for _, name := range skillNames {
				writeSkillFile(skillDir, name, 3000)
			}

			cache := hook.NewSkillContentCache(skillDir)
			Expect(cache.Init()).To(Succeed())

			cfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills:     []string{},
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 8000,
				PerSkillMaxBytes:   0,
				CategoryMappings:   map[string][]string{},
				KeywordPatterns:    []hook.KeywordPattern{},
			}

			manifest := agent.Manifest{
				ID:         "test-agent",
				Complexity: "deep",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: skillNames,
				},
			}

			captured := runHookWithCache(cfg, manifest, cache, "test message")
			systemContent := captured.Messages[0].Content

			counter := fscontext.NewTiktokenCounter()
			boundedTokens := counter.Count(systemContent)

			maxTokenEstimate := (cfg.MaxAutoSkillsBytes / 4) + 500
			Expect(boundedTokens).To(BeNumerically("<=", maxTokenEstimate))

			var unboundedBuilder strings.Builder
			unboundedBuilder.WriteString("You are helpful.\n")
			for _, name := range skillNames {
				content, _ := cache.GetContent(name)
				fmt.Fprintf(&unboundedBuilder, "<skill name=%q>\n%s\n</skill>\n", name, content)
			}
			unboundedTokens := counter.Count(unboundedBuilder.String())

			reduction := 1.0 - (float64(boundedTokens) / float64(unboundedTokens))
			Expect(reduction).To(BeNumerically(">=", 0.30))
		})
	})

	Context("baseline skills always injected", func() {
		It("injects baseline skills regardless of ceiling", func() {
			skillDir := GinkgoT().TempDir()
			baselineNames := []string{"base-a", "base-b", "base-c"}
			for _, name := range baselineNames {
				writeSkillFile(skillDir, name, 5000)
			}

			cache := hook.NewSkillContentCache(skillDir)
			Expect(cache.Init()).To(Succeed())

			cfg := &hook.SkillAutoLoaderConfig{
				BaselineSkills:     baselineNames,
				MaxAutoSkills:      10,
				MaxAutoSkillsBytes: 100,
				PerSkillMaxBytes:   0,
				CategoryMappings:   map[string][]string{},
				KeywordPatterns:    []hook.KeywordPattern{},
			}

			manifest := agent.Manifest{
				ID:         "test-agent",
				Complexity: "deep",
				Capabilities: agent.Capabilities{
					AlwaysActiveSkills: []string{},
				},
			}

			captured := runHookWithCache(cfg, manifest, cache, "test message")
			systemContent := captured.Messages[0].Content

			for _, name := range baselineNames {
				Expect(systemContent).To(ContainSubstring(fmt.Sprintf(`<skill name=%q>`, name)))
			}
		})
	})
})
