// Package pricing_test pins the three-tier Resolver contract from
// the Provider Quota and Spend Visibility plan (May 2026), §"Pricing
// table" lines 338-388.
package pricing_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/quota/pricing"
)

func TestPricing(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Provider Quota Pricing Suite")
}

var _ = Describe("Embedded pricing.json (plan §Pricing table line 346)", func() {
	It("LoadEmbedded parses without error", func() {
		t, err := pricing.LoadEmbedded()
		Expect(err).NotTo(HaveOccurred(),
			"the embedded pricing.json MUST parse cleanly — a build-time regression breaks every fresh install")
		Expect(t.Version).To(Equal("v1"))
		Expect(t.DefaultCurrency).To(Equal("USD"))
		Expect(t.Source).To(Equal(string(pricing.SourceEmbedded)),
			"the embedded table MUST stamp Source=flowstate-default-v1 so Snapshot.PricingSource surfaces the right tier in the panel")
	})

	It("ships the load-bearing Anthropic Opus 4.7 entry", func() {
		t, err := pricing.LoadEmbedded()
		Expect(err).NotTo(HaveOccurred())
		entry, ok := t.Models["anthropic/claude-opus-4-7"]
		Expect(ok).To(BeTrue(), "anthropic/claude-opus-4-7 MUST be in the embedded baseline — it's the default Opus tier surfaced via failover")
		Expect(entry.Currency).To(Equal("USD"))
		Expect(entry.InputPerMillion).To(BeNumerically(">", 0))
		Expect(entry.OutputPerMillion).To(BeNumerically(">", 0))
		Expect(entry.CacheReadPerMillion).To(BeNumerically(">", 0),
			"Anthropic entries SHOULD ship cache rates — the streaming pipe will need them in PR4")
	})

	It("ships Z.AI glm-4.6 in CNY (plan §A1 per-model currency fold)", func() {
		t, err := pricing.LoadEmbedded()
		Expect(err).NotTo(HaveOccurred())
		entry, ok := t.Models["zai/glm-4.6"]
		Expect(ok).To(BeTrue())
		Expect(entry.Currency).To(Equal("CNY"),
			"glm-4.6 MUST ship CNY per the plan's JSON example (lines 368-372) — a single deployment can talk to Anthropic-USD + Z.AI-CNY simultaneously")
	})
})

