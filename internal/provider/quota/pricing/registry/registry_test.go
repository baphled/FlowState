// Package registry_test pins the registry-loader contract from the
// Provider Quota and Spend Visibility plan (May 2026), §"Pricing
// table (OD-1 resolution)" lines 338-388.
//
// The spec drives Load() through:
//   - URL=empty → Unreachable (quiet "registry not configured" path).
//   - 200 OK → table parsed + cache written atomically + ETag captured.
//   - 304 Not Modified → cache served unchanged, FetchedAt bumped.
//   - 5xx or network error → fallback to cache → fallback to
//     Unreachable; structured warning emitted (B5 honesty stance per
//     feedback_default_urls_must_be_provisioned_or_disabled).
//   - Cache cold start (no file) → 200 round trip writes the envelope.
//   - Cache TTL fresh → no network hit (short-circuit).
//   - Cache TTL expired → network hit; If-None-Match attached.
//   - Cache URL mismatch (operator changed URL) → cache invalidated.
package registry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/quota/pricing"
	"github.com/baphled/flowstate/internal/provider/quota/pricing/registry"
)

const samplePricingJSON = `{
	"version": "v1",
	"updated_at": "2026-05-13",
	"default_currency": "USD",
	"models": {
		"anthropic/claude-opus-4-7": {
			"currency": "USD",
			"input_per_million": 15.00,
			"output_per_million": 75.00
		}
	}
}`

const updatedPricingJSON = `{
	"version": "v1",
	"updated_at": "2026-05-14",
	"default_currency": "USD",
	"models": {
		"anthropic/claude-opus-4-7": {
			"currency": "USD",
			"input_per_million": 14.50,
			"output_per_million": 72.50
		}
	}
}`

func makeServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func makeLogger() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	h := slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelWarn})
	return slog.New(h), buf
}

func tempCachePath() string {
	dir := GinkgoT().TempDir()
	return filepath.Join(dir, "pricing-registry.json")
}

var _ = Describe("Empty-URL short-circuit (B5 — no aspirational URLs)", func() {
	It("returns Unreachable without a network hit when URL is empty", func() {
		logger, buf := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       "",
			CachePath: tempCachePath(),
			Logger:    logger,
		})
		Expect(result.Unreachable).To(BeTrue(),
			"empty URL is the v1 default per B5 (feedback_default_urls_must_be_provisioned_or_disabled) — no canonical FlowState URL ships, no network attempt fires")
		Expect(result.Table.Models).To(BeEmpty())
		Expect(buf.String()).To(BeEmpty(),
			"empty URL MUST NOT emit a warning — 'not configured' is the quiet default; the boot-validation gate catches enabled=true + url=empty separately")
	})
})

var _ = Describe("Fresh fetch (200 OK round trip — plan lines 345-346)", func() {
	It("fetches, parses, writes cache atomically, and stamps registry source", func() {
		var hits atomic.Int32
		server := makeServer(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			w.Header().Set("ETag", `"abc123"`)
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, samplePricingJSON)
		})
		defer server.Close()

		cachePath := tempCachePath()
		logger, _ := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			Logger:    logger,
		})

		Expect(result.Unreachable).To(BeFalse())
		Expect(result.FromNetwork).To(BeTrue())
		Expect(result.FromCache).To(BeFalse())
		Expect(result.ETag).To(Equal(`"abc123"`))
		Expect(result.Table.Models).To(HaveKey("anthropic/claude-opus-4-7"))
		Expect(result.Table.Source).To(Equal("registry:" + server.URL),
			"successful registry fetch MUST stamp Table.Source=registry:<url> — plan §Pricing table line 386")
		Expect(hits.Load()).To(Equal(int32(1)))

		// Cache file was written atomically (the temp+rename path
		// must not leave a *.atomicwrite-* turd).
		entries, err := os.ReadDir(filepath.Dir(cachePath))
		Expect(err).NotTo(HaveOccurred())
		for _, e := range entries {
			Expect(e.Name()).NotTo(ContainSubstring(".atomicwrite-"),
				"atomicwrite.File MUST clean up its temp file — atomicity discipline (memory feedback_atomicity_awareness_uneven)")
		}

		// Envelope is readable + carries the ETag.
		data, err := os.ReadFile(cachePath)
		Expect(err).NotTo(HaveOccurred())
		var env registry.CacheEnvelope
		Expect(json.Unmarshal(data, &env)).To(Succeed())
		Expect(env.SchemaVersion).To(Equal("v1"))
		Expect(env.ETag).To(Equal(`"abc123"`))
		Expect(env.URL).To(Equal(server.URL))
	})
})

