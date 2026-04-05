package failover_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

func TestFailover(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Failover Suite")
}

var _ = Describe("RateLimitDetector", func() {
	var (
		bus      *eventbus.EventBus
		health   *failover.HealthManager
		detector *failover.RateLimitDetector
	)

	BeforeEach(func() {
		var err error
		dir, err := os.MkdirTemp("", "failover-detector-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		bus = eventbus.NewEventBus()
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
		detector = failover.NewRateLimitDetector(bus, health)
	})

	Describe("detects rate-limit errors and marks rate-limited", func() {
		It("detects rate_limit error message and marks provider as rate-limited", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue())
		})

		It("detects too many requests error and marks provider as rate-limited", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "openai",
				Error:        errors.New("too many requests, please retry"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("openai", "")).To(BeTrue())
		})

		It("uses ModelName field and marks rate-limited with model", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				ModelName:    "claude-3-5-sonnet-20241022",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "claude-3-5-sonnet-20241022")).To(BeTrue())
		})

		It("detects quota exceeded error and marks provider as rate-limited", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("quota exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue())
			Expect(health.GetHealthyAlternatives("anthropic", "")).To(BeEmpty())
		})
	})

	Describe("detects rate-limit keywords in error messages", func() {
		It("detects 'rate_limit' in error message", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue())
		})

		It("detects 'rate limit' (with space) in error message", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate limit exceeded for this model"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue())
		})

		It("detects 'quota exceeded' in error message", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "openai",
				Error:        errors.New("quota exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("openai", "")).To(BeTrue())
		})

		It("detects 'too many requests' in error message", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "github-copilot",
				Error:        errors.New("too many requests, please try again later"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("github-copilot", "")).To(BeTrue())
		})

		It("detects 'free usage exceeded' in error message", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("free usage exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeTrue())
		})

		It("does not mark healthy for non-rate-limit errors", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("invalid request: missing parameter"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeFalse())
		})
	})

	Describe("publishes rate-limited event", func() {
		It("publishes provider.rate_limited event when rate-limit detected", func() {
			var publishedEvent *events.ProviderEvent

			bus.Subscribe("provider.rate_limited", func(event any) {
				publishedEvent = event.(*events.ProviderEvent)
			})

			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("rate_limit exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(publishedEvent).NotTo(BeNil())
			Expect(publishedEvent.Data.ProviderName).To(Equal("anthropic"))
		})
	})
})

var _ = Describe("CheckAndMarkRateLimited", func() {
	var (
		health *failover.HealthManager
	)

	BeforeEach(func() {
		var err error
		dir, err := os.MkdirTemp("", "failover-check-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
	})

	It("returns false and does not mark when error is nil", func() {
		result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", nil)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse())
	})

	It("returns true and marks provider when error contains rate_limit", func() {
		err := errors.New("rate_limit exceeded")

		result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
	})

	It("returns true and marks provider when error contains quota exceeded", func() {
		err := errors.New("quota exceeded for this month")

		result := failover.CheckAndMarkRateLimited(health, "openai", "gpt-4", err)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("openai", "gpt-4")).To(BeTrue())
	})

	It("returns true and marks provider when error contains too many requests", func() {
		err := errors.New("too many requests, please try again later")

		result := failover.CheckAndMarkRateLimited(health, "github-copilot", "claude-3", err)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("github-copilot", "claude-3")).To(BeTrue())
	})

	It("returns false and does not mark for generic non-rate-limit error", func() {
		err := errors.New("invalid request: missing required field")

		result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse())
	})

	It("returns false and does not mark for auth error", func() {
		err := errors.New("authentication failed: invalid API key")

		result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse())
	})
})

