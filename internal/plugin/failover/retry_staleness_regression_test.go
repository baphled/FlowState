package failover

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/provider"
)

// R2-02/R2-03 regression suite: bounded retry, backoff, cancellation,
// error classification and attempt-time candidate rechecks in
// StreamHook.Execute. All waits go through an injected sleeper — no
// real backoff delays in tests. Attempt counts are captured exactly.

// retryFixture wires a StreamHook over a delivery-scoped manager with
// an injected sleeper and a counting, scripted dispatch handler.
type retryFixture struct {
	m       *Manager
	hm      *HealthManager
	slept   []time.Duration
	calls   []provider.ModelPreference
	script  func(call int, p provider.ModelPreference) error
	hook    *StreamHook
	wrapped func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error)
}

func newRetryFixture(t *testing.T, cfg RetryBackoffConfig, base []provider.ModelPreference, script func(int, provider.ModelPreference) error) *retryFixture {
	t.Helper()
	hm := NewHealthManager()
	m := NewManager(nil, hm, 50*time.Millisecond)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetBasePreferences(base)
	f := &retryFixture{m: m, hm: hm, script: script}
	cfg.Sleep = func(ctx context.Context, d time.Duration) error {
		f.slept = append(f.slept, d)
		return ctx.Err()
	}
	h := NewStreamHook(m, nil, "r2-retry")
	h.SetRetryBackoff(cfg)
	f.hook = h
	f.wrapped = h.Execute(func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		call := len(f.calls)
		f.calls = append(f.calls, provider.ModelPreference{Provider: req.Provider, Model: req.Model})
		if f.script != nil {
			if err := f.script(call, provider.ModelPreference{Provider: req.Provider, Model: req.Model}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return f
}

// R2-02-a: an already-cancelled context makes ZERO downstream calls.
func TestR202AlreadyCancelledZeroCalls(t *testing.T) {
	f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: 4}, []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
	}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.wrapped(ctx, &provider.ChatRequest{}); err == nil {
		t.Fatal("cancelled context must error")
	}
	if len(f.calls) != 0 {
		t.Fatalf("cancelled context produced %d calls", len(f.calls))
	}
}

// R2-02-b: cancellation between attempts (inside the sleeper) stops
// immediately — no further dispatches after ctx dies mid-backoff.
func TestR202CancellationBetweenAttemptsStops(t *testing.T) {
	f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: 4}, []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
	}, func(int, provider.ModelPreference) error {
		return errors.New("connection refused") // transient
	})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel on first sleep.
	orig := f.hook.retry.Sleep
	f.hook.retry.Sleep = func(c context.Context, d time.Duration) error {
		cancel()
		return orig(c, d)
	}
	_, err := f.wrapped(ctx, &provider.ChatRequest{})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected exactly 1 attempt before cancellation, got %d", len(f.calls))
	}
}

// R2-02-c: permanent (critical: auth/billing/quota) errors stop the
// loop immediately — no retry, no crossing to sibling pairs.
func TestR202PermanentErrorStopsImmediately(t *testing.T) {
	f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: 4}, []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-4-flash"},
	}, func(int, provider.ModelPreference) error {
		return &provider.Error{ErrorType: provider.ErrorTypeAuthFailure, Message: "invalid api key"}
	})
	_, err := f.wrapped(context.Background(), &provider.ChatRequest{})
	if err == nil || !provider.IsCriticalStreamError(err) {
		t.Fatalf("expected permanent failure error, got %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("permanent error must stop after 1 attempt, got %d", len(f.calls))
	}
	if len(f.slept) != 0 {
		t.Fatalf("permanent error must not backoff, slept %v", f.slept)
	}
}