var _ = Describe("ParseTable validation (honesty-stance gate)", func() {
	It("rejects empty data", func() {
		_, err := pricing.ParseTable(nil)
		Expect(err).To(HaveOccurred())
	})

	It("rejects unsupported version", func() {
		_, err := pricing.ParseTable([]byte(`{"version":"v99","models":{"x/y":{"input_per_million":1,"output_per_million":2}}}`))
		Expect(err).To(MatchError(ContainSubstring("unsupported version")))
	})

	It("rejects empty models map", func() {
		_, err := pricing.ParseTable([]byte(`{"version":"v1","models":{}}`))
		Expect(err).To(MatchError(ContainSubstring("empty models")))
	})

	It("rejects non-positive input_per_million (silent-zero failure mode)", func() {
		_, err := pricing.ParseTable([]byte(`{"version":"v1","models":{"x/y":{"input_per_million":0,"output_per_million":1}}}`))
		Expect(err).To(MatchError(ContainSubstring("non-positive input_per_million")))
	})

	It("rejects non-positive output_per_million", func() {
		_, err := pricing.ParseTable([]byte(`{"version":"v1","models":{"x/y":{"input_per_million":1,"output_per_million":0}}}`))
		Expect(err).To(MatchError(ContainSubstring("non-positive output_per_million")))
	})

	It("backfills per-entry currency from default_currency (plan §A1 fold)", func() {
		t, err := pricing.ParseTable([]byte(`{
			"version":"v1",
			"default_currency":"GBP",
			"models":{
				"x/y":{"input_per_million":1,"output_per_million":2}
			}
		}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Models["x/y"].Currency).To(Equal("GBP"),
			"entry currency MUST inherit from table default_currency when omitted — plan §Pricing table line 377")
	})

	It("preserves per-entry currency when explicit (override beats default)", func() {
		t, err := pricing.ParseTable([]byte(`{
			"version":"v1",
			"default_currency":"USD",
			"models":{
				"zai/glm":{"currency":"CNY","input_per_million":5,"output_per_million":20}
			}
		}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Models["zai/glm"].Currency).To(Equal("CNY"))
	})

	It("defaults missing default_currency to USD", func() {
		t, err := pricing.ParseTable([]byte(`{"version":"v1","models":{"x/y":{"input_per_million":1,"output_per_million":2}}}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(t.DefaultCurrency).To(Equal("USD"))
	})
})

var _ = Describe("Three-tier Resolver precedence (plan §Pricing table lines 342-346)", func() {
	makeTable := func(source string, models map[string]pricing.Entry) pricing.Table {
		return pricing.Table{
			Version:         "v1",
			DefaultCurrency: "USD",
			Models:          models,
			Source:          source,
		}
	}

	Context("override beats registry beats embedded", func() {
		It("returns the override entry when all three tiers have the key", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 15, OutputPerMillion: 75},
			})
			registry := makeTable(pricing.SourceRegistryString("https://example.test/prices.json"), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 14, OutputPerMillion: 70},
			})
			override := makeTable(pricing.SourceOverrideString("/etc/flowstate/pricing.json"), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 13, OutputPerMillion: 65},
			})
			r := pricing.NewResolver(embedded, registry, override)
			entry, source, ok := r.Lookup("anthropic", "claude-opus-4-7")
			Expect(ok).To(BeTrue())
			Expect(entry.InputPerMillion).To(Equal(13.0),
				"override tier MUST win over registry and embedded — plan precedence is operator > registry > embedded")
			Expect(source).To(Equal("operator-override:/etc/flowstate/pricing.json"))
		})

		It("returns the registry entry when override misses but registry hits", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 15, OutputPerMillion: 75},
			})
			registry := makeTable(pricing.SourceRegistryString("https://example.test/prices.json"), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 14, OutputPerMillion: 70},
			})
			// Override has a different model only — partial override
			// per plan line 344.
			override := makeTable(pricing.SourceOverrideString("/etc/x.json"), map[string]pricing.Entry{
				"openai/gpt-4o": {Currency: "USD", InputPerMillion: 2.5, OutputPerMillion: 10},
			})
			r := pricing.NewResolver(embedded, registry, override)
			entry, source, ok := r.Lookup("anthropic", "claude-opus-4-7")
			Expect(ok).To(BeTrue())
			Expect(entry.InputPerMillion).To(Equal(14.0),
				"registry MUST win over embedded when override doesn't cover the key")
			Expect(source).To(Equal("registry:https://example.test/prices.json"))
		})

		It("returns the embedded entry when override and registry both miss", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 15, OutputPerMillion: 75},
			})
			r := pricing.NewResolver(embedded, pricing.Table{}, pricing.Table{})
			entry, source, ok := r.Lookup("anthropic", "claude-opus-4-7")
			Expect(ok).To(BeTrue())
			Expect(entry.InputPerMillion).To(Equal(15.0))
			Expect(source).To(Equal("flowstate-default-v1"))
		})

		It("returns (zero, empty, false) when no tier has the key (unknown-model path)", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 15, OutputPerMillion: 75},
			})
			r := pricing.NewResolver(embedded, pricing.Table{}, pricing.Table{})
			_, source, ok := r.Lookup("future-provider", "future-model")
			Expect(ok).To(BeFalse(),
				"unknown (provider, model) MUST return ok=false so the adapter surfaces NotConfigured{Reason:'unknown-model:<id>'} — plan §Pricing table line 388")
			Expect(source).To(BeEmpty())
		})
	})

	Context("Partial-override merge (plan §Pricing table line 344)", func() {
		It("override-only-for-key-A still falls through to embedded for key-B", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 15, OutputPerMillion: 75},
				"openai/gpt-4o":             {Currency: "USD", InputPerMillion: 2.5, OutputPerMillion: 10},
			})
			override := makeTable(pricing.SourceOverrideString("/etc/p.json"), map[string]pricing.Entry{
				"anthropic/claude-opus-4-7": {Currency: "USD", InputPerMillion: 13, OutputPerMillion: 65},
			})
			r := pricing.NewResolver(embedded, pricing.Table{}, override)

			// Key A overridden — Source = operator-override.
			_, srcA, okA := r.Lookup("anthropic", "claude-opus-4-7")
			Expect(okA).To(BeTrue())
			Expect(srcA).To(Equal("operator-override:/etc/p.json"))

			// Key B falls through to embedded.
			entryB, srcB, okB := r.Lookup("openai", "gpt-4o")
			Expect(okB).To(BeTrue())
			Expect(srcB).To(Equal("flowstate-default-v1"))
			Expect(entryB.InputPerMillion).To(Equal(2.5),
				"partial override means key-B still resolves via embedded — plan §Pricing table line 344 'merged-over-default, not strict-replace'")
		})
	})

	Context("Edge cases", func() {
		It("empty provider returns false (defensive)", func() {
			r := pricing.NewResolver(pricing.Table{
				Version: "v1", DefaultCurrency: "USD",
				Models: map[string]pricing.Entry{"x/y": {Currency: "USD", InputPerMillion: 1, OutputPerMillion: 2}},
			}, pricing.Table{}, pricing.Table{})
			_, _, ok := r.Lookup("", "y")
			Expect(ok).To(BeFalse())
		})

		It("empty model returns false (defensive)", func() {
			r := pricing.NewResolver(pricing.Table{
				Version: "v1", DefaultCurrency: "USD",
				Models: map[string]pricing.Entry{"x/y": {Currency: "USD", InputPerMillion: 1, OutputPerMillion: 2}},
			}, pricing.Table{}, pricing.Table{})
			_, _, ok := r.Lookup("x", "")
			Expect(ok).To(BeFalse())
		})

		It("SetRegistry hot-swaps the registry tier", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"x/y": {Currency: "USD", InputPerMillion: 1, OutputPerMillion: 2},
			})
			r := pricing.NewResolver(embedded, pricing.Table{}, pricing.Table{})

			// Before SetRegistry: embedded wins.
			_, srcBefore, _ := r.Lookup("x", "y")
			Expect(srcBefore).To(Equal("flowstate-default-v1"))

			// SetRegistry installs the new registry table.
			r.SetRegistry(pricing.Table{
				Version: "v1", DefaultCurrency: "USD",
				Models: map[string]pricing.Entry{
					"x/y": {Currency: "USD", InputPerMillion: 5, OutputPerMillion: 10},
				},
				Source: pricing.SourceRegistryString("https://refresh.test/prices.json"),
			})

			entryAfter, srcAfter, _ := r.Lookup("x", "y")
			Expect(srcAfter).To(Equal("registry:https://refresh.test/prices.json"),
				"SetRegistry MUST hot-swap so PR5/PR6 refresh tickers can update prices without rebuilding the Resolver")
			Expect(entryAfter.InputPerMillion).To(Equal(5.0))
		})

		It("HasModel mirrors Lookup's ok", func() {
			embedded := makeTable(string(pricing.SourceEmbedded), map[string]pricing.Entry{
				"x/y": {Currency: "USD", InputPerMillion: 1, OutputPerMillion: 2},
			})
			r := pricing.NewResolver(embedded, pricing.Table{}, pricing.Table{})
			Expect(r.HasModel("x", "y")).To(BeTrue())
			Expect(r.HasModel("x", "z")).To(BeFalse())
		})
	})
})

var _ = Describe("LoadOverride (operator-override file)", func() {
	It("returns zero Table + nil error for empty path (quiet success)", func() {
		t, err := pricing.LoadOverride("")
		Expect(err).NotTo(HaveOccurred(),
			"empty path is the 'no override configured' case — must NOT error per the YAML defaults")
		Expect(t.Models).To(BeEmpty())
	})

	It("loads and stamps Source=operator-override:<path>", func() {
		tmp := GinkgoT().TempDir()
		path := filepath.Join(tmp, "pricing.json")
		Expect(os.WriteFile(path, []byte(`{
			"version":"v1",
			"default_currency":"USD",
			"models":{
				"x/y":{"input_per_million":3,"output_per_million":15}
			}
		}`), 0o600)).To(Succeed())

		t, err := pricing.LoadOverride(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(t.Source).To(Equal("operator-override:" + path))
		Expect(t.Models["x/y"].InputPerMillion).To(Equal(3.0))
	})

	It("propagates parse errors with file context", func() {
		tmp := GinkgoT().TempDir()
		path := filepath.Join(tmp, "bad.json")
		Expect(os.WriteFile(path, []byte(`not json`), 0o600)).To(Succeed())

		_, err := pricing.LoadOverride(path)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("parsing override file"))
	})

	It("propagates filesystem-read errors (path not found)", func() {
		_, err := pricing.LoadOverride("/nonexistent/path/does/not/exist.json")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("reading override file"))
	})
})

var _ = Describe("Sourced adapter (quota.PricingResolver seam)", func() {
	It("Lookup returns the same (source, ok) as Resolver.SourceLookup", func() {
		r := pricing.NewResolver(pricing.Table{
			Version: "v1", DefaultCurrency: "USD",
			Models: map[string]pricing.Entry{
				"x/y": {Currency: "USD", InputPerMillion: 1, OutputPerMillion: 2},
			},
			Source: string(pricing.SourceEmbedded),
		}, pricing.Table{}, pricing.Table{})
		s := pricing.Sourced{Resolver: r}
		src, ok := s.Lookup("x", "y")
		Expect(ok).To(BeTrue())
		Expect(src).To(Equal("flowstate-default-v1"))
	})

	It("Lookup returns (\"\", false) for nil-Resolver embedding (defensive)", func() {
		s := pricing.Sourced{}
		src, ok := s.Lookup("x", "y")
		Expect(ok).To(BeFalse())
		Expect(src).To(BeEmpty())
	})

	It("SourceLookup discards Entry but preserves ok signal", func() {
		r := pricing.NewResolver(pricing.Table{
			Version: "v1", DefaultCurrency: "USD",
			Models: map[string]pricing.Entry{
				"x/y": {Currency: "USD", InputPerMillion: 1, OutputPerMillion: 2},
			},
			Source: string(pricing.SourceEmbedded),
		}, pricing.Table{}, pricing.Table{})

		src, ok := r.SourceLookup("x", "y")
		Expect(ok).To(BeTrue())
		Expect(src).To(Equal("flowstate-default-v1"))

		src, ok = r.SourceLookup("x", "missing")
		Expect(ok).To(BeFalse())
		Expect(src).To(BeEmpty())
	})
})

var _ = Describe("Source audit-trail constants (plan §Pricing table line 386)", func() {
	It("SourceRegistryString formats registry:<url>", func() {
		Expect(pricing.SourceRegistryString("https://pricing.example/v1/models.json")).
			To(Equal("registry:https://pricing.example/v1/models.json"))
	})

	It("SourceOverrideString formats operator-override:<path>", func() {
		Expect(pricing.SourceOverrideString("/etc/flowstate/pricing.json")).
			To(Equal("operator-override:/etc/flowstate/pricing.json"))
	})

	It("SourceEmbedded is the v1-pinned constant the panel reads verbatim", func() {
		Expect(string(pricing.SourceEmbedded)).To(Equal("flowstate-default-v1"))
	})

	It("CanonicalKey formats <provider>/<model>", func() {
		Expect(pricing.CanonicalKey("anthropic", "claude-opus-4-7")).To(Equal("anthropic/claude-opus-4-7"))
	})
})
