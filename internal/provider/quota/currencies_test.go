package quota_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/quota"
)

var _ = Describe("Currency conversion table (plan OD-6 lines 498-503)", func() {
	Describe("DefaultConversionTable", func() {
		It("returns a table with USD/CNY/EUR/GBP — the v1 currencies named in OD-6", func() {
			t := quota.DefaultConversionTable()
			Expect(t.Rates).To(HaveKey("USD"))
			Expect(t.Rates).To(HaveKey("CNY"))
			Expect(t.Rates).To(HaveKey("EUR"))
			Expect(t.Rates).To(HaveKey("GBP"),
				"v1 currencies are USD/CNY/EUR/GBP per OD-6 — any addition is a quarterly refresh PR")
		})

		It("USD identity rate is exactly 1.0", func() {
			t := quota.DefaultConversionTable()
			Expect(t.Rates["USD"]).To(Equal(1.0),
				"USD MUST be the identity entry — every conversion is expressed as 'units per 1 USD'")
		})

		It("stamps UpdatedAt with the quarterly-refresh date", func() {
			t := quota.DefaultConversionTable()
			Expect(t.UpdatedAt).NotTo(BeEmpty(),
				"UpdatedAt MUST be populated — the panel surfaces this as 'rates as of <date>'")
		})

		It("returns a copy — mutating Rates does not affect subsequent calls", func() {
			t1 := quota.DefaultConversionTable()
			t1.Rates["USD"] = 99.0

			t2 := quota.DefaultConversionTable()
			Expect(t2.Rates["USD"]).To(Equal(1.0),
				"DefaultConversionTable MUST return a defensive copy — global mutation would corrupt every caller")
		})
	})

	Describe("ConvertToUSD round-trip per currency", func() {
		t := quota.DefaultConversionTable()

		It("USD passes through unchanged (identity)", func() {
			got, err := t.ConvertToUSD(2500, "USD")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(int64(2500)))
		})

		It("CNY → USD divides by the embedded rate", func() {
			// rate is "1 USD = N CNY"; so 723 fen ÷ 7.23 = 100 cents.
			got, err := t.ConvertToUSD(723, "CNY")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(int64(100)))
		})

		It("EUR → USD divides by the embedded rate", func() {
			// 92 cents ÷ 0.92 = 100 cents.
			got, err := t.ConvertToUSD(92, "EUR")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(int64(100)))
		})

		It("GBP → USD divides by the embedded rate", func() {
			// 79 pence ÷ 0.79 = 100 cents.
			got, err := t.ConvertToUSD(79, "GBP")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(int64(100)))
		})

		It("unknown currency returns an error (no silent zero — Recall failure mode)", func() {
			_, err := t.ConvertToUSD(100, "JPY")
			Expect(err).To(HaveOccurred(),
				"unknown currency MUST error — silent-zero SpentUSD is the Recall failure mode the plan rejects (memory feedback_atomicity_awareness_uneven)")
		})

		It("normalises lowercase currency codes (defensive)", func() {
			got, err := t.ConvertToUSD(2500, "usd")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(int64(2500)))
		})

		It("rounds half-to-nearest", func() {
			// 50 / 0.92 ≈ 54.347 → 54
			got, err := t.ConvertToUSD(50, "EUR")
			Expect(err).NotTo(HaveOccurred())
			Expect(got).To(Equal(int64(54)))
		})
	})

	Describe("LoadCurrencyOverride", func() {
		It("returns DefaultConversionTable for empty path", func() {
			t, err := quota.LoadCurrencyOverride("")
			Expect(err).NotTo(HaveOccurred(),
				"empty path is the 'no override configured' case — quiet success per OD-6")
			Expect(t.Rates).To(HaveKey("USD"))
		})

		It("loads a valid override file", func() {
			tmp := GinkgoT().TempDir()
			path := filepath.Join(tmp, "rates.json")
			Expect(os.WriteFile(path, []byte(`{
				"rates": {"USD": 1.0, "EUR": 0.95, "GBP": 0.81},
				"updated_at": "2026-08-15"
			}`), 0o600)).To(Succeed())

			t, err := quota.LoadCurrencyOverride(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(t.Rates["EUR"]).To(Equal(0.95),
				"override MUST replace embedded rates exactly — operator-supplied numbers win")
			Expect(t.UpdatedAt).To(Equal("2026-08-15"))
		})

		It("rejects override missing USD identity entry", func() {
			tmp := GinkgoT().TempDir()
			path := filepath.Join(tmp, "rates.json")
			Expect(os.WriteFile(path, []byte(`{"rates":{"EUR":0.92}}`), 0o600)).To(Succeed())

			_, err := quota.LoadCurrencyOverride(path)
			Expect(err).To(MatchError(ContainSubstring("missing USD identity rate")),
				"a table without USD=1.0 would silently break SpentUSD — reject at load time")
		})

		It("rejects non-unity USD rate", func() {
			tmp := GinkgoT().TempDir()
			path := filepath.Join(tmp, "rates.json")
			Expect(os.WriteFile(path, []byte(`{"rates":{"USD":0.5,"EUR":0.92}}`), 0o600)).To(Succeed())

			_, err := quota.LoadCurrencyOverride(path)
			Expect(err).To(MatchError(ContainSubstring("non-unity USD rate")))
		})

		It("rejects empty rates map", func() {
			tmp := GinkgoT().TempDir()
			path := filepath.Join(tmp, "rates.json")
			Expect(os.WriteFile(path, []byte(`{"rates":{}}`), 0o600)).To(Succeed())

			_, err := quota.LoadCurrencyOverride(path)
			Expect(err).To(MatchError(ContainSubstring("empty rates map")))
		})

		It("propagates filesystem-read errors", func() {
			_, err := quota.LoadCurrencyOverride("/nonexistent/rates.json")
			Expect(err).To(HaveOccurred())
		})

		It("propagates JSON parse errors", func() {
			tmp := GinkgoT().TempDir()
			path := filepath.Join(tmp, "bad.json")
			Expect(os.WriteFile(path, []byte(`not json`), 0o600)).To(Succeed())

			_, err := quota.LoadCurrencyOverride(path)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("SupportedCurrencies", func() {
		It("returns the sorted list of v1 currencies", func() {
			t := quota.DefaultConversionTable()
			got := t.SupportedCurrencies()
			Expect(got).To(Equal([]string{"CNY", "EUR", "GBP", "USD"}),
				"v1 currencies sorted lexicographically — surfaced in the panel tooltip per OD-6")
		})
	})
})