// R2-02-d: transient errors retry through the injected sleeper with
// exponential backoff, honouring the cumulative wait budget; original
// error classification preserved in the joined error.
func TestR202TransientRetryUsesSleeperAndBudget(t *testing.T) {
	f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: 3, MaxTotalWait: 250 * time.Millisecond}, []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
	}, func(call int, p provider.ModelPreference) error {
		if call < 2 {
			return errors.New("timeout: temporary failure") // transient keyword
		}
		return nil
	})
	if _, err := f.wrapped(context.Background(), &provider.ChatRequest{}); err != nil {
		t.Fatalf("transient failures should recover on round 3: %v", err)
	}
	if len(f.calls) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(f.calls))
	}
	// Backoffs: 100ms (round 2), then budget-capped 150ms (round 3).
	if len(f.slept) != 2 {
		t.Fatalf("expected 2 backoff sleeps, got %v", f.slept)
	}
	var total time.Duration
	for _, d := range f.slept {
		total += d
	}
	if total > 250*time.Millisecond {
		t.Fatalf("cumulative wait %v exceeded budget", total)
	}
	if f.slept[0] != 100*time.Millisecond {
		t.Fatalf("first backoff should be 100ms, got %v", f.slept[0])
	}
	if f.slept[1] != 150*time.Millisecond {
		t.Fatalf("second backoff should be budget-capped at 150ms, got %v", f.slept[1])
	}
}

// R2-02-e: provider Retry-After is honoured in preference to the
// exponential default.
func TestR202HonoursRetryAfter(t *testing.T) {
	f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: 2}, []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
	}, func(call int, p provider.ModelPreference) error {
		if call == 0 {
			return &provider.Error{
				ErrorType: provider.ErrorTypeRateLimit,
				Message:   "slow down",
				RateLimit: &provider.RateLimit{RetryAfter: 7 * time.Second},
			}
		}
		return nil
	})
	if _, err := f.wrapped(context.Background(), &provider.ChatRequest{}); err != nil {
		t.Fatalf("expected recovery: %v", err)
	}
	if len(f.slept) != 1 || f.slept[0] != 7*time.Second {
		t.Fatalf("Retry-After 7s must be honoured, slept %v", f.slept)
	}
}

// R2-02-f: MaxRounds semantics — zero-value config is a SINGLE pass
// (never 4 rounds), negative selects the default.
func TestR202MaxRoundsSemantics(t *testing.T) {
	for _, tc := range []struct {
		raw        int
		wantCalls  int
		wantRounds int
	}{
		{raw: 0, wantCalls: 1, wantRounds: 1},
		{raw: 1, wantCalls: 1, wantRounds: 1},
		{raw: 2, wantCalls: 2, wantRounds: 2},
		{raw: -1, wantCalls: 4, wantRounds: 4},
	} {
		f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: tc.raw}, []provider.ModelPreference{
			{Provider: "zai", Model: "glm-5.2"},
		}, func(int, provider.ModelPreference) error {
			return errors.New("connection refused")
		})
		_, err := f.wrapped(context.Background(), &provider.ChatRequest{})
		if err == nil {
			t.Fatalf("raw=%d: expected failure", tc.raw)
		}
		if len(f.calls) != tc.wantCalls {
			t.Fatalf("raw=%d: expected %d attempts, got %d", tc.raw, tc.wantCalls, len(f.calls))
		}
	}
	if got := (RetryBackoffConfig{}).withDefaults().MaxRounds; got != 1 {
		t.Fatalf("zero-value MaxRounds must normalise to 1 (single pass), got %d", got)
	}
	if got := (RetryBackoffConfig{MaxRounds: -5}).withDefaults().MaxRounds; got != 4 {
		t.Fatalf("negative MaxRounds must normalise to default 4, got %d", got)
	}
}

// R2-03-a: attempt-time recheck — during healthy A's failing callback,
// mark only B rate-limited while A stays healthy: the recorder receives
// ZERO B calls on the next round (B is re-validated at attempt time via
// the re-queried candidate pool).
func TestR203StaleCandidateNotRetriedAtAttemptTime(t *testing.T) {
	hm := NewHealthManager()
	m := NewManager(nil, hm, 0)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetBasePreferences([]provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-4-flash"},
	})
	var calls []provider.ModelPreference
	h := NewStreamHook(m, nil, "r2-03")
	h.SetRetryBackoff(RetryBackoffConfig{MaxRounds: 3, Sleep: func(ctx context.Context, d time.Duration) error { return nil }})
	wrapped := h.Execute(func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		calls = append(calls, provider.ModelPreference{Provider: req.Provider, Model: req.Model})
		// glm-5.2 always fails transiently; glm-4-flash succeeds.
		if req.Model == "glm-5.2" {
			// Mid-flight: remove the QUICK TIER from the pool after the
			// first failed head attempt. Next round must not dispatch it.
			if len(calls) == 1 {
				hm.MarkRateLimited("zai", "glm-4-flash", time.Now().Add(time.Hour))
			}
			return nil, errors.New("connection refused")
		}
		return nil, nil
	})
	_, err := wrapped(context.Background(), &provider.ChatRequest{})
	if err == nil {
		t.Fatal("expected explicit failure once the pool empties")
	}
	bCalls := 0
	for _, c := range calls {
		if c.Model == "glm-4-flash" {
			bCalls++
		}
	}
	if bCalls != 0 {
		t.Fatalf("rate-limited mid-flight candidate was dispatched: %+v", calls)
	}
	// All remaining attempts are the healthy head only, bounded by
	// MaxRounds=3 (refreshing the pool cannot reset the bound).
	for _, c := range calls {
		if c.Model != "glm-5.2" {
			t.Fatalf("unexpected dispatch: %+v", calls)
		}
	}
	if len(calls) > 3 {
		t.Fatalf("attempt bound violated: %+v", calls)
	}
}

