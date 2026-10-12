//go:build e2e

package support

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/shared"
	"github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

// vaultSyncPerfState holds scenario-scoped state for the sync-perf steps.
type vaultSyncPerfState struct {
	vaultRoot string

	embedder *instrumentedEmbedder
	store    *flushableStore
	state    *vaultindex.State

	summary vaultindex.Summary
	err     error

	peakWorkers     int
	stalledProvider *ollama.Provider
	stallElapsed    time.Duration
}

// RegisterVaultSyncPerfSteps wires the vault sync performance BDD steps.
//
// Expected: parameters for RegisterVaultSyncPerfSteps.
// Side effects: None.
func RegisterVaultSyncPerfSteps(ctx *godog.ScenarioContext) {
	s := &vaultSyncPerfState{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &vaultSyncPerfState{}
		return nil, nil
	})

	ctx.Step(`^a sync perf vault directory with markdown files$`, s.tempVault)
	ctx.Step(`^I run a vault sync with at most (\d+) concurrent file workers over (\d+) changed files$`, s.runBoundedSync)
	ctx.Step(`^the sync should complete successfully$`, s.syncCompleted)
	ctx.Step(`^the observed peak concurrent file workers should not exceed (\d+)$`, s.peakWithinLimit)
	ctx.Step(`^I run a vault sync with a batch-capable embedder and (\d+) chunks at batch size (\d+)$`, s.runBatchedSync)
	ctx.Step(`^the embedder should have received (\d+) batch requests$`, s.batchRequests)
	ctx.Step(`^the embedder should have received (\d+) single-chunk requests$`, s.singleRequests)
	ctx.Step(`^I run a vault sync with a batch-rejecting embedder$`, s.runBatchRejectingSync)
	ctx.Step(`^the embedder should have received only single-chunk requests$`, s.onlySingleRequests)
	ctx.Step(`^I run a vault sync over (\d+) changed files with checkpoint interval (\d+)$`, s.runCheckpointSync)
	ctx.Step(`^the sidecar should have been saved exactly once$`, s.savedOnce)
	ctx.Step(`^I run a vault sync over (\d+) changed files with a flush-capable store$`, s.runFlushSync)
	ctx.Step(`^every per-file upsert should have been issued with wait=false$`, s.allUpsertsDeferred)
	ctx.Step(`^the store should have flushed exactly once after the last upsert$`, s.flushedOnce)
	ctx.Step(`^I run a vault sync against an embedder that accepts connections but never responds$`, s.runStalledEmbedderSync)
	ctx.Step(`^the sync should abort within a bounded timeout$`, s.abortedWithinTimeout)
}

// tempVault creates a scratch vault directory (Background step reuse).
func (s *vaultSyncPerfState) tempVault() error {
	dir, err := os.MkdirTemp("", "flowstate-sync-perf-*")
	if err != nil {
		return err
	}
	s.vaultRoot = dir
	return nil
}

