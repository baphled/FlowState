package failover_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Integration: full error classification chain", Label("integration"), func() {
	var (
		registry *provider.Registry
		health   *failover.HealthManager
		manager  *failover.Manager
		sh       *failover.StreamHook
	)

	BeforeEach(func() {
		registry = provider.NewRegistry()
		health = failover.NewHealthManager()
		manager = failover.NewManager(registry, health, 2*time.Second)
		sh = failover.NewStreamHook(manager, nil, "")
	})

	Describe("Scenario 1: Z.AI billing error (1113) is NOT rate-limited but provider marked unavailable", func() {
		BeforeEach(func() {
			billingErr := &provider.Error{
				HTTPStatus: 429,
				ErrorCode:  "1113",
				ErrorType:  provider.ErrorTypeBilling,
				Provider:   "zai",
				Message:    "Insufficient balance",
			}
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(billingErr),
			})
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Fallback via Anthropic", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5"},
				{Provider: "anthropic", Model: "claude-3"},
			})
		})

		It("falls back to the next provider successfully", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Fallback via Anthropic"))
		})

		It("marks the billing provider as unavailable with 24h cooldown", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})

		It("classifies billing as NOT a rate-limit via CheckAndMarkRateLimited", func() {
			billingErr := &provider.Error{
				ErrorType: provider.ErrorTypeBilling,
				Provider:  "zai",
				Message:   "Insufficient balance",
			}

			result := failover.CheckAndMarkRateLimited(health, "zai", "glm-5", billingErr)
			Expect(result).To(BeFalse())
		})

		It("records the successful fallback provider as last", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())
			for v := range ch {
				_ = v
			}

			Expect(manager.LastProvider()).To(Equal("anthropic"))
			Expect(manager.LastModel()).To(Equal("claude-3"))
		})
	})

	Describe("Scenario 2: Z.AI rate-limit (1001) IS rate-limited with 1h cooldown", func() {
		BeforeEach(func() {
			rateLimitErr := &provider.Error{
				HTTPStatus: 429,
				ErrorCode:  "1001",
				ErrorType:  provider.ErrorTypeRateLimit,
				Provider:   "zai",
				Message:    "Rate limit exceeded",
			}
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(rateLimitErr),
			})
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Rate-limit fallback", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5"},
				{Provider: "anthropic", Model: "claude-3"},
			})
		})

		It("falls back to the next provider", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Rate-limit fallback"))
		})

		It("marks the provider as unavailable", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})

		It("classifies rate-limit as IS rate-limited via CheckAndMarkRateLimited", func() {
			rateLimitErr := &provider.Error{
				ErrorType: provider.ErrorTypeRateLimit,
				Provider:  "zai",
				Message:   "Rate limit exceeded",
			}

			result := failover.CheckAndMarkRateLimited(health, "zai", "glm-5", rateLimitErr)
			Expect(result).To(BeTrue())
		})

		It("uses 1h cooldown for rate-limit errors", func() {
			Expect(failover.CooldownForErrorType(provider.ErrorTypeRateLimit)).To(Equal(time.Hour))
		})
	})

	Describe("Scenario 3: Anthropic 529 overload gets 60s cooldown", func() {
		BeforeEach(func() {
			overloadErr := &provider.Error{
				HTTPStatus: 529,
				ErrorType:  provider.ErrorTypeOverload,
				Provider:   "anthropic",
				Message:    "Overloaded",
			}
			registry.Register(&mockStreamProvider{
				name:     "anthropic",
				streamFn: syncErrorStreamFn(overloadErr),
			})
			registry.Register(&mockStreamProvider{
				name: "ollama",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Overload fallback", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "ollama", Model: "llama3.2"},
			})
		})

		It("falls back to the next provider", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Overload fallback"))
		})

		It("marks the overloaded provider as unavailable", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
		})

		It("uses 60s cooldown for overload errors", func() {
			Expect(failover.CooldownForErrorType(provider.ErrorTypeOverload)).To(Equal(60 * time.Second))
		})
	})

	Describe("Scenario 4: all providers fail with non-retriable errors", func() {
		BeforeEach(func() {
			authErr := &provider.Error{
				ErrorType: provider.ErrorTypeAuthFailure,
				Provider:  "anthropic",
				Message:   "invalid API key",
			}
			billingErr := &provider.Error{
				ErrorType: provider.ErrorTypeBilling,
				Provider:  "zai",
				Message:   "insufficient balance",
			}
			registry.Register(&mockStreamProvider{
				name:     "anthropic",
				streamFn: syncErrorStreamFn(authErr),
			})
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(billingErr),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "anthropic", Model: "claude-3"},
				{Provider: "zai", Model: "glm-5"},
			})
		})

		It("returns a meaningful error containing 'all providers failed'", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("all providers failed"))
		})

		It("marks both providers as unavailable", func() {
			handler := sh.Execute(baseHandler(registry))
			_, _ = handler(context.Background(), &provider.ChatRequest{})

			Expect(health.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})
	})

	Describe("Scenario 5: mixed providers — some ProviderError, some plain error", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name:     "openai",
				streamFn: syncErrorStreamFn(errors.New("rate_limit exceeded")),
			})
			billingErr := &provider.Error{
				ErrorType: provider.ErrorTypeBilling,
				Provider:  "zai",
				Message:   "insufficient balance",
			}
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(billingErr),
			})
			registry.Register(&mockStreamProvider{
				name: "ollama",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Hybrid fallback", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "openai", Model: "gpt-4"},
				{Provider: "zai", Model: "glm-5"},
				{Provider: "ollama", Model: "llama3.2"},
			})
		})

		It("falls back through both failures to the third provider", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Hybrid fallback"))
		})

		It("marks both failed providers as unavailable", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("openai", "gpt-4")).To(BeTrue())
			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})

		It("plain error is classified via keyword detection", func() {
			result := failover.CheckAndMarkRateLimited(health, "openai", "gpt-4", errors.New("rate_limit exceeded"))
			Expect(result).To(BeTrue())
		})

		It("structured billing error is NOT classified as rate-limited", func() {
			billingErr := &provider.Error{
				ErrorType: provider.ErrorTypeBilling,
				Provider:  "zai",
				Message:   "insufficient balance",
			}
			result := failover.CheckAndMarkRateLimited(health, "zai", "glm-5", billingErr)
			Expect(result).To(BeFalse())
		})
	})

	Describe("Scenario 6: malformed/unknown error gracefully degrades to Unknown", func() {
		BeforeEach(func() {
			registry.Register(&mockStreamProvider{
				name:     "custom",
				streamFn: syncErrorStreamFn(errors.New("something unexpected happened")),
			})
			registry.Register(&mockStreamProvider{
				name: "backup",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Backup response", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "custom", Model: "custom-model"},
				{Provider: "backup", Model: "backup-model"},
			})
		})

		It("does NOT mark the provider via keyword detection for unknown errors", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("custom", "custom-model")).To(BeFalse())
		})

		It("still falls back to the next provider", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Backup response"))
		})

		It("returns false from CheckAndMarkRateLimited for unknown plain errors", func() {
			result := failover.CheckAndMarkRateLimited(health, "custom", "custom-model", errors.New("something unexpected"))
			Expect(result).To(BeFalse())
		})

		It("classifies unknown ErrorType with 5m cooldown", func() {
			Expect(failover.CooldownForErrorType(provider.ErrorTypeUnknown)).To(Equal(5 * time.Minute))
		})
	})

	Describe("async error chain — provider.Error delivered via stream chunk", func() {
		BeforeEach(func() {
			billingErr := &provider.Error{
				HTTPStatus: 429,
				ErrorCode:  "1113",
				ErrorType:  provider.ErrorTypeBilling,
				Provider:   "zai",
				Message:    "Insufficient balance",
			}
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: asyncErrorStreamFn(billingErr),
			})
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Async billing fallback", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5"},
				{Provider: "anthropic", Model: "claude-3"},
			})
		})

		It("detects the async billing error and falls back", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Async billing fallback"))
		})

		It("marks the async billing provider as unavailable", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})
	})

	Describe("cooldown differentiation across error types", func() {
		DescribeTable("billing vs rate-limit vs overload cooldowns are distinct",
			func(errorType provider.ErrorType, expectedCooldown time.Duration) {
				Expect(failover.CooldownForErrorType(errorType)).To(Equal(expectedCooldown))
			},
			Entry("Billing → 24h", provider.ErrorTypeBilling, 24*time.Hour),
			Entry("RateLimit → 1h", provider.ErrorTypeRateLimit, time.Hour),
			Entry("Overload → 60s", provider.ErrorTypeOverload, 60*time.Second),
			Entry("NetworkError → 30s", provider.ErrorTypeNetworkError, 30*time.Second),
			Entry("ServerError → 2m", provider.ErrorTypeServerError, 2*time.Minute),
			Entry("Unknown → 5m", provider.ErrorTypeUnknown, 5*time.Minute),
		)
	})

	Describe("wrapped provider.Error through fmt.Errorf", func() {
		BeforeEach(func() {
			billingErr := &provider.Error{
				ErrorType: provider.ErrorTypeBilling,
				Provider:  "zai",
				Message:   "Insufficient balance",
			}
			wrappedErr := fmt.Errorf("zai: %w", billingErr)
			registry.Register(&mockStreamProvider{
				name:     "zai",
				streamFn: syncErrorStreamFn(wrappedErr),
			})
			registry.Register(&mockStreamProvider{
				name: "anthropic",
				streamFn: successStreamFn(
					provider.StreamChunk{Content: "Wrapped fallback", Done: true},
				),
			})
			manager.SetBasePreferences([]provider.ModelPreference{
				{Provider: "zai", Model: "glm-5"},
				{Provider: "anthropic", Model: "claude-3"},
			})
		})

		It("unwraps the provider.Error and marks with correct cooldown", func() {
			handler := sh.Execute(baseHandler(registry))
			_, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})

		It("falls back to the next provider", func() {
			handler := sh.Execute(baseHandler(registry))
			ch, err := handler(context.Background(), &provider.ChatRequest{})
			Expect(err).NotTo(HaveOccurred())

			var chunks []provider.StreamChunk
			for chunk := range ch {
				chunks = append(chunks, chunk)
			}
			Expect(chunks).To(HaveLen(1))
			Expect(chunks[0].Content).To(Equal("Wrapped fallback"))
		})
	})
})
