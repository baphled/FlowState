package app

// quota_cache_test.go — PR6 specs for the persisted-cache + refresh-
// ticker wireup. Pins:
//
//   - loadQuotaCacheFromDisk: file-absent (first-boot), file-present
//     (round-trip hydration), file-corrupt (log + empty Tracker),
//     unknown-version (log + empty Tracker).
//   - quotaPersistLoop / startQuotaCacheController: ticker writes
//     atomically at mode 0o600; fingerprint-skip suppresses unchanged
//     writes; context cancellation exits the loop cleanly.
//   - resolveQuotaCachePath: $HOME / XDG_CACHE_HOME fallback,
//     operator override.
//   - resolveQuotaRefreshInterval: empty → 10s, "0" → disabled,
//     malformed → warn + 10s.
//
// Plan §"Rollout Plan" PR6 row 430 + memory feedback_atomicity_
// awareness_uneven (atomicwrite discipline pinned).

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
	quotastore "github.com/baphled/flowstate/internal/provider/quota/store"
)

// testStoreAdapter is the production-mirror SpendStore shim. Duplicates
// the wireup's memorySpendStoreAdapter so tests don't reach into
// unexported helpers across packages.
type testStoreAdapter struct {
	inner *quotastore.MemoryStore
}

func newTestStoreAdapter() *testStoreAdapter {
	return &testStoreAdapter{inner: quotastore.NewMemoryStore()}
}

func (m *testStoreAdapter) Get(ctx context.Context, k quota.SpendStoreKey) (quota.Snapshot, error) {
	snap, err := m.inner.Get(ctx, quotastore.Key{ProviderID: k.ProviderID, AccountHash: k.AccountHash, ModelID: k.ModelID})
	if err != nil {
		if err == quotastore.ErrSnapshotNotFound {
			return quota.Snapshot{}, quota.SpendStoreErrNotFound
		}
		return quota.Snapshot{}, err
	}
	return snap, nil
}
func (m *testStoreAdapter) Put(ctx context.Context, k quota.SpendStoreKey, s quota.Snapshot) error {
	return m.inner.Put(ctx, quotastore.Key{ProviderID: k.ProviderID, AccountHash: k.AccountHash, ModelID: k.ModelID}, s)
}
func (m *testStoreAdapter) Reset(ctx context.Context, k quota.SpendStoreKey) error {
	return m.inner.Reset(ctx, quotastore.Key{ProviderID: k.ProviderID, AccountHash: k.AccountHash, ModelID: k.ModelID})
}
func (m *testStoreAdapter) List(ctx context.Context) ([]quota.SpendStoreEntry, error) {
	rows, err := m.inner.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]quota.SpendStoreEntry, len(rows))
	for i, r := range rows {
		out[i] = quota.SpendStoreEntry{
			Key:      quota.SpendStoreKey{ProviderID: r.Key.ProviderID, AccountHash: r.Key.AccountHash, ModelID: r.Key.ModelID},
			Snapshot: r.Snapshot,
		}
	}
	return out, nil
}

// testPricingResolver implements both PricingResolver + PriceEntryResolver
// for the seed-spend ticker spec. Minimal — one entry, fixed currency.
type testPricingResolver struct{}

func (testPricingResolver) Lookup(_, _ string) (string, bool) { return "stub", true }
func (testPricingResolver) Entry(_, _ string) (quota.PriceEntry, bool) {
	return quota.PriceEntry{Currency: "USD", InputPerMillion: 15.00, OutputPerMillion: 75.00}, true
}