// R2-03-b: policy changes between attempts are revalidated — installing
// a stricter allowlist mid-flight blocks the next round's dispatch.
func TestR203PolicyChangeRevalidatedBetweenAttempts(t *testing.T) {
	hm := NewHealthManager()
	m := NewManager(nil, hm, 0)
	allowAllZai := NewDeliveryAllowlist([2]string{"zai", "*"})
	m.SetDeliveryAllowlist(allowAllZai)
	m.SetBasePreferences([]provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
		{Provider: "zai", Model: "glm-scratchpad"},
	})
	var calls []provider.ModelPreference
	h := NewStreamHook(m, nil, "r2-03")
	h.SetRetryBackoff(RetryBackoffConfig{MaxRounds: 3, Sleep: func(ctx context.Context, d time.Duration) error { return nil }})
	wrapped := h.Execute(func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		calls = append(calls, provider.ModelPreference{Provider: req.Provider, Model: req.Model})
		if len(calls) == 1 {
			// Tighten policy mid-flight: glm-scratchpad is now forbidden.
			m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
		}
		return nil, errors.New("connection refused")
	})
	if _, err := wrapped(context.Background(), &provider.ChatRequest{}); err == nil {
		t.Fatal("expected failure after policy tightening")
	}
	for _, c := range calls {
		if c.Model == "glm-scratchpad" {
			t.Fatalf("post-tightening candidate dispatched: %+v", calls)
		}
	}
}

// R2-03-c: refreshing the candidate list cannot reset the finite
// attempt bound — with a persistently failing single candidate the
// total attempts equal MaxRounds, no spin.
func TestR203RefreshCannotResetAttemptBound(t *testing.T) {
	f := newRetryFixture(t, RetryBackoffConfig{MaxRounds: 3}, []provider.ModelPreference{
		{Provider: "zai", Model: "glm-5.2"},
	}, func(int, provider.ModelPreference) error {
		return errors.New("connection refused")
	})
	if _, err := f.wrapped(context.Background(), &provider.ChatRequest{}); err == nil {
		t.Fatal("expected bounded failure")
	}
	if len(f.calls) != 3 {
		t.Fatalf("attempt bound violated: %d calls for MaxRounds=3", len(f.calls))
	}
}

// R2-02-g: per-attempt timeout — the dispatch handler receives a
// context that expires after the manager's StreamTimeout.
func TestR202PerAttemptTimeoutApplied(t *testing.T) {
	hm := NewHealthManager()
	m := NewManager(nil, hm, 30*time.Millisecond)
	m.SetDeliveryAllowlist(DefaultDeliveryAllowlist())
	m.SetBasePreferences([]provider.ModelPreference{{Provider: "zai", Model: "glm-5.2"}})
	var gotDeadline bool
	h := NewStreamHook(m, nil, "r2-timeout")
	h.SetRetryBackoff(RetryBackoffConfig{MaxRounds: 1})
	wrapped := h.Execute(func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
		_, ok := ctx.Deadline()
		gotDeadline = ok
		return nil, errors.New("connection refused")
	})
	_, _ = wrapped(context.Background(), &provider.ChatRequest{})
	if !gotDeadline {
		t.Fatal("attempt context must carry the manager's per-attempt timeout deadline")
	}
}