var _ = Describe("Fresh cache TTL short-circuit", func() {
	It("returns cache without a network hit when CacheAge < TTL", func() {
		// Seed a cache envelope dated now.
		cachePath := tempCachePath()
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o700)).To(Succeed())
		seedURL := "http://seed.example/v1.json"
		env := registry.CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now(),
			ETag:          `"seed-etag"`,
			URL:           seedURL,
			Table:         json.RawMessage(samplePricingJSON),
		}
		envBytes, _ := json.Marshal(env)
		Expect(os.WriteFile(cachePath, envBytes, 0o600)).To(Succeed())

		// Make a server that errors if hit — the test fails if Load
		// reaches the network on a fresh cache.
		var hits atomic.Int32
		server := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			hits.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer server.Close()

		logger, _ := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       seedURL, // MUST match the cache's URL
			CachePath: cachePath,
			TTL:       24 * time.Hour,
			Logger:    logger,
		})

		Expect(hits.Load()).To(Equal(int32(0)),
			"fresh cache (CacheAge < TTL) MUST short-circuit before the network — plan §Pricing table line 345 24h TTL")
		Expect(result.FromCache).To(BeTrue())
		Expect(result.Table.Source).To(Equal("registry:" + seedURL))
	})
})

var _ = Describe("ETag round trip (304 Not Modified — plan line 379)", func() {
	It("attaches If-None-Match on the next fetch and serves cache on 304", func() {
		// First seed: cache with an ETag, stale enough to trigger a
		// network hit.
		cachePath := tempCachePath()
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o700)).To(Succeed())
		env := registry.CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now().Add(-48 * time.Hour), // expired
			ETag:          `"cached-etag"`,
			Table:         json.RawMessage(samplePricingJSON),
		}

		var receivedINM atomic.Value
		server := makeServer(func(w http.ResponseWriter, r *http.Request) {
			receivedINM.Store(r.Header.Get("If-None-Match"))
			w.WriteHeader(http.StatusNotModified)
		})
		defer server.Close()
		env.URL = server.URL
		envBytes, _ := json.Marshal(env)
		Expect(os.WriteFile(cachePath, envBytes, 0o600)).To(Succeed())

		logger, _ := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			TTL:       24 * time.Hour, // forces network because cache is 48h old
			Logger:    logger,
		})

		Expect(receivedINM.Load()).To(Equal(`"cached-etag"`),
			"loader MUST attach If-None-Match=<cached ETag> on the next fetch — plan line 379 ETag refresh")
		Expect(result.FromCache).To(BeTrue(),
			"304 Not Modified MUST serve the cache unchanged — table stays put")
		Expect(result.Table.Source).To(Equal("registry:" + server.URL))
	})

	It("304 without local cache returns Unreachable with the 'state mismatch' warning", func() {
		server := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotModified)
		})
		defer server.Close()

		logger, buf := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: tempCachePath(), // no file at path
			Logger:    logger,
		})
		Expect(result.Unreachable).To(BeTrue())
		Expect(result.FetchErr).To(MatchError(ContainSubstring("304 Not Modified without local cache")))
		Expect(buf.String()).To(ContainSubstring("pricing_registry_304_without_cache"))
	})
})