// writeNFiles seeds n markdown files with enough words to produce at
// least one chunk each.
func (s *vaultSyncPerfState) writeNFiles(n int) error {
	for i := 0; i < n; i++ {
		body := fmt.Sprintf("# Note %d\n\n%s\n", i, repeatWords("word", 30))
		if err := os.WriteFile(filepath.Join(s.vaultRoot, fmt.Sprintf("note-%02d.md", i)), []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func repeatWords(word string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		if i > 0 {
			out += " "
		}
		out += word
	}
	return out
}

// runIndexer runs a single IndexAll pass with the scenario's embedder and
// store, recording the summary/error on the state.
func (s *vaultSyncPerfState) runIndexer(workers, batch, interval int) {
	state, err := vaultindex.LoadState(vaultindex.SidecarPath(s.vaultRoot))
	if err != nil {
		s.err = err
		return
	}
	s.state = state
	idx := vaultindex.NewIndexer(vaultindex.IndexerConfig{
		VaultRoot:         s.vaultRoot,
		Collection:        "sync-perf",
		BatchSize:         batch,
		EmbeddingDim:      3,
		Workers:           workers,
		StateSaveInterval: interval,
		Chunker:           vaultindex.NewChunker(vaultindex.DefaultChunkSize, vaultindex.DefaultChunkOverlap),
		Embedder:          s.embedder,
		Store:             s.store,
		State:             state,
	})
	ctx := context.Background()
	if err := idx.EnsureCollection(ctx); err != nil {
		s.err = err
		return
	}
	s.summary, s.err = idx.IndexAll(ctx)
}

func (s *vaultSyncPerfState) runBoundedSync(limit, files int) error {
	if err := s.writeNFiles(files); err != nil {
		return err
	}
	s.embedder = newInstrumentedEmbedder(true)
	s.embedder.delay = 5 * time.Millisecond
	s.embedder.trackWorkerPeak = true
	s.store = newFlushableStore()
	s.runIndexer(limit, vaultindex.DefaultBatchSize, vaultindex.DefaultStateSaveInterval)
	return nil
}

func (s *vaultSyncPerfState) syncCompleted() error {
	if s.err != nil {
		return fmt.Errorf("sync failed: %w", s.err)
	}
	if s.summary.Indexed == 0 {
		return errors.New("sync indexed nothing")
	}
	return nil
}

func (s *vaultSyncPerfState) peakWithinLimit(limit int) error {
	if peak := s.embedder.peakWorkers.Load(); peak > int64(limit) {
		return fmt.Errorf("peak concurrency %d exceeded limit %d", peak, limit)
	}
	return nil
}

func (s *vaultSyncPerfState) runBatchedSync(chunks, batch int) error {
	// One file chunked into exactly `chunks` windows. The chunker strides
	// by size-overlap (512-50=462) after the first window, so a body of
	// 512 + (chunks-1)*462 words yields exactly `chunks` chunks.
	// One file chunked into exactly `chunks` windows. With stride
	// size-overlap (462), a body of size + (chunks-2)*stride + 1 words
	// (plus heading tokens) yields exactly `chunks` chunks.
	stride := vaultindex.DefaultChunkSize - vaultindex.DefaultChunkOverlap
	words := vaultindex.DefaultChunkSize + (chunks-2)*stride + 1
	body := "# Batched\n\n" + repeatWords("alpha", words) + "\n"
	if err := os.WriteFile(filepath.Join(s.vaultRoot, "batched.md"), []byte(body), 0o644); err != nil {
		return err
	}
	s.embedder = newInstrumentedEmbedder(true)
	s.store = newFlushableStore()
	s.runIndexer(1, batch, vaultindex.DefaultStateSaveInterval)
	if s.summary.Chunks != chunks {
		return fmt.Errorf("expected %d chunks, got %d", chunks, s.summary.Chunks)
	}
	return nil
}

func (s *vaultSyncPerfState) batchRequests(want int) error {
	if got := s.embedder.batchCalls.Load(); got != int64(want) {
		return fmt.Errorf("expected %d batch requests, got %d", want, got)
	}
	return nil
}

func (s *vaultSyncPerfState) singleRequests(want int) error {
	if got := s.embedder.singleCalls.Load(); got != int64(want) {
		return fmt.Errorf("expected %d single-chunk requests, got %d", want, got)
	}
	return nil
}

func (s *vaultSyncPerfState) runBatchRejectingSync() error {
	if err := s.writeNFiles(3); err != nil {
		return err
	}
	s.embedder = newInstrumentedEmbedder(false)
	s.store = newFlushableStore()
	s.runIndexer(1, 2, vaultindex.DefaultStateSaveInterval)
	return nil
}

func (s *vaultSyncPerfState) onlySingleRequests() error {
	if got := s.embedder.batchSuccessful.Load(); got != 0 {
		return fmt.Errorf("expected 0 successful batches, got %d", got)
	}
	if s.embedder.singleCalls.Load() == 0 {
		return errors.New("expected fallback single embeds, got none")
	}
	return nil
}

func (s *vaultSyncPerfState) runCheckpointSync(files, interval int) error {
	if err := s.writeNFiles(files); err != nil {
		return err
	}
	s.embedder = newInstrumentedEmbedder(true)
	s.store = newFlushableStore()
	s.runIndexer(2, vaultindex.DefaultBatchSize, interval)
	if s.summary.Indexed != files {
		return fmt.Errorf("expected %d indexed, got %d", files, s.summary.Indexed)
	}
	return nil
}

func (s *vaultSyncPerfState) savedOnce() error {
	if got := s.state.SaveCount(); got != 1 {
		return fmt.Errorf("expected exactly 1 sidecar save, got %d", got)
	}
	return nil
}

func (s *vaultSyncPerfState) runFlushSync(files int) error {
	if err := s.writeNFiles(files); err != nil {
		return err
	}
	s.embedder = newInstrumentedEmbedder(true)
	s.store = newFlushableStore()
	s.runIndexer(2, vaultindex.DefaultBatchSize, vaultindex.DefaultStateSaveInterval)
	if s.summary.Indexed != files {
		return fmt.Errorf("expected %d indexed, got %d", files, s.summary.Indexed)
	}
	return nil
}

func (s *vaultSyncPerfState) allUpsertsDeferred() error {
	if s.store.waitTrueUpserts != 0 {
		return fmt.Errorf("%d upserts were issued with wait=true", s.store.waitTrueUpserts)
	}
	if s.store.deferredUpserts != s.summary.Indexed {
		return fmt.Errorf("expected %d deferred upserts, got %d", s.summary.Indexed, s.store.deferredUpserts)
	}
	return nil
}

func (s *vaultSyncPerfState) flushedOnce() error {
	if s.store.flushes != 1 {
		return fmt.Errorf("expected exactly 1 flush, got %d", s.store.flushes)
	}
	return nil
}

// runStalledEmbedderSync starts a TCP server that accepts connections but
// never writes a response, wires the REAL ollama provider over it with the
// StreamGuard client (as the vault tool does), and runs an embed call.
func (s *vaultSyncPerfState) runStalledEmbedderSync() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	stalled := httptest.NewUnstartedServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		// Accept and hold the connection without responding. A fixed sleep
		// (rather than waiting on the request context) guarantees the
		// handler terminates so the deferred server Close can't deadlock.
		time.Sleep(10 * time.Second)
	}))
	stalled.Listener = ln
	stalled.Start()
	defer stalled.Close()

	prov, err := ollama.NewWithClient("http://"+ln.Addr().String(), shared.StreamGuardHTTPClient(500*time.Millisecond))
	if err != nil {
		return err
	}
	s.stalledProvider = prov

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, s.err = prov.Embed(ctx, provider.EmbedRequest{Input: "stall probe", Model: "nomic-embed-text"})
	s.stallElapsed = time.Since(start)
	return nil
}

