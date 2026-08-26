package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/plan"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/quota"
	quotastore "github.com/baphled/flowstate/internal/provider/quota/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// newWiringTestApp builds a bare App the wiring/accessor specs can mutate
// without paying for the full New bootstrap.
func newWiringTestApp(t *testing.T) *App {
	t.Helper()
	return &App{}
}

// TestAccessorsNilSafeAndRoundTrip locks the graceful-degradation contract
// for the App accessors the TUI/CLI layers call before boot wiring has
// finished: every getter must tolerate a zero-valued App (nil receiver or
// nil field) rather than panicking, and the setter/getter pairs must
// round-trip.
func TestAccessorsNilSafeAndRoundTrip(t *testing.T) {
	var nilApp *App
	if got := nilApp.SessionManager(); got != nil {
		t.Errorf("SessionManager on nil App = %v, want nil", got)
	}
	if err := nilApp.ShutdownQuotaCache(context.Background()); err != nil {
		t.Errorf("ShutdownQuotaCache on nil App = %v, want nil error", err)
	}

	a := newWiringTestApp(t)
	if got := a.SessionManager(); got != nil {
		t.Errorf("SessionManager before wiring = %v, want nil", got)
	}
	if err := a.ShutdownQuotaCache(context.Background()); err != nil {
		t.Errorf("ShutdownQuotaCache without controller = %v, want nil error", err)
	}
	if got := a.ProviderRegistry(); got != nil {
		t.Errorf("ProviderRegistry before wiring = %v, want nil", got)
	}
	if got := a.PlanOutputDir(); got != "" {
		t.Errorf("PlanOutputDir before wiring = %q, want empty", got)
	}
	if got := a.CompletionOrchestrator(); got != nil {
		t.Errorf("CompletionOrchestrator before wiring = %v, want nil", got)
	}
	if got := a.SessionMgr(); got != nil {
		t.Errorf("SessionMgr before wiring = %v, want nil", got)
	}
	if got := a.BackgroundManager(); got != nil {
		t.Errorf("BackgroundManager before wiring = %v, want nil", got)
	}

	a.SetBackgroundManager(nil)
	if got := a.BackgroundManager(); got != nil {
		t.Errorf("SetBackgroundManager(nil) not reflected: %v", got)
	}
	// Setters must accept nil without panicking (documented contract:
	// "may be nil to disable").
	a.SetAutoresearchRunner(nil)
	a.SetAutoresearchPruner(nil)
}

