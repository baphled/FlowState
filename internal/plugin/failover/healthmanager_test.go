package failover

import (
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/provider"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("HealthManager", func() {
	var (
		dir  string
		path string
		hm   *HealthManager
	)

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "healthmanager-test-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		path = filepath.Join(dir, "provider-health.json")
		hm = NewHealthManager()
		hm.SetPersistPath(path)
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
		retryAfter := time.Now().Add(1 * time.Hour)
		failureAt := time.Now()
		hm.MarkRateLimited("anthropic", "claude-3", retryAfter)
		err := hm.PersistStateInternal(path)
		Expect(err).NotTo(HaveOccurred())
		newHM := NewHealthManager()
		newHM.persistPath = path
		err = newHM.LoadState(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(newHM.IsRateLimited("anthropic", "claude-3")).To(BeTrue())
		Expect(newHM.ConsecutiveFailures("anthropic", "claude-3")).To(Equal(1))
		Expect(newHM.LastCooldown("anthropic", "claude-3")).To(BeNumerically(">=", 59*time.Minute))
		Expect(newHM.LastCooldown("anthropic", "claude-3")).To(BeNumerically("<=", time.Hour))
		entries := newHM.GetHealthStateEntries()
		Expect(entries).To(HaveLen(1))
		Expect(entries[0].LastFailureAt).To(BeTemporally("~", failureAt, time.Second))
	})

	It("excludes expired persisted cooldowns from active status", func() {
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(-1*time.Hour))
		Expect(hm.PersistStateInternal(path)).To(Succeed())

		fresh := NewHealthManager()
		fresh.persistPath = path
		Expect(fresh.LoadState(path)).To(Succeed())
		Expect(fresh.GetHealthStateEntries()).To(BeEmpty())
		Expect(fresh.IsRateLimited("anthropic", "claude-3")).To(BeFalse())
	})

	It("scores healthier candidates lower", func() {
		now := time.Now()
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(-1*time.Minute))
		hm.MarkRateLimited("openai", "gpt-4", time.Now().Add(-1*time.Minute))
		hm.MarkRateLimited("openzen", "gpt-4", time.Now().Add(-1*time.Hour))

		Expect(hm.HealthScore("openai", "gpt-4", now)).To(BeNumerically(">", hm.HealthScore("openzen", "gpt-4", now)))
		Expect(hm.HealthScore("openzen", "gpt-4", now)).To(BeNumerically("<", hm.HealthScore("anthropic", "claude-3", now)))
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

	// M3 — HealthManager key uses "+" separator collision risk
	// (Bug Hunt May 2026 — Medium severity).
	//
	// The pre-fix key format `provider + "+" + model` is ambiguous when
	// either field contains "+". Two distinct (provider, model) inputs
	// could share one map key and silently over-write each other; the
	// inverse `GetHealthyAlternatives` re-parse split on the first "+"
	// could mis-attribute the boundary. The struct-keyed map closes both
	// halves and keeps the public string-string interface intact.
	Context("M3 — provider/model identifiers containing '+' must not collide", func() {
		It("does not collapse provider='a+b'/model='c' onto provider='a'/model='b+c'", func() {
			expiryA := time.Now().Add(1 * time.Hour)
			expiryB := time.Now().Add(2 * time.Hour)

			hm.MarkRateLimited("a+b", "c", expiryA)
			hm.MarkRateLimited("a", "b+c", expiryB)

			// Both pairs must be tracked independently.
			Expect(hm.IsRateLimited("a+b", "c")).To(BeTrue(),
				"provider='a+b'/model='c' must remain rate-limited after marking provider='a'/model='b+c'")
			Expect(hm.IsRateLimited("a", "b+c")).To(BeTrue(),
				"provider='a'/model='b+c' must remain rate-limited after marking provider='a+b'/model='c'")

			// Their cooldown expiries must be addressable separately.
			gotA, okA := hm.RateLimitedUntil("a+b", "c")
			Expect(okA).To(BeTrue())
			Expect(gotA.Equal(expiryA)).To(BeTrue(),
				"expected expiryA preserved for ('a+b','c'); got %v want %v", gotA, expiryA)

			gotB, okB := hm.RateLimitedUntil("a", "b+c")
			Expect(okB).To(BeTrue())
			Expect(gotB.Equal(expiryB)).To(BeTrue(),
				"expected expiryB preserved for ('a','b+c'); got %v want %v", gotB, expiryB)
		})

		It("preserves provider/model boundary when emitting healthy alternatives for ids containing '+'", func() {
			// Two pairs, both expired in the past so they appear as
			// "healthy" in GetHealthyAlternatives. With the buggy
			// string-key + first-"+"-split re-parse, ('a+b','c') gets
			// emitted as Provider='a', Model='b+c'.
			past := time.Now().Add(-1 * time.Hour)
			hm.MarkRateLimited("a+b", "c", past)
			hm.MarkRateLimited("openrouter", "mistral/mistral-7b+free", past)

			alts := hm.GetHealthyAlternatives("anthropic", "claude-3")
			byPair := make(map[ProviderModel]struct{}, len(alts))
			for _, p := range alts {
				byPair[p] = struct{}{}
			}

			Expect(byPair).To(HaveKey(ProviderModel{Provider: "a+b", Model: "c"}),
				"GetHealthyAlternatives must preserve provider='a+b'/model='c' boundary")
			Expect(byPair).To(HaveKey(ProviderModel{Provider: "openrouter", Model: "mistral/mistral-7b+free"}),
				"GetHealthyAlternatives must preserve provider='openrouter'/model='mistral/mistral-7b+free' boundary")
		})

		It("round-trips provider/model identifiers containing '+' through persist+load", func() {
			expiry := time.Now().Add(1 * time.Hour).Round(time.Second)
			hm.MarkRateLimited("a+b", "c", expiry)
			hm.MarkRateLimited("a", "b+c", expiry)

			Expect(hm.PersistStateInternal(path)).To(Succeed())

			fresh := NewHealthManager()
			fresh.SetPersistPath(path)
			Expect(fresh.LoadState(path)).To(Succeed())

			Expect(fresh.IsRateLimited("a+b", "c")).To(BeTrue(),
				"after persist+load, ('a+b','c') must still be rate-limited")
			Expect(fresh.IsRateLimited("a", "b+c")).To(BeTrue(),
				"after persist+load, ('a','b+c') must still be rate-limited")
		})
	})
})

