package engine_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/engine"
)

var _ = Describe("IsToolCapableModel", func() {
	var (
		allow []string
		deny  []string
	)

	BeforeEach(func() {
		allow = []string{"claude-*", "qwen3:*", "gpt-oss:20b", "devstral:latest"}
		deny = []string{"llama3.2*", "qwen2.5-coder*", "glm-4.7"}
	})

	It("matches allow-list glob suffix", func() {
		Expect(engine.IsToolCapableModel("anthropic", "claude-sonnet-4-20250514", allow, deny)).To(BeTrue())
		Expect(engine.IsToolCapableModel("ollama", "qwen3:8b", allow, deny)).To(BeTrue())
		Expect(engine.IsToolCapableModel("ollama", "qwen3:30b-a3b", allow, deny)).To(BeTrue())
	})

	It("matches allow-list literal entries", func() {
		Expect(engine.IsToolCapableModel("ollama", "gpt-oss:20b", allow, deny)).To(BeTrue())
		Expect(engine.IsToolCapableModel("ollama", "devstral:latest", allow, deny)).To(BeTrue())
	})

	It("rejects literal allow entry when the model has any extra suffix", func() {
		Expect(engine.IsToolCapableModel("ollama", "gpt-oss:20b-4k", allow, deny)).To(BeFalse())
		Expect(engine.IsToolCapableModel("ollama", "gpt-oss:20b-8k", allow, deny)).To(BeFalse())
	})

	It("treats deny-list as taking precedence over allow", func() {
		allowAll := append([]string{"*"}, allow...)
		Expect(engine.IsToolCapableModel("ollama", "llama3.2:latest", allowAll, deny)).To(BeFalse())
		Expect(engine.IsToolCapableModel("zai", "glm-4.7", allowAll, deny)).To(BeFalse())
		Expect(engine.IsToolCapableModel("ollama", "qwen2.5-coder:7b", allowAll, deny)).To(BeFalse())
	})

	It("returns false for unknown models (fail-closed)", func() {
		Expect(engine.IsToolCapableModel("ollama", "some-new-model:7b", allow, deny)).To(BeFalse())
	})

	It("returns false for nil/empty config (fail-closed)", func() {
		Expect(engine.IsToolCapableModel("anthropic", "claude-sonnet-4-20250514", nil, nil)).To(BeFalse())
		Expect(engine.IsToolCapableModel("anthropic", "claude-sonnet-4-20250514", []string{}, []string{})).To(BeFalse())
	})

	It("returns false for an empty model name", func() {
		Expect(engine.IsToolCapableModel("anthropic", "", allow, deny)).To(BeFalse())
	})

	It("ignores empty patterns inside the lists", func() {
		Expect(engine.IsToolCapableModel("anthropic", "claude-3-5-haiku", []string{"", "claude-*"}, nil)).To(BeTrue())
		Expect(engine.IsToolCapableModel("anthropic", "claude-3-5-haiku", []string{""}, nil)).To(BeFalse())
	})

	It("matches middle-glob deny patterns like gpt-*-mini", func() {
		denyMid := []string{"gpt-*-mini"}
		allowAny := []string{"*"}
		Expect(engine.IsToolCapableModel("github-copilot", "gpt-5-mini", allowAny, denyMid)).To(BeFalse())
		Expect(engine.IsToolCapableModel("github-copilot", "gpt-5.4-mini", allowAny, denyMid)).To(BeFalse())
		Expect(engine.IsToolCapableModel("github-copilot", "gpt-4o-mini", allowAny, denyMid)).To(BeFalse())
		Expect(engine.IsToolCapableModel("github-copilot", "gpt-5", allowAny, denyMid)).To(BeTrue())
		Expect(engine.IsToolCapableModel("github-copilot", "gpt-5.5", allowAny, denyMid)).To(BeTrue())
	})

	It("matches prefix-glob deny patterns like claude-haiku*", func() {
		denyHaiku := []string{"claude-haiku*"}
		Expect(engine.IsToolCapableModel("github-copilot", "claude-haiku-4.5", []string{"claude-*"}, denyHaiku)).To(BeFalse())
		Expect(engine.IsToolCapableModel("github-copilot", "claude-haiku-3-5", []string{"claude-*"}, denyHaiku)).To(BeFalse())
		Expect(engine.IsToolCapableModel("github-copilot", "claude-sonnet-4.6", []string{"claude-*"}, denyHaiku)).To(BeTrue())
		Expect(engine.IsToolCapableModel("github-copilot", "claude-opus-4.7", []string{"claude-*"}, denyHaiku)).To(BeTrue())
	})
})