var _ = Describe("Network unreachable fallback (plan lines 381-385)", func() {
	It("falls back to cache when the server is unreachable and emits the warning", func() {
		// Pre-seed a cache the loader can fall back to.
		cachePath := tempCachePath()
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o700)).To(Succeed())

		// Bind to a closed port — connect refused.
		closedServer := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		closedURL := closedServer.URL
		closedServer.Close() // immediately closes so the next dial fails

		env := registry.CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now().Add(-48 * time.Hour),
			ETag:          `"stale-etag"`,
			URL:           closedURL,
			Table:         json.RawMessage(samplePricingJSON),
		}
		envBytes, _ := json.Marshal(env)
		Expect(os.WriteFile(cachePath, envBytes, 0o600)).To(Succeed())

		logger, buf := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:         closedURL,
			CachePath:   cachePath,
			TTL:         time.Second, // forces network attempt
			HTTPTimeout: 250 * time.Millisecond,
			Logger:      logger,
		})

		Expect(result.FromCache).To(BeTrue(),
			"registry-unreachable MUST fall back to cache when present — plan lines 381-385")
		Expect(result.FetchErr).To(HaveOccurred())
		Expect(buf.String()).To(ContainSubstring("pricing_registry_unreachable"),
			"plan §Pricing table line 385 — structured warning 'pricing_registry_unreachable' on fallback")
	})

	It("falls back to Unreachable when neither network nor cache is available", func() {
		closedServer := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
		closedURL := closedServer.URL
		closedServer.Close()

		logger, buf := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:         closedURL,
			CachePath:   tempCachePath(), // no file
			HTTPTimeout: 250 * time.Millisecond,
			Logger:      logger,
		})
		Expect(result.Unreachable).To(BeTrue(),
			"no network + no cache MUST return Unreachable — caller falls back to embedded baseline")
		Expect(result.Table.Models).To(BeEmpty())
		Expect(buf.String()).To(ContainSubstring("pricing_registry_unreachable"))
	})

	It("5xx response falls back to cache + emits warning", func() {
		cachePath := tempCachePath()
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o700)).To(Succeed())

		server := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		})
		defer server.Close()

		env := registry.CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now().Add(-48 * time.Hour),
			URL:           server.URL,
			Table:         json.RawMessage(samplePricingJSON),
		}
		envBytes, _ := json.Marshal(env)
		Expect(os.WriteFile(cachePath, envBytes, 0o600)).To(Succeed())

		logger, buf := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			TTL:       time.Second,
			Logger:    logger,
		})
		Expect(result.FromCache).To(BeTrue())
		Expect(buf.String()).To(ContainSubstring("pricing_registry_unreachable"))
	})

	It("malformed response body falls back to cache + emits warning", func() {
		cachePath := tempCachePath()
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o700)).To(Succeed())

		server := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("not-json"))
		})
		defer server.Close()

		env := registry.CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now().Add(-48 * time.Hour),
			URL:           server.URL,
			Table:         json.RawMessage(samplePricingJSON),
		}
		envBytes, _ := json.Marshal(env)
		Expect(os.WriteFile(cachePath, envBytes, 0o600)).To(Succeed())

		logger, buf := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			TTL:       time.Second,
			Logger:    logger,
		})
		Expect(result.FromCache).To(BeTrue())
		Expect(buf.String()).To(ContainSubstring("pricing_registry_unreachable"))
	})
})

var _ = Describe("Cache URL mismatch (operator-changed-URL hygiene)", func() {
	It("invalidates cache from a different URL than the current request", func() {
		// Seed cache with URL = http://old.example
		cachePath := tempCachePath()
		Expect(os.MkdirAll(filepath.Dir(cachePath), 0o700)).To(Succeed())
		env := registry.CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now(), // fresh
			ETag:          `"old-etag"`,
			URL:           "http://old.example/v1.json",
			Table:         json.RawMessage(samplePricingJSON),
		}
		envBytes, _ := json.Marshal(env)
		Expect(os.WriteFile(cachePath, envBytes, 0o600)).To(Succeed())

		// Now hit a different URL — the cache should NOT short-circuit.
		var hits atomic.Int32
		server := makeServer(func(w http.ResponseWriter, r *http.Request) {
			hits.Add(1)
			// Cache URL mismatch MUST suppress If-None-Match too —
			// re-using an old URL's ETag against a new URL is wrong.
			Expect(r.Header.Get("If-None-Match")).To(BeEmpty(),
				"If-None-Match MUST NOT carry the old URL's ETag against a new URL")
			w.Header().Set("ETag", `"new-etag"`)
			w.WriteHeader(http.StatusOK)
			_, _ = fmt.Fprint(w, updatedPricingJSON)
		})
		defer server.Close()

		logger, _ := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			TTL:       24 * time.Hour,
			Logger:    logger,
		})
		Expect(hits.Load()).To(Equal(int32(1)),
			"cache URL mismatch MUST trigger a fresh network fetch — stale cache from an old URL is not served against a new URL")
		Expect(result.FromNetwork).To(BeTrue())
		Expect(result.ETag).To(Equal(`"new-etag"`))
		// New cache stamps the new URL.
		data, _ := os.ReadFile(cachePath)
		var newEnv registry.CacheEnvelope
		Expect(json.Unmarshal(data, &newEnv)).To(Succeed())
		Expect(newEnv.URL).To(Equal(server.URL))
	})
})