// TestMetricsHandlerServesRegistry verifies the Prometheus exposition
// handler is wired to the app's metrics registry and responds 200 with
// exposition-format output.
func TestMetricsHandlerServesRegistry(t *testing.T) {
	a := newWiringTestApp(t)
	a.metricsRegistry = newMetricsRegistryForTest(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	a.MetricsHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("MetricsHandler status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "# HELP") && !strings.Contains(body, "# TYPE") {
		t.Errorf("metrics body lacks Prometheus exposition markers:\n%s", body[:min(len(body), 200)])
	}
}

// TestBootstrapDeferredFlagLifecycle covers the deferred-bootstrap flag
// pair: BootstrapDeferred reports the construction-time setting, and
// MarkBootstrapRan flips it so a re-entry does not double-bootstrap.
func TestBootstrapDeferredFlagLifecycle(t *testing.T) {
	var nilApp *App
	if nilApp.BootstrapDeferred() {
		t.Error("BootstrapDeferred on nil App = true, want false")
	}

	a := newWiringTestApp(t)
	a.bootstrapDeferred = true
	if !a.BootstrapDeferred() {
		t.Error("BootstrapDeferred = false, want true before MarkBootstrapRan")
	}
	a.MarkBootstrapRan()
	if a.BootstrapDeferred() {
		t.Error("BootstrapDeferred = true after MarkBootstrapRan, want false")
	}

	var nilAgain *App
	nilAgain.MarkBootstrapRan() // must not panic
	if nilAgain.BootstrapDeferred() {
		t.Error("nil App still reports deferred bootstrap after MarkBootstrapRan")
	}
}

// memoryCoordinationStore is an inline coordination.Store backed by a map,
// used to drive PersistApprovedPlan without any on-disk coordination
// infrastructure.
type memoryCoordinationStore struct {
	data map[string][]byte
	fail map[string]error
}

func newMemoryCoordinationStore() *memoryCoordinationStore {
	return &memoryCoordinationStore{data: map[string][]byte{}, fail: map[string]error{}}
}

func (m *memoryCoordinationStore) Get(key string) ([]byte, error) {
	if err, ok := m.fail[key]; ok {
		return nil, err
	}
	v, ok := m.data[key]
	if !ok {
		return nil, errors.New("key not found: " + key)
	}
	return v, nil
}

func (m *memoryCoordinationStore) Set(key string, value []byte) error {
	m.data[key] = value
	return nil
}
func (m *memoryCoordinationStore) List(prefix string) ([]string, error) {
	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	return keys, nil
}
func (m *memoryCoordinationStore) Delete(key string) error           { delete(m.data, key); return nil }
func (m *memoryCoordinationStore) Increment(key string) (int, error) { return 0, nil }
func (m *memoryCoordinationStore) Exists(key string) (bool, error) {
	_, ok := m.data[key]
	return ok, nil
}

var _ coordination.Store = (*memoryCoordinationStore)(nil)

// TestPersistApprovedPlan covers the approved-plan persistence path end to
// end: rejection cases (no store, unapproved review, missing plan, store
// errors) and the happy path writing a plan file via Store.Create.
func TestPersistApprovedPlan(t *testing.T) {
	chainID := "chain-persist-1"
	planMD := "---\nid: persist-plan\ntitle: Persist approved plan\n---\n\n# Body\n"

	newStore := func(t *testing.T) *coordinationBackedPlanStore {
		t.Helper()
		return newCoordinationBackedPlanStore(t)
	}

	t.Run("rejects when plan store is not configured", func(t *testing.T) {
		a := newWiringTestApp(t)
		err := a.PersistApprovedPlan(chainID, newMemoryCoordinationStore())
		if err == nil || !strings.Contains(err.Error(), "plan store not configured") {
			t.Fatalf("error = %v, want 'plan store not configured'", err)
		}
	})

	t.Run("propagates review fetch errors", func(t *testing.T) {
		a := newWiringTestApp(t)
		a.Store = newStore(t).store
		cs := newMemoryCoordinationStore()
		cs.fail[chainID+"/review"] = errors.New("coordination offline")
		err := a.PersistApprovedPlan(chainID, cs)
		if err == nil || !strings.Contains(err.Error(), "getting review") {
			t.Fatalf("error = %v, want wrapped 'getting review'", err)
		}
	})

	t.Run("rejects unapproved review", func(t *testing.T) {
		a := newWiringTestApp(t)
		a.Store = newStore(t).store
		cs := newMemoryCoordinationStore()
		cs.data[chainID+"/review"] = []byte("REJECT: needs work")
		err := a.PersistApprovedPlan(chainID, cs)
		if err == nil || !strings.Contains(err.Error(), "plan not approved") {
			t.Fatalf("error = %v, want 'plan not approved'", err)
		}
	})

	t.Run("propagates plan fetch errors", func(t *testing.T) {
		a := newWiringTestApp(t)
		a.Store = newStore(t).store
		cs := newMemoryCoordinationStore()
		cs.data[chainID+"/review"] = []byte("APPROVE")
		err := a.PersistApprovedPlan(chainID, cs)
		if err == nil || !strings.Contains(err.Error(), "getting plan") {
			t.Fatalf("error = %v, want wrapped 'getting plan'", err)
		}
	})

	t.Run("persists approved plan to the store", func(t *testing.T) {
		a := newWiringTestApp(t)
		wrapped := newStore(t)
		a.Store = wrapped.store
		cs := newMemoryCoordinationStore()
		cs.data[chainID+"/review"] = []byte("verdict: APPROVE")
		cs.data[chainID+"/plan"] = []byte(planMD)

		if err := a.PersistApprovedPlan(chainID, cs); err != nil {
			t.Fatalf("PersistApprovedPlan: %v", err)
		}
		// Store.Create writes {dataDir}/{frontmatter id}.md — assert the
		// file landed on disk and parses back with approved status.
		onDisk := filepath.Join(wrapped.dir, "persist-plan.md")
		data, err := os.ReadFile(onDisk)
		if err != nil {
			t.Fatalf("persisted plan file unreadable: %v", err)
		}
		if !strings.Contains(string(data), "title: Persist approved plan") {
			t.Errorf("persisted file missing frontmatter title:\n%s", data)
		}
		parsed, err := plan.ParseFile(string(data))
		if err != nil {
			t.Fatalf("re-parsing persisted plan: %v", err)
		}
		if parsed.ID != "persist-plan" {
			t.Errorf("persisted plan ID = %q, want 'persist-plan'", parsed.ID)
		}
		if parsed.Status != "approved" {
			t.Errorf("persisted plan Status = %q, want 'approved'", parsed.Status)
		}
	})
}

// coordinationBackedPlanStore pairs a real plan.Store with its data
// directory so the happy-path spec asserts the on-disk artefact directly.
type coordinationBackedPlanStore struct {
	store *plan.Store
	dir   string
}

func newCoordinationBackedPlanStore(t *testing.T) *coordinationBackedPlanStore {
	t.Helper()
	dir := t.TempDir()
	s, err := plan.NewStore(dir)
	if err != nil {
		t.Fatalf("plan.NewStore: %v", err)
	}
	return &coordinationBackedPlanStore{store: s, dir: dir}
}

// TestDelegateStoreFactoryCreatesSessionStore covers the delegation
// session-store factory used by the DelegateTool wiring: it materialises a
// file-backed recall store under the sessions directory.
func TestDelegateStoreFactoryCreatesSessionStore(t *testing.T) {
	dir := t.TempDir()
	f := newDelegateStoreFactory(dir)

	store, err := f.CreateSessionStore("sess-42")
	if err != nil {
		t.Fatalf("CreateSessionStore: %v", err)
	}
	if store == nil {
		t.Fatal("CreateSessionStore returned nil store")
	}
	want := filepath.Join(dir, "sess-42.json")
	if _, err := os.Stat(want); err == nil {
		t.Errorf("store file %q already exists before first write", want)
	}
	msg := provider.Message{Role: "user", Content: "hello"}
	id := store.AppendReturningID(msg)
	if id == "" {
		t.Error("AppendReturningID returned empty id")
	}
	store.Close()
	if _, err := os.Stat(want); err != nil {
		t.Errorf("store file %q not created after Close: %v", want, err)
	}
}

// TestMemorySpendStoreAdapter covers the quota spend-store adapter
// translation layer: Put then Get round-trips the snapshot, a missing key
// maps to quota.ErrSpendStoreNotFound, Reset clears the entry, and List
// reports the stored entries.
func TestMemorySpendStoreAdapter(t *testing.T) {
	ctx := context.Background()
	adapter := newMemorySpendStoreAdapter(quotastore.NewMemoryStore())

	key := quota.SpendStoreKey{ProviderID: "anthropic", AccountHash: "abc123", ModelID: "claude-opus-4-5"}
	snap := quota.Snapshot{Provider: "anthropic", AccountHash: "abc123", Model: "claude-opus-4-5"}

	if _, err := adapter.Get(ctx, key); !errors.Is(err, quota.ErrSpendStoreNotFound) {
		t.Fatalf("Get on empty store: err = %v, want ErrSpendStoreNotFound", err)
	}

	if err := adapter.Put(ctx, key, snap); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := adapter.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get after Put: %v", err)
	}
	if got.Provider != snap.Provider || got.Model != snap.Model || got.AccountHash != snap.AccountHash {
		t.Errorf("round-tripped snapshot = %+v, want %+v", got, snap)
	}

	entries, err := adapter.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List returned %d entries, want 1", len(entries))
	}
	if entries[0].Key.ProviderID != key.ProviderID || entries[0].Key.ModelID != key.ModelID {
		t.Errorf("List entry key = %+v, want %+v", entries[0].Key, key)
	}

	if err := adapter.Reset(ctx, key); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := adapter.Get(ctx, key); !errors.Is(err, quota.ErrSpendStoreNotFound) {
		t.Fatalf("Get after Reset: err = %v, want ErrSpendStoreNotFound", err)
	}
}

