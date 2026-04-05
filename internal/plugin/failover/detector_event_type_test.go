package failover_test

import (
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
)

var _ = Describe("DetectorEventType", func() {
	var (
		bus      *eventbus.EventBus
		health   *failover.HealthManager
		detector *failover.RateLimitDetector
	)

	BeforeEach(func() {
		dir, err := os.MkdirTemp("", "detector-event-type-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		bus = eventbus.NewEventBus()
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
		detector = failover.NewRateLimitDetector(bus, health)
	})

	Describe("characterisation: ProviderEvent is not ProviderErrorEvent", func() {
		It("cannot be type-asserted as *ProviderErrorEvent — they are distinct types", func() {
			genericEvent := events.NewProviderEvent(events.ProviderEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			var asErrorEvent any = genericEvent
			_, ok := asErrorEvent.(*events.ProviderErrorEvent)

			Expect(ok).To(BeFalse(),
				"*ProviderEvent must NOT satisfy *ProviderErrorEvent type assertion")
		})

		It("silently drops *ProviderErrorEvent — the type the engine actually publishes — leaving health unchanged", func() {
			errorEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(errorEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeFalse(),
				"current buggy handler must drop *ProviderErrorEvent because it asserts *ProviderEvent instead")
		})
	})

	Describe("verification: ProviderErrorEvent is handled correctly after fix", func() {
		PIt("marks provider as rate-limited when a *ProviderErrorEvent with rate-limit error is published", func() {
			errorEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(errorEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue(),
				"handler must process *ProviderErrorEvent and mark the provider rate-limited")
		})

		PIt("marks provider+model as rate-limited using ModelName from ProviderErrorEventData", func() {
			errorEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				ModelName:    "claude-sonnet-4-6",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(errorEvent)

			Expect(health.IsRateLimited("anthropic", "claude-sonnet-4-6")).To(BeTrue(),
				"handler must use ModelName field from ProviderErrorEventData for rate-limit key")
		})

		PIt("does not mark rate-limited when error does not contain rate-limit keywords", func() {
			errorEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("invalid request: missing parameter"),
			})

			detector.HandleError(errorEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeFalse(),
				"handler must not mark rate-limited for non-rate-limit errors")
		})

		PIt("publishes provider.rate_limited event when rate-limit detected via ProviderErrorEvent", func() {
			var published *events.ProviderEvent
			bus.Subscribe("provider.rate_limited", func(event any) {
				published = event.(*events.ProviderEvent)
			})

			errorEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(errorEvent)

			Expect(published).NotTo(BeNil(),
				"handler must re-publish to provider.rate_limited when rate-limit is detected")
			Expect(published.Data.ProviderName).To(Equal("anthropic"))
		})
	})
})
