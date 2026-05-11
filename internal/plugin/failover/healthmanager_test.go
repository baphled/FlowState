package failover

import (
	"os"
	"path/filepath"
	"time"

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
		now := time.Now().Add(1 * time.Hour)
		hm.MarkRateLimited("anthropic", "claude-3", now)
		err := hm.PersistStateInternal(path)
		Expect(err).NotTo(HaveOccurred())
		newHM := NewHealthManager()
		newHM.persistPath = path
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