var _ = Describe("Hook", func() {
	var (
		chain     *failover.FallbackChain
		health    *failover.HealthManager
		hook      *failover.Hook
		providers []failover.ProviderModel
	)

	BeforeEach(func() {
		var err error
		dir, err := os.MkdirTemp("", "failover-hook-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		providers = []failover.ProviderModel{
			{Provider: "anthropic", Model: "claude-3-5-sonnet-20241022"},
			{Provider: "github-copilot", Model: "claude-3-5-sonnet-20241022"},
			{Provider: "openai", Model: "gpt-4o"},
			{Provider: "ollama", Model: "llama3.2"},
		}
		chain = failover.NewFallbackChain(providers, nil)
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
		hook = failover.NewHook(chain, health)
	})

	Describe("updates ChatRequest on rate-limit", func() {
		It("switches to next healthy provider when current is rate-limited", func() {
			health.MarkRateLimited("anthropic", "claude-3-5-sonnet-20241022", time.Now().Add(1*time.Hour))

			req := &provider.ChatRequest{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-20241022",
				Messages: []provider.Message{{Role: "user", Content: "Hello"}},
			}

			err := hook.Apply(context.Background(), req)

			Expect(err).NotTo(HaveOccurred())
			Expect(req.Provider).To(Equal("github-copilot"))
			Expect(req.Model).To(Equal("claude-3-5-sonnet-20241022"))
		})

		It("skips multiple rate-limited providers", func() {
			health.MarkRateLimited("anthropic", "claude-3-5-sonnet-20241022", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("github-copilot", "claude-3-5-sonnet-20241022", time.Now().Add(1*time.Hour))

			req := &provider.ChatRequest{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-20241022",
			}

			err := hook.Apply(context.Background(), req)

			Expect(err).NotTo(HaveOccurred())
			Expect(req.Provider).To(Equal("openai"))
			Expect(req.Model).To(Equal("gpt-4o"))
		})

		It("returns error when no healthy provider available", func() {
			health.MarkRateLimited("anthropic", "claude-3-5-sonnet-20241022", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("github-copilot", "claude-3-5-sonnet-20241022", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("openai", "gpt-4o", time.Now().Add(1*time.Hour))
			health.MarkRateLimited("ollama", "llama3.2", time.Now().Add(1*time.Hour))

			req := &provider.ChatRequest{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-20241022",
			}

			err := hook.Apply(context.Background(), req)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("no healthy provider available"))
		})
	})

	Describe("does not modify when provider is healthy", func() {
		It("returns nil when provider is not rate-limited", func() {
			req := &provider.ChatRequest{
				Provider: "anthropic",
				Model:    "claude-3-5-sonnet-20241022",
				Messages: []provider.Message{{Role: "user", Content: "Hello"}},
			}

			err := hook.Apply(context.Background(), req)

			Expect(err).NotTo(HaveOccurred())
			Expect(req.Provider).To(Equal("anthropic"))
			Expect(req.Model).To(Equal("claude-3-5-sonnet-20241022"))
		})

		It("uses default provider when Provider field is empty", func() {
			req := &provider.ChatRequest{
				Provider: "",
				Model:    "",
				Messages: []provider.Message{{Role: "user", Content: "Hello"}},
			}

			err := hook.Apply(context.Background(), req)

			Expect(err).NotTo(HaveOccurred())
			Expect(req.Provider).To(Equal("anthropic"))
		})

		It("preserves existing model when Provider field is empty", func() {
			req := &provider.ChatRequest{
				Provider: "",
				Model:    "claude-sonnet-4-6",
				Messages: []provider.Message{{Role: "user", Content: "Hello"}},
			}

			err := hook.Apply(context.Background(), req)

			Expect(err).NotTo(HaveOccurred())
			Expect(req.Provider).To(Equal("anthropic"))
			Expect(req.Model).To(Equal("claude-sonnet-4-6"))
		})
	})

	Describe("handles nil request", func() {
		It("returns error when request is nil", func() {
			err := hook.Apply(context.Background(), nil)

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("request is nil"))
		})
	})
})

var _ = Describe("HealthManager", func() {
	var (
		dir  string
		path string
		hm   *failover.HealthManager
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		path = filepath.Join(dir, "provider-health.json")
		hm = failover.NewHealthManager()
	})

	It("marks provider+model as rate-limited", func() {
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		Expect(hm.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
	})

	It("returns true for rate-limited provider", func() {
		hm.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))
		Expect(hm.IsRateLimited("openai", "gpt-4")).To(BeTrue())
	})

	It("returns false after expiry time passes", func() {
		hm.MarkRateLimited("openai", "gpt-4", time.Now().Add(-1*time.Minute))
		Expect(hm.IsRateLimited("openai", "gpt-4")).To(BeFalse())
	})

	It("filters out rate-limited providers in GetHealthyAlternatives", func() {
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		alts := hm.GetHealthyAlternatives("anthropic", "claude-3")
		for _, alt := range alts {
			Expect(alt.Provider).NotTo(Equal("anthropic"))
		}
	})

	It("persists state to disk", func() {
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		err := hm.PersistStateInternal(path)
		Expect(err).NotTo(HaveOccurred())
		_, err = os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	})

	It("loads state from disk (round-trip)", func() {
		now := time.Now().Add(1 * time.Hour)
		hm.MarkRateLimited("anthropic", "claude-3", now)
		err := hm.PersistStateInternal(path)
		Expect(err).NotTo(HaveOccurred())
		newHM := failover.NewHealthManager()
		err = newHM.LoadState(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(newHM.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
	})

	It("does not race on concurrent reads (RLock)", func() {
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		ch := make(chan bool, 10)
		for range 10 {
			go func() {
				ch <- hm.IsRateLimited("anthropic", "claude-3")
			}()
		}
		for range 10 {
			<-ch
		}
	})

	It("does not race on concurrent write + reads (mutex discipline)", func() {
		ch := make(chan bool, 10)
		for i := range 10 {
			go func(idx int) {
				if idx%2 == 0 {
					hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
				} else {
					ch <- hm.IsRateLimited("anthropic", "claude-3")
				}
			}(i)
		}
		for range 5 {
			<-ch
		}
	})
})
