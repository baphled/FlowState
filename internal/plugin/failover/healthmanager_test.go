package failover_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plugin/failover"
)

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
		err := hm.MarkRateLimited("anthropic", "claude-3", time.Now().Add(1*time.Hour))
		Expect(err).NotTo(HaveOccurred())
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