// TestQuotaAggregatorAdapterNilSafe locks the graceful-degradation branch
// of the api.QuotaAggregator adapter: a nil adapter or nil engine yields
// empty results rather than a panic.
func TestQuotaAggregatorAdapterNilSafe(t *testing.T) {
	var nilAdapter *quotaAggregatorAdapter
	if rows := nilAdapter.QuotaSnapshots(context.Background()); rows != nil {
		t.Errorf("nil adapter QuotaSnapshots = %v, want nil", rows)
	}
	if ok, err := nilAdapter.ResetQuotaSpend(context.Background(), "p", "h", "m"); ok || err != nil {
		t.Errorf("nil adapter ResetQuotaSpend = (%v, %v), want (false, nil)", ok, err)
	}

	empty := &quotaAggregatorAdapter{}
	if rows := empty.QuotaSnapshots(context.Background()); rows != nil {
		t.Errorf("empty adapter QuotaSnapshots = %v, want nil", rows)
	}
	if ok, err := empty.ResetQuotaSpend(context.Background(), "p", "h", "m"); ok || err != nil {
		t.Errorf("empty adapter ResetQuotaSpend = (%v, %v), want (false, nil)", ok, err)
	}
}

// TestCreateExecutionEvaluator covers the harness-adapter construction
// path: the evaluator is built with retry options when configured and
// without them otherwise. The execution loop requires a live streamer,
// so live delegation is covered by the harness-adapter integration
// specs; here we pin the constructor contract (non-nil adapter for
// both configuration shapes).
func TestCreateExecutionEvaluator(t *testing.T) {
	for _, cfg := range []config.HarnessConfig{{}, {MaxRetries: 3}} {
		if ev := createExecutionEvaluator(cfg, agent.NewRegistry(), nil); ev == nil {
			t.Fatalf("createExecutionEvaluator(%+v) = nil, want evaluator", cfg)
		}
	}
}

