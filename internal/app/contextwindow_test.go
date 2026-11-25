package app

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// intp returns a pointer to n.
func intp(n int) *int { return &n }

// windowOf resolves a window with no profile and no config.
func windowOf(providerName, model string) ContextWindow {
	tokens, source := ResolveContextWindow(providerName, model, 0, CompactionConfig{})
	return ContextWindow{Tokens: tokens, Source: source}
}

var _ = Describe("CompactionConfig.Resolved", func() {
	It("enables compaction by default", func() {
		Expect(CompactionConfig{}.Resolved().Enabled).To(BeTrue())
	})

	It("defaults the reserve to 16384", func() {
		Expect(CompactionConfig{}.Resolved().Reserve).To(Equal(16384))
	})

	It("defaults keep_recent to 20000", func() {
		Expect(CompactionConfig{}.Resolved().KeepRecent).To(Equal(20000))
	})

	It("configures no window by default", func() {
		Expect(CompactionConfig{}.Resolved().ContextWindow).To(Equal(0))
	})

	It("keeps a configured reserve", func() {
		Expect(CompactionConfig{Reserve: intp(500)}.Resolved().Reserve).To(Equal(500))
	})

	It("keeps enabled false", func() {
		off := false
		Expect(CompactionConfig{Enabled: &off}.Resolved().Enabled).To(BeFalse())
	})
})

var _ = DescribeTable("CompactionConfig.Validate rejects",
	func(cfg CompactionConfig, key string) {
		Expect(cfg.Validate()).To(MatchError(ContainSubstring(key)))
	},
	Entry("a zero reserve", CompactionConfig{Reserve: intp(0)}, "compaction.reserve"),
	Entry("a negative reserve", CompactionConfig{Reserve: intp(-1)}, "compaction.reserve"),
	Entry("a zero keep_recent", CompactionConfig{KeepRecent: intp(0)}, "compaction.keep_recent"),
	Entry("a zero window", CompactionConfig{ContextWindow: intp(0)}, "compaction.context_window"),
	Entry("an absurd window", CompactionConfig{ContextWindow: intp(100_000_001)}, "compaction.context_window"),
)

var _ = Describe("CompactionConfig.Validate messages", func() {
	It("names the maximum for a window", func() {
		Expect(CompactionConfig{ContextWindow: intp(0)}.Validate()).To(MatchError(ContainSubstring("(at most 100000000)")))
	})

	It("names no maximum for a reserve", func() {
		Expect(CompactionConfig{Reserve: intp(0)}.Validate()).NotTo(MatchError(ContainSubstring("at most")))
	})

	It("names no maximum for keep_recent", func() {
		Expect(CompactionConfig{KeepRecent: intp(0)}.Validate()).NotTo(MatchError(ContainSubstring("at most")))
	})
})

var _ = Describe("CompactionConfig.Validate", func() {
	It("accepts positive values", func() {
		Expect(CompactionConfig{Reserve: intp(1), KeepRecent: intp(1), ContextWindow: intp(1)}.Validate()).To(Succeed())
	})

	It("accepts the empty object", func() {
		Expect(CompactionConfig{}.Validate()).To(Succeed())
	})
})

var _ = Describe("ResolveContextWindow", func() {
	It("prefers the profile to the config", func() {
		tokens, _ := ResolveContextWindow("anthropic", "claude-sonnet-5-5", 111, CompactionConfig{ContextWindow: intp(222)})
		Expect(tokens).To(Equal(111))
	})

	It("names the profile as the source", func() {
		_, source := ResolveContextWindow("anthropic", "claude-sonnet-5-5", 111, CompactionConfig{ContextWindow: intp(222)})
		Expect(source).To(Equal("profile"))
	})

	It("prefers the config to the table", func() {
		tokens, _ := ResolveContextWindow("anthropic", "claude-sonnet-5-5", 0, CompactionConfig{ContextWindow: intp(222)})
		Expect(tokens).To(Equal(222))
	})

	It("names the config as the source", func() {
		_, source := ResolveContextWindow("anthropic", "claude-sonnet-5-5", 0, CompactionConfig{ContextWindow: intp(222)})
		Expect(source).To(Equal("config"))
	})

	It("reads the table for an exact anthropic model", func() {
		Expect(windowOf("anthropic", "claude-sonnet-4-5")).To(Equal(ContextWindow{Tokens: 200000, Source: "table"}))
	})

	It("reads the table for the zai alias", func() {
		tokens, _ := ResolveContextWindow("zai", "glm-4.7", 0, CompactionConfig{})
		Expect(tokens).To(Equal(204800))
	})

	It("lets a config window serve a model the table lacks", func() {
		_, source := ResolveContextWindow("ollama", "qwen2.5-coder:7b", 0, CompactionConfig{ContextWindow: intp(32768)})
		Expect(source).To(Equal("config"))
	})
})

var _ = DescribeTable("ResolveContextWindow is unknown without a profile or config for",
	func(providerName, model string) {
		Expect(windowOf(providerName, model)).To(Equal(ContextWindow{Source: "unknown"}))
	},
	Entry("an id with a suffix the table lacks", "anthropic", "claude-sonnet-5-5-extra"),
	Entry("a dated id the table lacks", "anthropic", "claude-sonnet-5-5-20990101"),
	Entry("an id with a provider prefix", "anthropic", "anthropic/claude-sonnet-5-5"),
	Entry("a prefix of a listed id", "anthropic", "claude-sonnet"),
	Entry("a z.ai model the registry lacks", "z.ai", "glm-4.5-flash"),
	Entry("ollama", "ollama", "qwen2.5-coder:7b"),
	Entry("openrouter", "openrouter", "claude-sonnet-5-5"),
	Entry("an empty model", "anthropic", ""),
	Entry("an upper-case z.ai id", "z.ai", "GLM-5.3"),
	Entry("a mixed-case z.ai id", "z.ai", "Glm-5-Turbo"),
	Entry("an upper-case anthropic id", "anthropic", "Claude-Sonnet-5-5"),
	Entry("an upper-case provider", "Anthropic", "claude-sonnet-5-5"),
)

// The expected numbers are hard-coded from Pi's model registry (see windowTable)
// so a changed or mistyped table entry fails here without reading outside the repo.
var _ = DescribeTable("the window table holds the registry value for",
	func(providerName, model string, want int) {
		tokens, _ := ResolveContextWindow(providerName, model, 0, CompactionConfig{})
		Expect(tokens).To(Equal(want))
	},
	Entry("claude-haiku-4-5", "anthropic", "claude-haiku-4-5", 200000),
	Entry("claude-haiku-4-5-20251001", "anthropic", "claude-haiku-4-5-20251001", 200000),
	Entry("claude-opus-4-6", "anthropic", "claude-opus-4-6", 1000000),
	Entry("claude-opus-5-5", "anthropic", "claude-opus-5-5", 1000000),
	Entry("claude-sonnet-5-5", "anthropic", "claude-sonnet-5-5", 1000000),
	Entry("glm-4.7", "z.ai", "glm-4.7", 204800),
	Entry("glm-5-turbo", "z.ai", "glm-5-turbo", 200000),
	Entry("glm-5.3", "z.ai", "glm-5.3", 1000000),
	Entry("glm-5.3-flash", "z.ai", "glm-5.3-flash", 1000000),
)
