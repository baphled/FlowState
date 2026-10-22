// Package config_test pins the QuotaConfig surface for PR2 of the
// Provider Quota and Spend Visibility plan (May 2026):
//
//   - QuotaPricingConfig (operator override + opt-in registry with B5
//     closure: enabled=true with empty URL is rejected at boot).
//   - QuotaCurrencyConfig (OD-6 conversion-table override path).
//   - ProviderQuotaConfig + ParseCap (per-deployment cap config —
//     plumbed in PR2, enforced in PR4).
//   - ValidateProviderQuota (boot-time per-provider validation).
//   - ValidatePricingRegistry (boot-time registry validation).
//
// Plan §"Pricing table" lines 338-388 + OD-6 lines 498-503 + OD-9
// lines 517-520 + line 390 (cap format).
package config_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("ParseCap (plan line 390 — \"<amount> <currency>\")", func() {
	It("parses \"50.00 USD\" as 5000 minor units + USD", func() {
		minor, currency, err := config.ParseCap("50.00 USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(int64(5000)),
			"50.00 USD MUST parse as 5000 cents — the plan demands minor-unit fidelity")
		Expect(currency).To(Equal("USD"))
	})

	It("parses integer-amount form \"50 USD\"", func() {
		minor, currency, err := config.ParseCap("50 USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(int64(5000)))
		Expect(currency).To(Equal("USD"))
	})

	It("parses single-decimal-place \"50.5 USD\" as 5050 cents", func() {
		minor, _, err := config.ParseCap("50.5 USD")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(int64(5050)),
			"single-decimal-place amount MUST be padded to two minor digits")
	})

	It("parses CNY for Z.AI per-deployment caps (OD-6 multi-currency)", func() {
		minor, currency, err := config.ParseCap("350.00 CNY")
		Expect(err).NotTo(HaveOccurred())
		Expect(minor).To(Equal(int64(35000)))
		Expect(currency).To(Equal("CNY"),
			"CNY caps support the Z.AI deployments per OD-6 (A1 per-model currency fold)")
	})

	It("normalises lowercase currency code to uppercase", func() {
		_, currency, err := config.ParseCap("50.00 usd")
		Expect(err).NotTo(HaveOccurred())
		Expect(currency).To(Equal("USD"))
	})

	It("rejects empty cap", func() {
		_, _, err := config.ParseCap("")
		Expect(err).To(MatchError(ContainSubstring("empty cap")))
	})

	It("rejects a single-token cap (no currency)", func() {
		_, _, err := config.ParseCap("50.00")
		Expect(err).To(MatchError(ContainSubstring("must be \"<amount> <currency>\"")))
	})

	It("rejects more than 2 decimal places (silent rounding is the plan's failure mode)", func() {
		_, _, err := config.ParseCap("50.001 USD")
		Expect(err).To(MatchError(ContainSubstring("more than 2 decimal places")),
			"3+ decimal places MUST reject — silent rounding violates the plan's honesty stance")
	})

	It("rejects negative amount", func() {
		_, _, err := config.ParseCap("-50.00 USD")
		Expect(err).To(HaveOccurred())
	})

	It("rejects non-3-letter currency", func() {
		_, _, err := config.ParseCap("50.00 USDOLLAR")
		Expect(err).To(MatchError(ContainSubstring("must be 3 letters")))
	})

	It("rejects currency containing digits", func() {
		_, _, err := config.ParseCap("50.00 US1")
		Expect(err).To(MatchError(ContainSubstring("must be 3 letters")))
	})

	It("rejects malformed amount (letters where digits expected)", func() {
		_, _, err := config.ParseCap("fifty USD")
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("ProviderQuotaConfig.ResolveThresholds (OD-9 defaults)", func() {
	It("applies 80/95 defaults when both fields are zero", func() {
		p := config.ProviderQuotaConfig{}
		amber, red := p.ResolveThresholds()
		Expect(amber).To(Equal(80),
			"OD-9 default amber threshold is 80% (plan line 519)")
		Expect(red).To(Equal(95),
			"OD-9 default red threshold is 95% (plan line 519)")
	})

	It("preserves explicit non-zero values", func() {
		p := config.ProviderQuotaConfig{ThresholdAmber: 60, ThresholdRed: 90}
		amber, red := p.ResolveThresholds()
		Expect(amber).To(Equal(60))
		Expect(red).To(Equal(90))
	})

	It("applies partial defaults — amber-only override", func() {
		p := config.ProviderQuotaConfig{ThresholdAmber: 70}
		amber, red := p.ResolveThresholds()
		Expect(amber).To(Equal(70))
		Expect(red).To(Equal(95),
			"missing red threshold MUST fall back to default 95")
	})
})

var _ = Describe("ProviderQuotaConfig.ResolvePeriod (monthly default)", func() {
	It("defaults to monthly when empty", func() {
		p := config.ProviderQuotaConfig{}
		Expect(p.ResolvePeriod()).To(Equal("monthly"),
			"period default is monthly per plan ProviderQuotaConfig.Period doc comment")
	})

	It("preserves explicit period", func() {
		p := config.ProviderQuotaConfig{Period: "rolling-30d"}
		Expect(p.ResolvePeriod()).To(Equal("rolling-30d"))
	})
})

var _ = Describe("ValidateProviderQuota (boot-time per-provider validation)", func() {
	It("accepts the canonical 50.00 USD + monthly config", func() {
		err := config.ValidateProviderQuota("anthropic", config.ProviderQuotaConfig{
			Cap:    "50.00 USD",
			Period: "monthly",
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts an empty config (no cap configured for this provider)", func() {
		err := config.ValidateProviderQuota("anthropic", config.ProviderQuotaConfig{})
		Expect(err).NotTo(HaveOccurred(),
			"empty per-provider config MUST validate — operators may configure caps for only some providers")
	})

	It("rejects malformed cap with provider id prefixed in the error", func() {
		err := config.ValidateProviderQuota("anthropic", config.ProviderQuotaConfig{
			Cap: "fifty bucks",
		})
		Expect(err).To(MatchError(ContainSubstring("quota.providers.anthropic.cap")))
	})

	It("rejects unknown period", func() {
		err := config.ValidateProviderQuota("openai", config.ProviderQuotaConfig{
			Period: "weekly",
		})
		Expect(err).To(MatchError(ContainSubstring("quota.providers.openai.period")))
	})

	It("rejects threshold_amber > 100", func() {
		err := config.ValidateProviderQuota("zai", config.ProviderQuotaConfig{
			ThresholdAmber: 150,
		})
		Expect(err).To(MatchError(ContainSubstring("threshold_amber")))
	})

	It("rejects threshold_red < 0", func() {
		err := config.ValidateProviderQuota("zai", config.ProviderQuotaConfig{
			ThresholdRed: -5,
		})
		Expect(err).To(MatchError(ContainSubstring("threshold_red")))
	})

	It("rejects amber >= red", func() {
		err := config.ValidateProviderQuota("zai", config.ProviderQuotaConfig{
			ThresholdAmber: 90,
			ThresholdRed:   80,
		})
		Expect(err).To(MatchError(ContainSubstring("threshold_amber (90) must be less than threshold_red (80)")))
	})

	It("accepts the documented rolling-30d period", func() {
		err := config.ValidateProviderQuota("openai", config.ProviderQuotaConfig{
			Period: "rolling-30d",
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts the documented session period", func() {
		err := config.ValidateProviderQuota("openai", config.ProviderQuotaConfig{
			Period: "session",
		})
		Expect(err).NotTo(HaveOccurred())
	})
})

var _ = Describe("ValidatePricingRegistry (B5 closure — no aspirational URLs)", func() {
	It("accepts enabled=false with empty URL (the v1 default)", func() {
		err := config.ValidatePricingRegistry(config.QuotaPricingRegistryConfig{
			Enabled: false,
		})
		Expect(err).NotTo(HaveOccurred(),
			"v1 default is enabled=false — fresh installs boot quietly with embedded prices, no warning")
	})

	It("rejects enabled=true with empty URL (B5 — no canonical FlowState URL ships)", func() {
		err := config.ValidatePricingRegistry(config.QuotaPricingRegistryConfig{
			Enabled: true,
			URL:     "",
		})
		Expect(err).To(MatchError(ContainSubstring("requires quota.pricing.registry.url")),
			"enabled=true + url=empty MUST fail-start (memory feedback_default_urls_must_be_provisioned_or_disabled)")
		Expect(err).To(MatchError(ContainSubstring("no aspirational URLs")),
			"the boot error MUST surface the B5 rationale so operators see why and how to fix")
	})

	It("rejects enabled=true with whitespace-only URL", func() {
		err := config.ValidatePricingRegistry(config.QuotaPricingRegistryConfig{
			Enabled: true,
			URL:     "   ",
		})
		Expect(err).To(HaveOccurred())
	})

	It("accepts enabled=true with a non-empty URL", func() {
		err := config.ValidatePricingRegistry(config.QuotaPricingRegistryConfig{
			Enabled: true,
			URL:     "https://my-prices.example/v1/models.json",
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("accepts enabled=false even with a URL set (informational)", func() {
		err := config.ValidatePricingRegistry(config.QuotaPricingRegistryConfig{
			Enabled: false,
			URL:     "https://my-prices.example/v1/models.json",
		})
		Expect(err).NotTo(HaveOccurred(),
			"enabled=false with URL set is informational — tier does not participate, no validation needed")
	})
})

var _ = Describe("DefaultQuotaConfig (PR2 fresh-install defaults)", func() {
	It("Pricing.Registry.Enabled defaults to false (B5 closure)", func() {
		def := config.DefaultQuotaConfig()
		Expect(def.Pricing.Registry.Enabled).To(BeFalse(),
			"v1 default is enabled=false — no aspirational URLs (memory feedback_default_urls_must_be_provisioned_or_disabled)")
	})

	It("Pricing.Path is empty (no operator override configured)", func() {
		def := config.DefaultQuotaConfig()
		Expect(def.Pricing.Path).To(BeEmpty())
	})

	It("Providers is empty (no caps configured by default)", func() {
		def := config.DefaultQuotaConfig()
		Expect(def.Providers).To(BeEmpty())
	})

	It("Currency.ConversionTable is empty (embedded table is the v1 baseline)", func() {
		def := config.DefaultQuotaConfig()
		Expect(def.Currency.ConversionTable).To(BeEmpty())
	})

	It("retains PR1 Store defaults (memory + single-instance)", func() {
		def := config.DefaultQuotaConfig()
		Expect(def.Store.Backend).To(Equal("memory"))
		Expect(def.Store.DeploymentTopology).To(Equal("single-instance"))
	})
})