var _ = Describe("HealthManager persist debounce", func() {
	var (
		dir  string
		path string
		hm   *HealthManager
	)

	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "healthmanager-debounce-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_ = os.RemoveAll(dir)
		})
		path = filepath.Join(dir, "provider-health.json")
		hm = NewHealthManager()
		hm.SetPersistPath(path)
	})

	It("defers persistence for a burst of mutations below the limits", func() {
		for range 5 {
			hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		}
		_, err := os.Stat(path)
		Expect(os.IsNotExist(err)).To(BeTrue())
		Expect(hm.Flush()).To(Succeed())
		_, err = os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	})

	It("persists once the mutation budget is exhausted", func() {
		for range persistMutationLimit {
			hm.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))
		}
		_, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
	})

	It("writes a full snapshot on the mutation-limit flush", func() {
		for range persistMutationLimit {
			hm.MarkRateLimited("openai", "gpt-4", time.Now().Add(1*time.Hour))
		}
		fresh := NewHealthManager()
		fresh.SetPersistPath(path)
		Expect(fresh.LoadState(path)).To(Succeed())
		Expect(fresh.IsRateLimited("openai", "gpt-4")).To(BeTrue())
	})

	It("is a no-op when nothing is dirty", func() {
		Expect(hm.Flush()).To(Succeed())
		_, err := os.Stat(path)
		Expect(os.IsNotExist(err)).To(BeTrue())
	})

	It("flushes outstanding state for shutdown", func() {
		hm.MarkRateLimited("zai", "glm-4.6", time.Now().Add(24*time.Hour))
		Expect(hm.Flush()).To(Succeed())
		fresh := NewHealthManager()
		fresh.SetPersistPath(path)
		Expect(fresh.LoadState(path)).To(Succeed())
		Expect(fresh.IsRateLimited("zai", "glm-4.6")).To(BeTrue())
	})
})