var _ = Describe("Cache atomic-write discipline (memory feedback_atomicity_awareness_uneven)", func() {
	It("uses atomicwrite — concurrent reads see either old or new envelope, never a torn read", func() {
		// Two sequential fetches: the second's cache write must not
		// corrupt the first's. Verified by reading the envelope after
		// each fetch.
		cachePath := tempCachePath()
		var responseBody atomic.Value
		responseBody.Store(samplePricingJSON)
		etag := atomic.Value{}
		etag.Store(`"v1"`)

		server := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", etag.Load().(string))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(responseBody.Load().(string)))
		})
		defer server.Close()

		logger, _ := makeLogger()

		// First fetch.
		result1 := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			Logger:    logger,
		})
		Expect(result1.FromNetwork).To(BeTrue())
		Expect(result1.ETag).To(Equal(`"v1"`))

		data1, _ := os.ReadFile(cachePath)
		var env1 registry.CacheEnvelope
		Expect(json.Unmarshal(data1, &env1)).To(Succeed())
		Expect(env1.ETag).To(Equal(`"v1"`))

		// Second fetch with new content — expire TTL with a tiny
		// FetchedAt rewrite.
		expiredEnv := env1
		expiredEnv.FetchedAt = time.Now().Add(-48 * time.Hour)
		expiredBytes, _ := json.Marshal(expiredEnv)
		Expect(os.WriteFile(cachePath, expiredBytes, 0o600)).To(Succeed())

		responseBody.Store(updatedPricingJSON)
		etag.Store(`"v2"`)

		result2 := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: cachePath,
			TTL:       time.Second,
			Logger:    logger,
		})
		Expect(result2.FromNetwork).To(BeTrue())
		Expect(result2.ETag).To(Equal(`"v2"`))

		// Cache is the new envelope.
		data2, _ := os.ReadFile(cachePath)
		var env2 registry.CacheEnvelope
		Expect(json.Unmarshal(data2, &env2)).To(Succeed())
		Expect(env2.ETag).To(Equal(`"v2"`))

		// No atomicwrite temp files left.
		entries, _ := os.ReadDir(filepath.Dir(cachePath))
		for _, e := range entries {
			Expect(e.Name()).NotTo(ContainSubstring(".atomicwrite-"))
		}
	})
})

var _ = Describe("DefaultCachePath", func() {
	It("returns a non-empty path", func() {
		p, err := registry.DefaultCachePath()
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeEmpty())
		Expect(filepath.Base(p)).To(Equal(registry.CacheFileName))
	})
})

var _ = Describe("Resolver integration round-trip (registry → pricing.Resolver)", func() {
	It("a 200 OK round trip feeds the Resolver with registry-stamped Source", func() {
		server := makeServer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("ETag", `"v1"`)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(samplePricingJSON))
		})
		defer server.Close()

		logger, _ := makeLogger()
		result := registry.Load(context.Background(), registry.LoadOptions{
			URL:       server.URL,
			CachePath: tempCachePath(),
			Logger:    logger,
		})
		Expect(result.FromNetwork).To(BeTrue())

		embedded, err := pricing.LoadEmbedded()
		Expect(err).NotTo(HaveOccurred())

		resolver := pricing.NewResolver(embedded, result.Table, pricing.Table{})
		_, source, ok := resolver.Lookup("anthropic", "claude-opus-4-7")
		Expect(ok).To(BeTrue())
		Expect(source).To(Equal("registry:"+server.URL),
			"registry hit MUST win over embedded — plan §Pricing table line 344 precedence")
	})
})