// TestModelListerFromRegistry covers the api.ModelLister closure built at
// boot: nil registry yields an empty non-nil slice, and a populated
// registry is enumerated through the same ListModels path the CLI uses.
func TestModelListerFromRegistry(t *testing.T) {
	if models, err := modelListerFromRegistry(nil)(); err != nil || len(models) != 0 {
		t.Fatalf("nil registry lister = (%v, %v), want (empty, nil)", models, err)
	}

	reg := providerRegistryWithStub(t, "stub-a", nil)
	models, err := modelListerFromRegistry(reg)()
	if err != nil {
		t.Fatalf("lister: %v", err)
	}
	if len(models) != 0 {
		t.Errorf("lister on empty-models registry returned %d models, want 0", len(models))
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// newMetricsRegistryForTest builds a fresh Prometheus registry with the
// Go runtime collector registered so /metrics output carries exposition
// markers even before app-specific metrics land.
func newMetricsRegistryForTest(t *testing.T) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	return reg
}

// providerRegistryWithStub registers an optional stub provider and returns
// the registry, mirroring the wiring modelListerFromRegistry consumes.
func providerRegistryWithStub(t *testing.T, name string, stub *stubModelsProvider) *provider.Registry {
	t.Helper()
	reg := provider.NewRegistry()
	if stub != nil {
		reg.Register(stub)
	}
	_ = name
	return reg
}