var _ = Describe("HealthManager hard-down circuit breaker", func() {
	var (
		health *HealthManager
	)

	BeforeEach(func() {
		health = NewHealthManager()
		health.SetPersistPath(filepath.Join(GinkgoT().TempDir(), "provider-health.json"))
	})

	Describe("trip thresholds", func() {
		It("trips hard-down on a single billing failure", func() {
			tripped := health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			Expect(tripped).To(BeTrue(), "billing is terminal after one failure")
			Expect(health.IsHardDown("zai", "glm-5")).To(BeTrue())
		})

		It("trips hard-down on a single auth failure carrying a billing-class account code", func() {
			tripped := health.MarkPermanentFailure("openai", "gpt-4o", provider.ErrorTypeAuthFailure, "account_deactivated")
			Expect(tripped).To(BeTrue(), "account_deactivated is a billing-class account state")
			Expect(health.IsHardDown("openai", "gpt-4o")).To(BeTrue())

			health.ResetProviderHealth("openai", "gpt-4o")
			tripped = health.MarkPermanentFailure("openai", "gpt-4o", provider.ErrorTypeAuthFailure, "billing_not_active")
			Expect(tripped).To(BeTrue(), "billing_not_active is a billing-class account state")
			Expect(health.IsHardDown("openai", "gpt-4o")).To(BeTrue())
		})

		It("trips hard-down on the third consecutive auth failure, not sooner", func() {
			Expect(health.MarkPermanentFailure("openai", "gpt-4o", provider.ErrorTypeAuthFailure, "")).To(BeFalse())
			Expect(health.IsHardDown("openai", "gpt-4o")).To(BeFalse(),
				"one auth failure leaves room for an OAuth refresh to recover the credential")

			Expect(health.MarkPermanentFailure("openai", "gpt-4o", provider.ErrorTypeAuthFailure, "")).To(BeFalse())
			Expect(health.IsHardDown("openai", "gpt-4o")).To(BeFalse(),
				"two auth failures still leave one refresh chance")

			Expect(health.MarkPermanentFailure("openai", "gpt-4o", provider.ErrorTypeAuthFailure, "")).To(BeTrue())
			Expect(health.IsHardDown("openai", "gpt-4o")).To(BeTrue(),
				"three consecutive auth failures mean the credential cannot recover")
		})

		It("never trips hard-down on transient classes", func() {
			Expect(health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeRateLimit, "")).To(BeFalse())
			Expect(health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeOverload, "")).To(BeFalse())
			Expect(health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeNetworkError, "")).To(BeFalse())
			Expect(health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeServerError, "")).To(BeFalse())
			Expect(health.IsHardDown("zai", "glm-5")).To(BeFalse(),
				"transient classes keep cooldown semantics and never break the provider")
		})

		It("keeps the hard-down pair visible to IsRateLimited so every consultation site skips it", func() {
			health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			Expect(health.IsRateLimited("zai", "glm-5")).To(BeTrue(),
				"hard-down pairs must be skipped exactly like rate-limited ones")
		})
	})

	Describe("persistence", func() {
		It("survives LoadState past any cooldown expiry", func() {
			health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			Expect(health.Flush()).To(Succeed())

			reloaded := NewHealthManager()
			reloaded.SetPersistPath(health.PersistPath())
			Expect(reloaded.LoadState(health.PersistPath())).To(Succeed())
			Expect(reloaded.IsHardDown("zai", "glm-5")).To(BeTrue(),
				"hard-down is permanent — expiry sweeping must not drop it")
		})

		It("keeps a persisted hard-down entry whose cooldown already expired", func() {
			health.MarkRateLimited("zai", "glm-5", time.Now().Add(-time.Minute))
			health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			Expect(health.Flush()).To(Succeed())

			reloaded := NewHealthManager()
			reloaded.SetPersistPath(health.PersistPath())
			Expect(reloaded.LoadState(health.PersistPath())).To(Succeed())
			Expect(reloaded.IsHardDown("zai", "glm-5")).To(BeTrue(),
				"an expired cooldown on a hard-down entry must not resurrect the provider")
			Expect(reloaded.IsRateLimited("zai", "glm-5")).To(BeTrue())
		})

		It("excludes hard-down pairs from healthy alternatives", func() {
			health.MarkRateLimited("openai", "gpt-4o", time.Now().Add(time.Hour))
			health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			alternatives := health.GetHealthyAlternatives("", "")
			for _, alt := range alternatives {
				Expect(alt.Provider).NotTo(Equal("zai"),
					"hard-down pairs must not be offered as healthy alternatives")
			}
		})

		It("surfaces hard-down entries to the health CLI snapshot", func() {
			health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			entries := health.GetHealthStateEntries()
			var found bool
			for _, e := range entries {
				if e.Provider == "zai" && e.Model == "glm-5" {
					found = e.HardDown
				}
			}
			Expect(found).To(BeTrue(), "the CLI snapshot must carry the hard-down marker")
		})
	})

	Describe("reset", func() {
		It("clears the hard-down state through ResetProviderHealth", func() {
			health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
			Expect(health.ResetProviderHealth("zai", "glm-5")).To(Succeed())
			Expect(health.IsHardDown("zai", "glm-5")).To(BeFalse(),
				"the operator reset path un-trips the breaker")
			Expect(health.IsRateLimited("zai", "glm-5")).To(BeFalse())
		})
	})

	It("persists the trip immediately so a crash cannot lose it", func() {
		health.MarkPermanentFailure("zai", "glm-5", provider.ErrorTypeBilling, "")
		_, statErr := os.Stat(health.PersistPath())
		Expect(statErr).NotTo(HaveOccurred(),
			"hard-down trips must write through the persistence debounce")
	})
})
