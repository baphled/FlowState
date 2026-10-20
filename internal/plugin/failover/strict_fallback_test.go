package failover_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Manager fail-closed stance", func() {
	var (
		registry *provider.Registry
		health   *failover.HealthManager
		mgr      *failover.Manager
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		mgr = failover.NewManager(registry, health, 5*time.Minute)
	})

	rejectAll := func(_, _ string) bool { return false }

	It("returns no candidates when fail-closed and the filter rejects everything", func() {
		mgr.SetCapabilityFilter(rejectAll)
		mgr.SetFailClosed(true)
		mgr.SetBasePreferences([]provider.ModelPreference{
			{Provider: "ollama", Model: "llama3.2"},
		})

		Expect(mgr.Candidates()).To(BeEmpty())
	})

	It("falls back to the unfiltered base when fail-closed is disarmed", func() {
		mgr.SetCapabilityFilter(rejectAll)
		mgr.SetFailClosed(false)
		base := []provider.ModelPreference{
			{Provider: "ollama", Model: "llama3.2"},
		}
		mgr.SetBasePreferences(base)

		Expect(mgr.Candidates()).To(Equal(base))
	})

	It("keeps filtering (not the empty-guard) when armed and some candidates pass", func() {
		mgr.SetFailClosed(true)
		mgr.SetCapabilityFilter(func(p, m string) bool {
			return p == "zai"
		})
		mgr.SetBasePreferences([]provider.ModelPreference{
			{Provider: "zai", Model: "glm-4.6"},
			{Provider: "ollama", Model: "llama3.2"},
		})

		Expect(mgr.Candidates()).To(Equal([]provider.ModelPreference{
			{Provider: "zai", Model: "glm-4.6"},
		}))
	})

	It("can be disarmed after being armed on a shared manager", func() {
		mgr.SetCapabilityFilter(rejectAll)
		mgr.SetFailClosed(true)
		mgr.SetBasePreferences([]provider.ModelPreference{
			{Provider: "ollama", Model: "llama3.2"},
		})
		Expect(mgr.Candidates()).To(BeEmpty())

		mgr.SetFailClosed(false)
		Expect(mgr.Candidates()).To(HaveLen(1))
	})
})

var _ = Describe("StrictChainWithHealthyFallback local exclusion", func() {
	var health *failover.HealthManager

	BeforeEach(func() {
		health = failover.NewHealthManager()
	})

	strict := []provider.ModelPreference{{Provider: "anthropic", Model: "claude-sonnet-4"}}

	It("skips a healthy local provider in the fallback tail", func() {
		health.MarkHardDown("anthropic", "claude-sonnet-4", "test")

		got := failover.StrictChainWithHealthyFallback(strict, []provider.ModelPreference{
			{Provider: "ollama", Model: "llama3.2"},
		}, health)

		Expect(got).To(Equal(strict))
	})

	It("keeps a healthy remote provider in the fallback tail", func() {
		health.MarkHardDown("anthropic", "claude-sonnet-4", "test")

		got := failover.StrictChainWithHealthyFallback(strict, []provider.ModelPreference{
			{Provider: "zai", Model: "glm-4.6"},
			{Provider: "ollama", Model: "llama3.2"},
		}, health)

		Expect(got).To(Equal([]provider.ModelPreference{
			{Provider: "anthropic", Model: "claude-sonnet-4"},
			{Provider: "zai", Model: "glm-4.6"},
		}))
	})

	It("fails closed to the bare strict chain when only locals remain healthy", func() {
		health.MarkHardDown("anthropic", "claude-sonnet-4", "test")

		got := failover.StrictChainWithHealthyFallback(strict, []provider.ModelPreference{
			{Provider: "ollama", Model: "llama3.2"},
			{Provider: "ollama", Model: "qwen3:8b"},
		}, health)

		Expect(got).To(Equal(strict))
		Expect(got).To(HaveLen(1))
	})

	It("leaves the chain untouched when the strict head is healthy", func() {
		got := failover.StrictChainWithHealthyFallback(strict, []provider.ModelPreference{
			{Provider: "ollama", Model: "llama3.2"},
		}, health)

		Expect(got).To(Equal(strict))
	})
})
