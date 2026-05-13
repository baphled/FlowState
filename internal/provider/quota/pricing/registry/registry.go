// Package registry implements the remote pricing-table loader for
// the Provider Quota and Spend Visibility plan (May 2026), §"Pricing
// table" lines 338-388.
//
// The loader is OPT-IN: v1 default is pricing.registry.enabled=false
// (B5 closure per memory feedback_default_urls_must_be_provisioned
// _or_disabled). Operators wanting fresh prices set
// pricing.registry.enabled=true AND supply their own
// pricing.registry.url — there is no FlowState-provisioned default
// URL in v1.
//
// The loader's contract:
//
//   - Fetch the registry URL via HTTP with a 10s timeout.
//   - Use If-None-Match against the cached ETag for incremental
//     refresh — 304 Not Modified leaves the cache untouched.
//   - Persist the cache at ~/.cache/flowstate/pricing-registry.json
//     atomically via internal/atomicwrite.File (per memory
//     feedback_atomicity_awareness_uneven — disk writes default to
//     atomic, even cache files).
//   - Honour a 24h TTL: callers (the refresh ticker in PR5/PR6)
//     consult CacheAge before re-fetching.
//   - Fall back through cache → embedded-default on registry-
//     unreachable; emit a structured warning via slog.
//
// Signed-response verification (Ed25519 per plan R9) is deferred to
// v3 — v1 ships unsigned with the operator-supplied URL as the
// supply-chain mitigation (operators concerned self-host).
package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/atomicwrite"
	"github.com/baphled/flowstate/internal/provider/quota/pricing"
)

// DefaultCacheTTL is the 24-hour TTL the plan pins (§"Pricing table"
// line 345). Operators override via pricing.registry.cache_ttl.
const DefaultCacheTTL = 24 * time.Hour

// DefaultHTTPTimeout is the 10s budget for the registry fetch per
// plan §"Pricing table" lines 381-385. A registry that takes longer
// than this is "unreachable" — the loader falls back to cache or
// embedded.
const DefaultHTTPTimeout = 10 * time.Second

// CacheFileName is the on-disk filename under XDG_CACHE_HOME/flowstate
// the loader writes its cache to. Pinned because PR5/PR6 ticker
// refresh code reads the same path.
const CacheFileName = "pricing-registry.json"

// CacheEnvelope is the on-disk wrapper around the parsed pricing.Table
// plus the ETag the loader matched for incremental refresh. The
// envelope is the v1 cache schema — future schema bumps version the
// outer wrapper, not the inner Table.
//
// FetchedAt drives the CacheAge / TTL check; ETag drives the
// If-None-Match round trip on the next refresh.
type CacheEnvelope struct {
	// SchemaVersion is the envelope schema version. "v1" today.
	SchemaVersion string `json:"schema_version"`

	// FetchedAt is the wall-clock at which the cache was last written.
	// CacheAge = now - FetchedAt; expired when CacheAge > TTL.
	FetchedAt time.Time `json:"fetched_at"`

	// ETag is the value the registry returned in the response's ETag
	// header on the last successful fetch. Empty when the registry
	// did not emit ETag (the loader still caches but cannot do
	// If-None-Match refresh).
	ETag string `json:"etag,omitempty"`

	// URL is the registry URL the cache was last fetched from. Used
	// defensively to invalidate the cache when the operator changes
	// the URL — a stale cache from the old URL is not re-served
	// against the new URL.
	URL string `json:"url"`

	// Table is the parsed pricing table. Stored as the raw JSON the
	// registry returned so future schema bumps inside the table
	// itself (currently v1) don't require an envelope migration.
	Table json.RawMessage `json:"table"`
}

// LoadResult is the outcome of a Load call. Exactly one of Table /
// FromCache / Unreachable is true:
//
//   - Table populated + FromNetwork=true: fresh registry fetch (200).
//   - Table populated + FromCache=true:   cache hit (304 or expired-
//     but-no-network).
//   - Unreachable + Table.Models nil:     no cache, no network — the
//     caller falls back to the embedded baseline.
//
// Plan §"Pricing table" lines 381-385.
type LoadResult struct {
	// Table is the parsed pricing table. Zero (no Models) when the
	// registry was unreachable AND no cache existed.
	Table pricing.Table

	// FromNetwork is true when the table came from a fresh HTTP 200
	// response. FromCache true when the table came from the on-disk
	// cache (either a 304-confirmed cache or an expired-but-no-
	// network fallback).
	FromNetwork bool

	// FromCache is true when the table came from the on-disk cache.
	FromCache bool

	// Unreachable is true when the registry could not be reached AND
	// no cache existed — the caller falls back to embedded.
	Unreachable bool

	// ETag is the cached or freshly-received ETag value, surfaced so
	// the refresh ticker can include it on the next If-None-Match
	// request without re-reading the envelope.
	ETag string

	// FetchedAt is the wall-clock the table was last fetched (cache
	// write-time on a network fetch, cache-stamp time on a 304 or
	// fallback). Drives the next TTL check.
	FetchedAt time.Time

	// FetchErr is the underlying network/parse error when
	// Unreachable=true. nil when the registry returned 200 / 304
	// successfully.
	FetchErr error
}

