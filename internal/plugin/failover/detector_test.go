package failover_test

import (
	"context"
	"errors"
	"fmt"
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

		It("does not classify quota exceeded as rate-limited after keyword removal", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "anthropic",
				Error:        errors.New("quota exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("anthropic", "")).To(BeFalse())
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

		It("does not detect 'quota exceeded' after keyword removal", func() {
			providerEvent := events.NewProviderErrorEvent(events.ProviderErrorEventData{
				ProviderName: "openai",
				Error:        errors.New("quota exceeded"),
			})

			detector.HandleError(providerEvent)

			Expect(health.IsRateLimited("openai", "")).To(BeFalse())
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

	It("does not mark provider for quota exceeded after keyword removal", func() {
		err := errors.New("quota exceeded for this month")

		result := failover.CheckAndMarkRateLimited(health, "openai", "gpt-4", err)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("openai", "gpt-4")).To(BeFalse())
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

var _ = Describe("CooldownForErrorType", func() {
	DescribeTable("maps error type to duration",
		func(errorType provider.ErrorType, expected time.Duration) {
			Expect(failover.CooldownForErrorType(errorType)).To(Equal(expected))
		},
		Entry("RateLimit → 1 hour", provider.ErrorTypeRateLimit, time.Hour),
		Entry("Billing → 24 hours", provider.ErrorTypeBilling, 24*time.Hour),
		Entry("Quota → 24 hours", provider.ErrorTypeQuota, 24*time.Hour),
		Entry("AuthFailure → 24 hours", provider.ErrorTypeAuthFailure, 24*time.Hour),
		Entry("ModelNotFound → 24 hours", provider.ErrorTypeModelNotFound, 24*time.Hour),
		Entry("Overload → 60 seconds", provider.ErrorTypeOverload, 60*time.Second),
		Entry("NetworkError → 30 seconds", provider.ErrorTypeNetworkError, 30*time.Second),
		Entry("ServerError → 2 minutes", provider.ErrorTypeServerError, 2*time.Minute),
		Entry("Unknown → 5 minutes", provider.ErrorTypeUnknown, 5*time.Minute),
	)

	It("preserves existing rate-limit cooldown of 1 hour", func() {
		Expect(failover.CooldownForErrorType(provider.ErrorTypeRateLimit)).To(Equal(time.Hour))
	})
})

var _ = Describe("Z.AI error code classification", func() {
	var health *failover.HealthManager

	BeforeEach(func() {
		var err error
		dir, err := os.MkdirTemp("", "failover-zai-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
	})

	It("returns true for Z.AI rate-limit code 1001", func() {
		err := errors.New("provider error: 429 {\"code\":\"1001\",\"message\":\"Rate limit exceeded\"}")

		result := failover.CheckAndMarkRateLimited(health, "zai", "glm-4.6", err)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeTrue())
	})

	It("returns false for Z.AI overload code 1002", func() {
		err := errors.New("provider error: 429 {\"code\":\"1002\",\"message\":\"Server overloaded\"}")

		result := failover.CheckAndMarkRateLimited(health, "zai", "glm-4.6", err)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeFalse())
	})

	It("returns false for Z.AI quota code 1112", func() {
		err := errors.New("provider error: 429 {\"code\":\"1112\",\"message\":\"Quota exhausted\"}")

		result := failover.CheckAndMarkRateLimited(health, "zai", "glm-4.6", err)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeFalse())
	})

	It("returns false for Z.AI billing code 1113", func() {
		err := errors.New("provider error: 429 {\"code\":\"1113\",\"message\":\"Insufficient balance\"}")

		result := failover.CheckAndMarkRateLimited(health, "zai", "glm-4.6", err)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeFalse())
	})

	It("still returns true for rate_limit_exceeded regression guard", func() {
		err := errors.New("rate_limit_exceeded")

		result := failover.CheckAndMarkRateLimited(health, "zai", "glm-4.6", err)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeTrue())
	})
})

var _ = Describe("keyword cleanup regression guards", func() {
	var health *failover.HealthManager

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
	})

	Context("removed keywords must not match", func() {
		It("does not classify plain '429' text as rate-limited", func() {
			err := errors.New("provider error: 429 service temporarily unavailable")

			result := failover.CheckAndMarkRateLimited(health, "zai", "glm-4.6", err)

			Expect(result).To(BeFalse())
			Expect(health.IsRateLimited("zai", "glm-4.6")).To(BeFalse())
		})

		It("does not classify '503 Service Unavailable' as rate-limited", func() {
			err := errors.New("503 Service Unavailable")

			result := failover.CheckAndMarkRateLimited(health, "ollama", "llama3.2", err)

			Expect(result).To(BeFalse())
			Expect(health.IsRateLimited("ollama", "llama3.2")).To(BeFalse())
		})

		It("does not classify 'quota exceeded' as rate-limited", func() {
			err := errors.New("quota exceeded for this billing period")

			result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

			Expect(result).To(BeFalse())
			Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeFalse())
		})
	})

	Context("preserved keywords still match", func() {
		It("still classifies 'rate_limit' as rate-limited", func() {
			err := errors.New("rate_limit exceeded")

			result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

			Expect(result).To(BeTrue())
			Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
		})

		It("still classifies 'free usage exceeded' as rate-limited", func() {
			err := errors.New("free usage exceeded for this model")

			result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

			Expect(result).To(BeTrue())
			Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
		})

		It("still classifies 'rate limit' (with space) as rate-limited", func() {
			err := errors.New("rate limit exceeded for this model")

			result := failover.CheckAndMarkRateLimited(health, "anthropic", "claude-3", err)

			Expect(result).To(BeTrue())
			Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
		})

		It("still classifies 'too many requests' as rate-limited", func() {
			err := errors.New("too many requests, try again later")

			result := failover.CheckAndMarkRateLimited(health, "openai", "gpt-4", err)

			Expect(result).To(BeTrue())
			Expect(health.IsRateLimited("openai", "gpt-4")).To(BeTrue())
		})
	})
})