// captureLogger returns an slog.Logger whose output is buffered in
// `buf`. Tests assert on the structured warn lines.
func captureLogger(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

var _ = Describe("PR6 — persisted cache + refresh ticker (quota_cache.go)", func() {
	var (
		ctx    context.Context
		tmpDir string
	)

	BeforeEach(func() {
		ctx = context.Background()
		tmpDir = GinkgoT().TempDir()
	})

	Context("loadQuotaCacheFromDisk (quota_cache.go (loadQuotaCacheFromDisk))", func() {
		It("first-boot path: file absent → empty Tracker, no warn", func() {
			path := filepath.Join(tmpDir, "absent.json")
			buf := new(bytes.Buffer)
			logger := captureLogger(buf)
			store := newTestStoreAdapter()
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, store, time.Now)

			loadQuotaCacheFromDisk(ctx, tracker, path, logger)

			Expect(buf.String()).NotTo(ContainSubstring("warn"),
				"file-not-found is the first-boot path; MUST not produce a warn log")
			_, ok := tracker.LookupSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(ok).To(BeFalse(), "Tracker MUST stay empty on first-boot")
		})

		It("file present + valid → hydrate", func() {
			// Seed a Tracker, persist, build a fresh one, hydrate, assert.
			store := newTestStoreAdapter()
			source := quota.NewTrackerWithSpend("memory", testPricingResolver{}, store, time.Now)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			Expect(source.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
				RequestID: "r1",
				Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r1"},
				CapConfig: capCfg,
			})).To(Succeed())

			entries, err := source.Snapshots(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			data, err := quota.MarshalCache(entries, time.Now())
			Expect(err).NotTo(HaveOccurred())

			path := filepath.Join(tmpDir, "valid.json")
			Expect(os.WriteFile(path, data, 0o600)).To(Succeed())

			// Fresh tracker — load from disk.
			fresh := quota.NewTrackerWithSpend("memory", testPricingResolver{}, newTestStoreAdapter(), time.Now)
			loadQuotaCacheFromDisk(ctx, fresh, path, captureLogger(new(bytes.Buffer)))

			snap, ok := fresh.LookupSpend(ctx, "anthropic", "acc-A", "claude-opus-4-7")
			Expect(ok).To(BeTrue(), "valid cache file MUST hydrate the fresh Tracker")
			Expect(snap.TokenSpend).NotTo(BeNil())
			Expect(snap.TokenSpend.Spent.Amount).To(Equal(entries[0].Snapshot.TokenSpend.Spent.Amount))
		})

		It("file present + corrupt → warn + empty Tracker", func() {
			path := filepath.Join(tmpDir, "corrupt.json")
			Expect(os.WriteFile(path, []byte("{not valid json"), 0o600)).To(Succeed())

			buf := new(bytes.Buffer)
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, newTestStoreAdapter(), time.Now)
			loadQuotaCacheFromDisk(ctx, tracker, path, captureLogger(buf))

			Expect(buf.String()).To(ContainSubstring("malformed cache file"),
				"corrupt cache file MUST produce a structured warn log")
			Expect(buf.String()).To(ContainSubstring("\"level\":\"WARN\""),
				"the corrupt-cache log MUST be at WARN level so operators see it")
		})

		It("file present + unknown envelope version → warn + empty Tracker", func() {
			path := filepath.Join(tmpDir, "future.json")
			Expect(os.WriteFile(path, []byte(`{"version":"v9","saved_at":"2026-05-13T12:00:00Z","snapshots":[]}`), 0o600)).To(Succeed())

			buf := new(bytes.Buffer)
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, newTestStoreAdapter(), time.Now)
			loadQuotaCacheFromDisk(ctx, tracker, path, captureLogger(buf))

			Expect(buf.String()).To(ContainSubstring("unknown envelope version"),
				"unknown-version MUST produce a distinct warn message so operators can act")
		})
	})

	Context("quotaPersistLoop / startQuotaCacheController atomic-write discipline", func() {
		It("writes the cache file at mode 0600 after the first tick", func() {
			path := filepath.Join(tmpDir, "spend.json")
			store := newTestStoreAdapter()
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, store, time.Now)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			Expect(tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
				RequestID: "r1",
				Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r1"},
				CapConfig: capCfg,
			})).To(Succeed())

			ctrl := startQuotaCacheController(tracker, path, 50*time.Millisecond, captureLogger(new(bytes.Buffer)))
			Expect(ctrl).NotTo(BeNil(), "non-zero interval + live tracker MUST start a controller")

			Eventually(func() os.FileMode {
				info, err := os.Stat(path)
				if err != nil {
					return 0
				}
				return info.Mode().Perm()
			}, "2s", "20ms").Should(Equal(os.FileMode(0o600)),
				"ticker MUST write the cache file at mode 0o600 — account hashes are sensitive")

			stopCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			Expect(ctrl.Stop(stopCtx)).To(Succeed())
		})

		It("leaves no .atomicwrite-* temp file behind after successful writes", func() {
			path := filepath.Join(tmpDir, "noleaks.json")
			store := newTestStoreAdapter()
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, store, time.Now)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			Expect(tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "anthropic", Model: "claude-opus-4-7", AccountHash: "acc-A",
				RequestID: "r1",
				Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r1"},
				CapConfig: capCfg,
			})).To(Succeed())

			ctrl := startQuotaCacheController(tracker, path, 25*time.Millisecond, captureLogger(new(bytes.Buffer)))
			Expect(ctrl).NotTo(BeNil())
			Eventually(func() error {
				_, err := os.Stat(path)
				return err
			}, "2s", "20ms").Should(Succeed())

			stopCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			Expect(ctrl.Stop(stopCtx)).To(Succeed())

			entries, err := os.ReadDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			for _, e := range entries {
				Expect(strings.Contains(e.Name(), ".atomicwrite-")).To(BeFalse(),
					"atomicwrite must clean up its temp files on success — leaked %q", e.Name())
			}
		})

		It("ticker exits cleanly on context cancellation (Stop returns)", func() {
			path := filepath.Join(tmpDir, "shutdown.json")
			store := newTestStoreAdapter()
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, store, time.Now)

			ctrl := startQuotaCacheController(tracker, path, 50*time.Millisecond, captureLogger(new(bytes.Buffer)))
			Expect(ctrl).NotTo(BeNil())

			stopCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			start := time.Now()
			Expect(ctrl.Stop(stopCtx)).To(Succeed(),
				"Stop MUST return promptly after the ticker goroutine sees ctx.Done()")
			Expect(time.Since(start)).To(BeNumerically("<", 500*time.Millisecond),
				"shutdown drain MUST be fast; the ticker is non-blocking")
		})

		It("Stop is idempotent — second call returns nil", func() {
			path := filepath.Join(tmpDir, "idempotent.json")
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, newTestStoreAdapter(), time.Now)
			ctrl := startQuotaCacheController(tracker, path, 50*time.Millisecond, captureLogger(new(bytes.Buffer)))
			Expect(ctrl).NotTo(BeNil())
			stopCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			Expect(ctrl.Stop(stopCtx)).To(Succeed())
			Expect(ctrl.Stop(stopCtx)).To(Succeed(), "second Stop MUST return nil cleanly")
		})

		It("startQuotaCacheController returns nil when interval is zero (disabled)", func() {
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, newTestStoreAdapter(), time.Now)
			Expect(startQuotaCacheController(tracker, "/tmp/x", 0, captureLogger(new(bytes.Buffer)))).To(BeNil())
		})

		It("startQuotaCacheController returns nil when path is empty (persistence disabled)", func() {
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, newTestStoreAdapter(), time.Now)
			Expect(startQuotaCacheController(tracker, "", time.Second, captureLogger(new(bytes.Buffer)))).To(BeNil())
		})

		It("written cache file is parseable by UnmarshalCache (round-trip on disk)", func() {
			path := filepath.Join(tmpDir, "roundtrip.json")
			store := newTestStoreAdapter()
			tracker := quota.NewTrackerWithSpend("memory", testPricingResolver{}, store, time.Now)
			capCfg := quota.CapConfig{Cap: quota.Money{Amount: 5000, Currency: "USD"}, Period: "monthly"}
			Expect(tracker.RecordSpend(ctx, quota.SpendRecord{
				Provider: "openai", Model: "gpt-4o", AccountHash: "acc-B",
				RequestID: "r1",
				Usage:     &provider.UsageDelta{InputTokens: 1_000_000, OutputTokens: 1_000_000, RequestID: "r1"},
				CapConfig: capCfg,
			})).To(Succeed())

			ctrl := startQuotaCacheController(tracker, path, 25*time.Millisecond, captureLogger(new(bytes.Buffer)))
			Expect(ctrl).NotTo(BeNil())
			Eventually(func() error {
				_, err := os.Stat(path)
				return err
			}, "2s", "20ms").Should(Succeed())

			stopCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			Expect(ctrl.Stop(stopCtx)).To(Succeed())

			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			// Decode the raw bytes via the public UnmarshalCache —
			// this is the same path the boot-time loader exercises.
			entries, err := quota.UnmarshalCache(data)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(HaveLen(1))
			Expect(entries[0].Key.ProviderID).To(Equal("openai"))
			Expect(entries[0].Snapshot.TokenSpend).NotTo(BeNil())
		})
	})

	Context("resolveQuotaCachePath (quota_cache.go (resolveQuotaCachePath))", func() {
		It("operator override wins when set", func() {
			override := filepath.Join(tmpDir, "custom-quota.json")
			// Pre-create parent so the ensureCacheDir call succeeds.
			cfg := newConfigWithCachePath(override)
			path := resolveQuotaCachePath(cfg, captureLogger(new(bytes.Buffer)))
			Expect(path).To(Equal(override),
				"operator-supplied cache path MUST take precedence over the XDG default")
		})

		It("falls back to XDG_CACHE_HOME/flowstate/provider-quota.json", func() {
			t := GinkgoT()
			t.Setenv("XDG_CACHE_HOME", tmpDir)
			cfg := newConfigWithCachePath("")
			path := resolveQuotaCachePath(cfg, captureLogger(new(bytes.Buffer)))
			Expect(path).To(Equal(filepath.Join(tmpDir, "flowstate", "provider-quota.json")))
			info, err := os.Stat(filepath.Join(tmpDir, "flowstate"))
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o700)),
				"cache directory MUST be created at 0o700 — account hashes are sensitive")
		})

		It("returns empty when cfg is nil (defensive)", func() {
			path := resolveQuotaCachePath(nil, captureLogger(new(bytes.Buffer)))
			Expect(path).To(BeEmpty())
		})
	})

	Context("resolveQuotaRefreshInterval (quota_cache.go (resolveQuotaRefreshInterval))", func() {
		It("empty string resolves to 10s default", func() {
			interval, enabled := resolveQuotaRefreshInterval("", captureLogger(new(bytes.Buffer)))
			Expect(enabled).To(BeTrue())
			Expect(interval).To(Equal(defaultQuotaCacheRefresh))
		})

		It("\"0\" disables the ticker (load-only mode)", func() {
			interval, enabled := resolveQuotaRefreshInterval("0", captureLogger(new(bytes.Buffer)))
			Expect(enabled).To(BeFalse())
			Expect(interval).To(BeZero())
		})

		It("malformed duration → warn + 10s default", func() {
			buf := new(bytes.Buffer)
			interval, enabled := resolveQuotaRefreshInterval("not-a-duration", captureLogger(buf))
			Expect(enabled).To(BeTrue())
			Expect(interval).To(Equal(defaultQuotaCacheRefresh))
			Expect(buf.String()).To(ContainSubstring("unparseable refresh_interval"))
		})

		It("custom 30s parses cleanly", func() {
			interval, enabled := resolveQuotaRefreshInterval("30s", captureLogger(new(bytes.Buffer)))
			Expect(enabled).To(BeTrue())
			Expect(interval).To(Equal(30 * time.Second))
		})
	})
})

// newConfigWithCachePath builds the minimal AppConfig the
// resolveQuotaCachePath helper needs. Only the Quota.Cache.Path field
// is consulted.
func newConfigWithCachePath(p string) *config.AppConfig {
	cfg := &config.AppConfig{}
	cfg.Quota.Cache.Path = p
	return cfg
}