// LoadOptions configures a Load call. Empty values default to the
// constants above.
type LoadOptions struct {
	// URL is the operator-supplied registry URL. Empty URL means "no
	// registry configured" — Load returns Unreachable without
	// attempting a network fetch and without logging a warning.
	URL string

	// CachePath is the absolute path the loader reads/writes the
	// envelope at. Defaults to filepath.Join(os.UserCacheDir(),
	// "flowstate", CacheFileName) when empty.
	CachePath string

	// TTL is the cache-validity window. Defaults to DefaultCacheTTL.
	TTL time.Duration

	// HTTPTimeout is the per-fetch timeout. Defaults to
	// DefaultHTTPTimeout.
	HTTPTimeout time.Duration

	// HTTPClient is the *http.Client to use for the fetch. Defaults
	// to a fresh client with Timeout=HTTPTimeout — tests inject a
	// fake to avoid the network.
	HTTPClient *http.Client

	// Logger is the slog.Logger the loader writes structured warnings
	// to. Defaults to slog.Default(). Tests pass a buffer-backed
	// logger to assert the warning lines.
	Logger *slog.Logger
}

// DefaultCachePath returns the absolute path the v1 cache lives at:
// $XDG_CACHE_HOME/flowstate/pricing-registry.json (or
// ~/.cache/flowstate/pricing-registry.json on Linux when XDG is
// unset).
//
// Exposed so the boot-time validator can assert the directory exists
// before the first Load call.
func DefaultCachePath() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("registry: resolving user cache dir: %w", err)
	}
	return filepath.Join(base, "flowstate", CacheFileName), nil
}

// Load fetches the registry per the precedence the plan demands:
//
//  1. Read the existing cache (if any).
//  2. If cache is fresh (CacheAge < TTL), return it without a network
//     hit — the refresh ticker handles staleness asynchronously.
//  3. Otherwise issue an HTTP GET with If-None-Match=<cached ETag>.
//     200 → parse, atomicwrite the new cache, return Table.
//     304 → cache is still authoritative; bump FetchedAt and return
//          Table from cache.
//     anything else → fall back to cache (even if expired); emit
//          structured warning; if no cache exists, return
//          Unreachable.
//
// Concurrent Load calls on the same CachePath are safe — the
// atomicwrite.File pattern ensures readers see either the old or new
// envelope, never a torn read. Callers serialise their own writers
// via the refresh ticker's single-goroutine discipline.
//
// Plan §"Pricing table" lines 338-388 (full algorithm).
func Load(ctx context.Context, opts LoadOptions) LoadResult {
	if opts.URL == "" {
		// "Registry not configured" — no error, no warning. The
		// caller's Resolver falls back to the embedded baseline.
		return LoadResult{Unreachable: true}
	}

	cachePath := opts.CachePath
	if cachePath == "" {
		dp, err := DefaultCachePath()
		if err != nil {
			return LoadResult{
				Unreachable: true,
				FetchErr:    err,
			}
		}
		cachePath = dp
	}

	ttl := opts.TTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	// Step 1: read the existing cache (if any).
	cached, cacheReadErr := readCache(cachePath)

	// If cache is fresh AND URL matches, short-circuit — no network
	// hit, no warning. Cache-URL mismatch is treated as a cold start
	// (operator changed the URL, the stale cache is irrelevant).
	if cacheReadErr == nil && cached.URL == opts.URL {
		if time.Since(cached.FetchedAt) < ttl {
			table, parseErr := pricing.ParseTable(cached.Table)
			if parseErr == nil {
				table.Source = pricing.SourceRegistryString(opts.URL)
				return LoadResult{
					Table:       table,
					FromCache:   true,
					ETag:        cached.ETag,
					FetchedAt:   cached.FetchedAt,
				}
			}
			// Cache parse failure is unexpected (the loader wrote it)
			// — log a warning, treat as cold start.
			logger.Warn("pricing_registry_cache_parse_failed",
				slog.String("cache_path", cachePath),
				slog.String("err", parseErr.Error()),
			)
		}
	}

	// Step 2: issue the HTTP fetch.
	client := opts.HTTPClient
	if client == nil {
		timeout := opts.HTTPTimeout
		if timeout <= 0 {
			timeout = DefaultHTTPTimeout
		}
		client = &http.Client{Timeout: timeout}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, opts.URL, nil)
	if err != nil {
		return fallbackToCache(logger, opts.URL, cachePath, cached, cacheReadErr, fmt.Errorf("building request: %w", err))
	}
	if cached.ETag != "" && cached.URL == opts.URL {
		req.Header.Set("If-None-Match", cached.ETag)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fallbackToCache(logger, opts.URL, cachePath, cached, cacheReadErr, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusNotModified:
		// 304 — cache is still authoritative. Bump FetchedAt so the
		// next TTL check measures from now rather than the original
		// fetch.
		if cacheReadErr != nil || cached.URL != opts.URL {
			// Server said "not modified" but we have no cache to
			// serve. Treat as unreachable; the warning surfaces the
			// state mismatch.
			logger.Warn("pricing_registry_304_without_cache",
				slog.String("registry_url", opts.URL),
				slog.String("cache_path", cachePath),
			)
			return LoadResult{Unreachable: true, FetchErr: errors.New("304 Not Modified without local cache")}
		}
		// Bump FetchedAt and rewrite the envelope so the TTL clock
		// resets. Failure to rewrite is non-fatal — we still return
		// the cached table.
		bumped := cached
		bumped.FetchedAt = time.Now()
		if data, mErr := json.Marshal(bumped); mErr == nil {
			_ = atomicwrite.File(cachePath, data, 0o600)
		}
		table, parseErr := pricing.ParseTable(cached.Table)
		if parseErr != nil {
			logger.Warn("pricing_registry_cache_parse_failed",
				slog.String("cache_path", cachePath),
				slog.String("err", parseErr.Error()),
			)
			return LoadResult{Unreachable: true, FetchErr: parseErr}
		}
		table.Source = pricing.SourceRegistryString(opts.URL)
		return LoadResult{
			Table:     table,
			FromCache: true,
			ETag:      cached.ETag,
			FetchedAt: bumped.FetchedAt,
		}

	case http.StatusOK:
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fallbackToCache(logger, opts.URL, cachePath, cached, cacheReadErr, fmt.Errorf("reading response body: %w", readErr))
		}
		table, parseErr := pricing.ParseTable(body)
		if parseErr != nil {
			return fallbackToCache(logger, opts.URL, cachePath, cached, cacheReadErr, fmt.Errorf("parsing registry response: %w", parseErr))
		}

		// Persist the cache atomically.
		newEnvelope := CacheEnvelope{
			SchemaVersion: "v1",
			FetchedAt:     time.Now(),
			ETag:          resp.Header.Get("ETag"),
			URL:           opts.URL,
			Table:         body,
		}
		envelopeBytes, mErr := json.Marshal(newEnvelope)
		if mErr == nil {
			// Best-effort: directory may not exist on first run.
			if mkErr := os.MkdirAll(filepath.Dir(cachePath), 0o700); mkErr == nil {
				if wErr := atomicwrite.File(cachePath, envelopeBytes, 0o600); wErr != nil {
					logger.Warn("pricing_registry_cache_write_failed",
						slog.String("cache_path", cachePath),
						slog.String("err", wErr.Error()),
					)
				}
			}
		}

		table.Source = pricing.SourceRegistryString(opts.URL)
		return LoadResult{
			Table:       table,
			FromNetwork: true,
			ETag:        newEnvelope.ETag,
			FetchedAt:   newEnvelope.FetchedAt,
		}

	default:
		return fallbackToCache(logger, opts.URL, cachePath, cached, cacheReadErr, fmt.Errorf("registry returned status %d", resp.StatusCode))
	}
}

