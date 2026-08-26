package providers

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider/zai"
)

const codingPlanHostURL = "https://api.z.ai/api/coding/paas/v4"

// zaiConfigWithPlanAndHost returns an AppConfig pre-populated with the
// supplied Z.AI plan/host pair so the zaiPlanFromConfig specs stay terse.
func zaiConfigWithPlanAndHost(plan, host string) *config.AppConfig {
	cfg := &config.AppConfig{}
	cfg.Providers.ZAI.Plan = plan
	cfg.Providers.ZAI.Host = host
	return cfg
}

var _ = Describe("zaiPlanFromConfig", func() {
	Context("when Plan is explicitly 'coding'", func() {
		It("returns the coding tag", func() {
			cfg := zaiConfigWithPlanAndHost("coding", "")
			Expect(zaiPlanFromConfig(cfg)).To(Equal(zai.PlanCoding))
		})

		It("normalises mixed-case and whitespace", func() {
			cfg := zaiConfigWithPlanAndHost("  Coding  ", "")
			Expect(zaiPlanFromConfig(cfg)).To(Equal(zai.PlanCoding))
		})
	})

	Context("when Plan is empty and Host equals the coding URL", func() {
		It("infers coding from the legacy host encoding", func() {
			cfg := zaiConfigWithPlanAndHost("", codingPlanHostURL)
			Expect(zaiPlanFromConfig(cfg)).To(Equal(zai.PlanCoding))
		})
	})

	Context("when both Plan and Host are empty", func() {
		It("returns the empty string (general)", func() {
			cfg := zaiConfigWithPlanAndHost("", "")
			Expect(zaiPlanFromConfig(cfg)).To(BeEmpty())
		})
	})

	Context("when Plan is 'general' and Host points at the coding URL", func() {
		It("honours the explicit Plan field over the legacy host", func() {
			cfg := zaiConfigWithPlanAndHost("general", codingPlanHostURL)
			Expect(zaiPlanFromConfig(cfg)).To(BeEmpty())
		})
	})
})
