// Package engine_test — Summariser Model Routing Contract
//
// This file is the authoritative specification for how summarisation
// workloads are routed through the model category table. It mirrors the
// shape of agent_model_contract_test.go, but keyed on the agent's
// ContextManagement.SummaryTier rather than Manifest.Complexity.
//
// If you change how the summariser selects a model, update this table.
// The non-negotiable invariant — enforced by the regression guard below —
// is that the summariser route goes through CategoryResolver, not the
// bound chat provider. See [[ADR - Agent Model Contract]].
package engine_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
)

// summariserContract describes the expected routing for one summary tier.
type summariserContract struct {
	tier               string
	expectedDescriptor string
}

// summariserContracts is the authoritative mapping of every supported
// summary tier to the abstract model descriptor it must resolve to.
var summariserContracts = []summariserContract{
	{tier: "quick", expectedDescriptor: "fast"},
	{tier: "deep", expectedDescriptor: "reasoning"},
}

var _ = Describe("Summariser Model Routing Contract", Label("integration", "contract"), func() {
	Describe("summary_tier resolves through CategoryResolver", func() {
		DescribeTable("tier → descriptor",
			func(contract summariserContract) {
				category := engine.NewCategoryResolver(nil)
				summariser := engine.NewSummariserResolver(category)
				manifest := &agent.Manifest{
					ContextManagement: agent.ContextManagement{SummaryTier: contract.tier},
				}

				cfg, err := summariser.ResolveForManifest(manifest)

				Expect(err).NotTo(HaveOccurred())
				Expect(cfg.Model).To(Equal(contract.expectedDescriptor))
			},
			Entry("quick tier routes to fast", summariserContracts[0]),
			Entry("deep tier routes to reasoning", summariserContracts[1]),
		)
	})

	Describe("summary tier defaulting", func() {
		It("defaults an empty SummaryTier to 'quick'", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)
			manifest := &agent.Manifest{
				ContextManagement: agent.ContextManagement{SummaryTier: ""},
			}

			cfg, err := summariser.ResolveForManifest(manifest)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})

		It("defaults a nil-manifest-equivalent (zero manifest) to 'quick'", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)

			cfg, err := summariser.ResolveForManifest(&agent.Manifest{})

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"))
		})
	})

	Describe("error propagation", func() {
		It("propagates the CategoryResolver error for an unknown tier", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)
			manifest := &agent.Manifest{
				ContextManagement: agent.ContextManagement{SummaryTier: "no-such-tier"},
			}

			_, err := summariser.ResolveForManifest(manifest)

			Expect(err).To(HaveOccurred())
		})

		It("returns an error when manifest is nil (defensive guard)", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)

			_, err := summariser.ResolveForManifest(nil)

			Expect(err).To(HaveOccurred())
		})

		It("returns an error when the category resolver is nil", func() {
			summariser := engine.NewSummariserResolver(nil)

			_, err := summariser.ResolveForManifest(&agent.Manifest{})

			Expect(err).To(HaveOccurred())
		})
	})

	Describe("override plumbing", func() {
		It("routes quick tier through user overrides when present", func() {
			overrides := map[string]engine.CategoryConfig{
				"quick": {Model: "user-override-model", Provider: "openai"},
			}
			category := engine.NewCategoryResolver(overrides)
			summariser := engine.NewSummariserResolver(category)
			manifest := &agent.Manifest{
				ContextManagement: agent.ContextManagement{SummaryTier: "quick"},
			}

			cfg, err := summariser.ResolveForManifest(manifest)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("user-override-model"))
			Expect(cfg.Provider).To(Equal("openai"))
		})
	})

	Describe("regression guard — routes through CategoryResolver, not chat provider", func() {
		// The binding constraint: model selection MUST happen via
		// CategoryResolver, keyed on a tier descriptor. The following test
		// would fail if someone rewired SummariserResolver to bypass the
		// category table (e.g. by pinning to a fixed provider name or
		// reading from the manifest's chat complexity field).
		It("uses ContextManagement.SummaryTier, not Manifest.Complexity, as the routing key", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)

			// A "deep" chat complexity agent with a "quick" summary tier
			// MUST still summarise via the 'quick' route ("fast" descriptor),
			// not via the chat route ("reasoning" descriptor).
			manifest := &agent.Manifest{
				Complexity: "deep",
				ContextManagement: agent.ContextManagement{
					SummaryTier: "quick",
				},
			}

			cfg, err := summariser.ResolveForManifest(manifest)

			Expect(err).NotTo(HaveOccurred())
			Expect(cfg.Model).To(Equal("fast"),
				"summariser must key on SummaryTier, not Complexity")
			Expect(cfg.Model).NotTo(Equal("reasoning"),
				"summariser must not route via the chat-tier descriptor")
		})

		It("returns config equivalent to CategoryResolver.Resolve(tier) directly", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)
			manifest := &agent.Manifest{
				ContextManagement: agent.ContextManagement{SummaryTier: "deep"},
			}

			viaSummariser, summariserErr := summariser.ResolveForManifest(manifest)
			viaCategory, categoryErr := category.Resolve("deep")

			Expect(summariserErr).NotTo(HaveOccurred())
			Expect(categoryErr).NotTo(HaveOccurred())
			Expect(viaSummariser).To(Equal(viaCategory),
				"SummariserResolver must be a pure adapter over CategoryResolver")
		})
	})

	Describe("sentinel error exposure", func() {
		It("exposes ErrNilCategoryResolver for the nil-resolver path", func() {
			summariser := engine.NewSummariserResolver(nil)

			_, err := summariser.ResolveForManifest(&agent.Manifest{})

			Expect(errors.Is(err, engine.ErrNilCategoryResolver)).To(BeTrue())
		})

		It("exposes ErrNilManifest for the nil-manifest path", func() {
			category := engine.NewCategoryResolver(nil)
			summariser := engine.NewSummariserResolver(category)

			_, err := summariser.ResolveForManifest(nil)

			Expect(errors.Is(err, engine.ErrNilManifest)).To(BeTrue())
		})
	})
})