// fallbackToCache encapsulates the "registry unreachable, use cache
// if present, otherwise return Unreachable" branch the plan
// prescribes at lines 381-385. Emits the structured warning the plan
// names ("pricing_registry_unreachable").
func fallbackToCache(logger *slog.Logger, url, cachePath string, cached CacheEnvelope, cacheReadErr error, fetchErr error) LoadResult {
	logger.Warn("pricing_registry_unreachable",
		slog.String("registry_url", url),
		slog.String("cache_path", cachePath),
		slog.String("err", fetchErr.Error()),
	)
	if cacheReadErr != nil {
		// No cache at all — caller falls back to embedded baseline.
		return LoadResult{Unreachable: true, FetchErr: fetchErr}
	}
	if cached.URL != url {
		// Cache exists but for a different URL — treat as no cache
		// to avoid serving stale data from a registry the operator
		// no longer points at.
		return LoadResult{Unreachable: true, FetchErr: fetchErr}
	}
	table, parseErr := pricing.ParseTable(cached.Table)
	if parseErr != nil {
		return LoadResult{Unreachable: true, FetchErr: parseErr}
	}
	table.Source = pricing.SourceRegistryString(url)
	return LoadResult{
		Table:     table,
		FromCache: true,
		ETag:      cached.ETag,
		FetchedAt: cached.FetchedAt,
		FetchErr:  fetchErr,
	}
}

// readCache reads and parses the envelope at path. Returns the
// envelope and a nil error on success. Returns the zero envelope and
// an error when the file is missing, unreadable, or malformed —
// callers treat any error as "no cache".
func readCache(path string) (CacheEnvelope, error) {
	if path == "" {
		return CacheEnvelope{}, errors.New("registry: empty cache path")
	}
	data, err := os.ReadFile(path) //nolint:gosec // operator-configured cache path is intentional
	if err != nil {
		return CacheEnvelope{}, err
	}
	var env CacheEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return CacheEnvelope{}, err
	}
	if env.SchemaVersion != "v1" {
		return CacheEnvelope{}, fmt.Errorf("registry: unsupported cache schema version %q", env.SchemaVersion)
	}
	if len(env.Table) == 0 {
		return CacheEnvelope{}, errors.New("registry: cache envelope missing table")
	}
	return env, nil
}