var _ = Describe("hybrid error classification", func() {
	var health *failover.HealthManager

	BeforeEach(func() {
		dir := GinkgoT().TempDir()
		health = failover.NewHealthManager()
		health.SetPersistPath(filepath.Join(dir, "provider-health.json"))
	})

	It("detects *provider.Error with ErrorTypeRateLimit as rate-limited", func() {
		provErr := &provider.Error{
			ErrorType: provider.ErrorTypeRateLimit,
			Provider:  "test-provider",
			Message:   "request throttled",
		}

		result := failover.CheckAndMarkRateLimited(health, "test-provider", "model-1", provErr)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("test-provider", "model-1")).To(BeTrue())
	})

	It("does not classify *provider.Error with ErrorTypeBilling as rate-limited", func() {
		provErr := &provider.Error{
			ErrorType: provider.ErrorTypeBilling,
			Provider:  "test-provider",
			Message:   "rate limit on billing endpoint",
		}

		result := failover.CheckAndMarkRateLimited(health, "test-provider", "model-1", provErr)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("test-provider", "model-1")).To(BeFalse())
	})

	It("does not classify *provider.Error with ErrorTypeOverload as rate-limited", func() {
		provErr := &provider.Error{
			ErrorType: provider.ErrorTypeOverload,
			Provider:  "test-provider",
			Message:   "too many requests causing overload",
		}

		result := failover.CheckAndMarkRateLimited(health, "test-provider", "model-1", provErr)

		Expect(result).To(BeFalse())
		Expect(health.IsRateLimited("test-provider", "model-1")).To(BeFalse())
	})

	It("unwraps wrapped *provider.Error via errors.As", func() {
		provErr := &provider.Error{
			ErrorType: provider.ErrorTypeRateLimit,
			Provider:  "test-provider",
			Message:   "throttled",
		}
		wrapped := fmt.Errorf("provider call failed: %w", provErr)

		result := failover.CheckAndMarkRateLimited(health, "test-provider", "model-1", wrapped)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("test-provider", "model-1")).To(BeTrue())
	})

	It("falls back to keyword matching for plain string errors", func() {
		err := errors.New("rate_limit exceeded")

		result := failover.CheckAndMarkRateLimited(health, "test-provider", "model-1", err)

		Expect(result).To(BeTrue())
		Expect(health.IsRateLimited("test-provider", "model-1")).To(BeTrue())
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