func (s *vaultSyncPerfState) abortedWithinTimeout() error {
	if s.err == nil {
		return errors.New("expected embed against stalled server to fail")
	}
	if s.stallElapsed > 5*time.Second {
		return fmt.Errorf("embed took %s to fail; timeout not bounded", s.stallElapsed)
	}
	return nil
}

// sharedEmbedRequest builds a minimal embed request for the stall probe.
func sharedEmbedRequest() (req struct {
	Input string
	Model string
}) {
	req.Input = "stall probe"
	req.Model = "nomic-embed-text"
	return req
}

// instrumentedEmbedder counts batch vs single embed calls and optionally
// tracks the peak number of concurrently-running embed operations.
type instrumentedEmbedder struct {
	batchSuccessful atomic.Int64
	batchCalls      atomic.Int64
	singleCalls     atomic.Int64
	inFlight        atomic.Int64
	peakWorkers     atomic.Int64
	delay           time.Duration
	trackWorkerPeak bool
	allowBatch      bool
}

func newInstrumentedEmbedder(allowBatch bool) *instrumentedEmbedder {
	return &instrumentedEmbedder{allowBatch: allowBatch}
}

func (e *instrumentedEmbedder) Embed(ctx context.Context, _ string) ([]float64, error) {
	e.enter()
	defer e.leave()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(e.delay):
	}
	e.singleCalls.Add(1)
	return []float64{0.1, 0.2, 0.3}, nil
}

func (e *instrumentedEmbedder) EmbedBatch(ctx context.Context, input []string) ([][]float64, error) {
	e.batchCalls.Add(1)
	if !e.allowBatch {
		return nil, errors.New("batching rejected by server")
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(e.delay):
	}
	e.batchSuccessful.Add(1)
	out := make([][]float64, len(input))
	for i := range input {
		out[i] = []float64{0.1, 0.2, 0.3}
	}
	return out, nil
}

func (e *instrumentedEmbedder) enter() {
	n := e.inFlight.Add(1)
	if e.trackWorkerPeak {
		for {
			peak := e.peakWorkers.Load()
			if n <= peak || e.peakWorkers.CompareAndSwap(peak, n) {
				break
			}
		}
	}
}

func (e *instrumentedEmbedder) leave() {
	e.inFlight.Add(-1)
}

// flushableStore is an in-memory VectorStore implementing the Flusher
// extension, recording wait flags and flush count.
type flushableStore struct {
	mu              sync.Mutex
	exists          bool
	deferredUpserts int
	waitTrueUpserts int
	flushes         int
}

func newFlushableStore() *flushableStore {
	return &flushableStore{}
}

func (f *flushableStore) CollectionExists(_ context.Context, _ string) (bool, error) {
	return f.exists, nil
}

func (f *flushableStore) CreateCollection(_ context.Context, _ string, _ qdrant.CollectionConfig) error {
	f.exists = true
	return nil
}

func (f *flushableStore) Upsert(_ context.Context, _ string, _ []qdrant.Point, wait bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if wait {
		f.waitTrueUpserts++
	} else {
		f.deferredUpserts++
	}
	return nil
}

func (f *flushableStore) Flush(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.flushes++
	return nil
}
