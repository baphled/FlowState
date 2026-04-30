package app_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("SwarmAgentRegistryAdapter", func() {
	Context("when the wrapped registry holds a manifest with aliases", func() {
		var adapter interface {
			Get(id string) (any, bool)
		}

		BeforeEach(func() {
			registry := agent.NewRegistry()
			registry.Register(&agent.Manifest{
				ID:      "lead-coordinator",
				Name:    "Lead Coordinator",
				Aliases: []string{"lead", "coordinator"},
			})
			adapter = app.NewSwarmAgentRegistryAdapterForTest(registry)
		})

		It("resolves the exact ID", func() {
			_, ok := adapter.Get("lead-coordinator")
			Expect(ok).To(BeTrue())
		})

		It("resolves a declared alias", func() {
			_, ok := adapter.Get("coordinator")
			Expect(ok).To(BeTrue())
		})

		It("resolves every declared alias", func() {
			_, ok := adapter.Get("lead")
			Expect(ok).To(BeTrue())
		})

		It("rejects unknown identifiers", func() {
			_, ok := adapter.Get("not-an-agent")
			Expect(ok).To(BeFalse())
		})
	})

	Context("when the wrapped registry is nil", func() {
		It("returns false for any identifier", func() {
			adapter := app.NewSwarmAgentRegistryAdapterForTest(nil)
			_, ok := adapter.Get("anything")
			Expect(ok).To(BeFalse())
		})
	})
})
