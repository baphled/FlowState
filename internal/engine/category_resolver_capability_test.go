package engine

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("CategoryResolver capability-aware promotion", func() {
	var (
		listed []provider.Model
		swaps  []CategoryModelSwap
		lister ModelLister
	)

	BeforeEach(func() {
		// Ordered ascending by context length so the 'fast' (pickSmallest)
		// strategy lands on claude-haiku-4.5 unless capability filtering
		// rules it out. The 'deep' (pickLargest) strategy lands on
		// claude-sonnet-4-6.
		listed = []provider.Model{
			{ID: "claude-haiku-4.5", Provider: "github-copilot", ContextLength: 100_000},
			{ID: "gpt-4o-mini", Provider: "github-copilot", ContextLength: 128_000},
			{ID: "claude-sonnet-4-6", Provider: "github-copilot", ContextLength: 1_000_000},
		}
		swaps = nil
		lister = func() ([]provider.Model, error) { return listed, nil }
	})

	recorder := func() SwapNotifier {
		return func(s CategoryModelSwap) { swaps = append(swaps, s) }
	}

	Context("when the strategy's first pick passes capability", func() {
		It("returns it unchanged and does not fire the notifier", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithToolCapability([]string{"claude-*"}, nil).
				WithSwapNotifier(recorder())

			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("claude-haiku-4.5"),
				"haiku is the smallest-context capable Claude model, the 'fast' strategy picks it")
			Expect(swaps).To(BeEmpty())
		})
	})

	Context("when the strategy's first pick is denied", func() {
		It("auto-promotes to the next-best capable model and fires the notifier", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithToolCapability(
					[]string{"claude-*", "gpt-4*"},
					[]string{"claude-haiku*", "gpt-*-mini"},
				).
				WithSwapNotifier(recorder())

			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("claude-sonnet-4-6"),
				"haiku and gpt-4o-mini are denied; sonnet is the only remaining capable model so 'fast' picks it")
			Expect(swaps).To(HaveLen(1))
			Expect(swaps[0].Category).To(Equal("quick"))
			Expect(swaps[0].Original).To(Equal("claude-haiku-4.5"))
			Expect(swaps[0].Chosen).To(Equal("claude-sonnet-4-6"))
			Expect(swaps[0].Reason).To(ContainSubstring(`tool-incapable pattern "claude-haiku*"`))
		})
	})

	Context("when capability filtering empties the candidate list", func() {
		It("falls through to the unfiltered pick (DelegateTool gate stays the fail-closed surface)", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithToolCapability(nil, []string{"claude-*", "gpt-*"}).
				WithSwapNotifier(recorder())

			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("claude-haiku-4.5"),
				"every model is denied; resolver returns the original pick rather than empty so the downstream gate produces a single coherent failure")
			Expect(swaps).To(BeEmpty(),
				"a swap fires only when the capability filter actually changed the result")
		})
	})

	Context("when no capability lists are configured", func() {
		It("preserves the pre-feature behaviour exactly", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithSwapNotifier(recorder())

			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("claude-haiku-4.5"))
			Expect(swaps).To(BeEmpty())
		})
	})

	Context("with no notifier wired", func() {
		It("performs the swap without panicking", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithToolCapability([]string{"claude-*"}, []string{"claude-haiku*"})

			cfg, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("claude-sonnet-4-6"))
		})
	})

	Context("denyReason precedence", func() {
		It("reports the deny pattern when one matches", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithToolCapability([]string{"claude-*"}, []string{"claude-haiku*"}).
				WithSwapNotifier(recorder())

			_, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(swaps[0].Reason).To(Equal(`matches tool-incapable pattern "claude-haiku*"`))
		})

		It("reports allowlist absence when no deny pattern matches but allow excludes the model", func() {
			resolver := NewCategoryResolver(nil).
				WithModelLister(lister).
				WithToolCapability([]string{"claude-sonnet*"}, nil).
				WithSwapNotifier(recorder())

			_, err := resolver.Resolve("quick")

			Expect(err).NotTo(HaveOccurred())
			Expect(swaps).To(HaveLen(1))
			Expect(swaps[0].Original).To(Equal("claude-haiku-4.5"))
			Expect(swaps[0].Chosen).To(Equal("claude-sonnet-4-6"))
			Expect(swaps[0].Reason).To(Equal("not in tool-capable allowlist"))
		})
	})

	Context("for the 'reasoning' strategy", func() {
		It("promotes from a denied largest pick to the next-largest capable one", func() {
			listed = []provider.Model{
				{ID: "small-allowed", Provider: "p", ContextLength: 8_000},
				{ID: "medium-allowed", Provider: "p", ContextLength: 32_000},
				{ID: "huge-denied", Provider: "p", ContextLength: 1_000_000},
			}
			resolver := NewCategoryResolver(nil).
				WithModelLister(func() ([]provider.Model, error) { return listed, nil }).
				WithToolCapability([]string{"*"}, []string{"huge-*"}).
				WithSwapNotifier(recorder())

			cfg, err := resolver.Resolve("deep")

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("medium-allowed"),
				"reasoning -> pickLargest; with huge-denied excluded, medium-allowed is the largest capable model")
			Expect(swaps).To(HaveLen(1))
			Expect(swaps[0].Original).To(Equal("huge-denied"))
		})
	})
})
