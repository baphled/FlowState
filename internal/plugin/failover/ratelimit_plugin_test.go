package failover_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
)

var _ = Describe("RateLimitPlugin", func() {
	var (
		dir      string
		bus      *eventbus.EventBus
		health   *failover.HealthManager
		registry *plugin.Registry
		plug     plugin.Plugin
	)

	BeforeEach(func() {
		plugin.ResetBuiltins()
		failover.RegisterBuiltins()

		var err error
		dir, err = os.MkdirTemp("", "rate-limit-plugin-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})

		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
		bus = eventbus.NewEventBus()
		registry = plugin.NewRegistry()

		err = plugin.LoadBuiltins(plugin.Deps{
			Registry:      registry,
			EventBus:      bus,
			HealthManager: health,
		})
		Expect(err).NotTo(HaveOccurred())

		var ok bool
		plug, ok = registry.Get("rate-limit-detector")
		Expect(ok).To(BeTrue())
	})

	It("registers the rate-limit-detector builtin", func() {
		Expect(plug.Name()).To(Equal("rate-limit-detector"))
		Expect(plug.Version()).To(Equal("1.0.0"))
	})

	It("implements BusStarter", func() {
		_, ok := plug.(plugin.BusStarter)
		Expect(ok).To(BeTrue())
	})

	Context("after starting the plugin", func() {
		It("marks a provider as rate-limited after a 429 provider.error event", func() {
			starter, ok := plug.(plugin.BusStarter)
			Expect(ok).To(BeTrue())

			Expect(starter.Start(bus)).To(Succeed())

			bus.Publish("provider.error", events.NewProviderEvent(events.ProviderEventData{
				ProviderName: "anthropic",
				Response: map[string]any{
					"status_code": 429,
				},
			}))

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue())
		})
	})
})
