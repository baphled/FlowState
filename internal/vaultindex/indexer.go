package vaultindex

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	"github.com/baphled/flowstate/internal/recall/qdrant"
)

// pointNamespace is a deterministic UUID-v5 namespace for vault chunk IDs.
//
// Qdrant only accepts unsigned integers or RFC 4122 UUIDs as point IDs;
// derive each chunk ID via uuid.NewSHA1 from the (file, chunk_index) pair
// so re-indexes overwrite the same point rather than accumulating
// duplicates.
var pointNamespace = uuid.MustParse("a1b2c3d4-e5f6-4789-a012-3456789abcde")

// DefaultBatchSize is the number of chunks embedded per Ollama batch.
const DefaultBatchSize = 32

// DefaultStateSaveInterval is the number of indexed files between state
// sidecar checkpoints. Saving after every file serialised the entire
// sidecar once per file — an O(N²) write pattern across a sync of N
// files. The sidecar is additionally saved once at the end of the pass as
// well, so an interrupted sync loses at most one interval of progress.
const DefaultStateSaveInterval = 50

// DefaultWorkers is the number of files processed concurrently during a
// sync pass. The default is deliberately conservative: embedding is CPU
// heavy on a local Ollama instance and the reported failure mode was the
// user's machine becoming unusable during sync. Override via the tool
// config (VAULT_SYNC_WORKERS env in the vault tools wiring).
const DefaultWorkers = 2

// DefaultEmbeddingDim is the vector dimensionality emitted by nomic-embed-text.
const DefaultEmbeddingDim = 768

// DefaultDistance is the Qdrant distance metric used for vault collections.
const DefaultDistance = "Cosine"

// Embedder produces an embedding vector for a single piece of text.
type Embedder interface {
	// Embed returns the embedding vector for text.
	Embed(ctx context.Context, text string) ([]float64, error)
}

// BatchEmbedder is an optional Embedder extension that embeds a batch of
// texts in a single request (Ollama /api/embed with a []string input).
// When the configured Embedder implements it the indexer sends one
// request per BatchSize window; otherwise it falls back to per-chunk
// Embed calls so any existing Embedder keeps working.
type BatchEmbedder interface {
	// EmbedBatch returns one embedding vector per input text, in order.
	// Implementations may return fewer vectors than inputs ONLY paired
	// with a non-nil error; partial success is signalled by error and the
	// caller falls back to single embeds for that batch.
	EmbedBatch(ctx context.Context, input []string) ([][]float64, error)
}

// EmbedderFunc adapts a plain function to Embedder.
type EmbedderFunc func(ctx context.Context, text string) ([]float64, error)

// VectorStore is the subset of qdrant.VectorStore the indexer requires.
type VectorStore interface {
	CollectionExists(ctx context.Context, name string) (bool, error)
	CreateCollection(ctx context.Context, name string, config qdrant.CollectionConfig) error
	Upsert(ctx context.Context, collection string, points []qdrant.Point, wait bool) error
}

// Flusher is an optional VectorStore extension that can force Qdrant to
// persist previously submitted wait=false upserts. When the store
// implements it, the indexer upserts with wait=false per file (letting
// Qdrant batch writes internally) and issues one Flush at the end of the
// pass so the sync only returns once points are durable. Stores without
// Flush fall back to wait=true upserts, preserving the old durability
// semantics for test fakes and alternative backends.
type Flusher interface {
	Flush(ctx context.Context, collection string) error
}

// Logger is a minimal slog-shaped sink used by the indexer for progress
// reporting. The vault server uses log.Default; tests pass a no-op logger.
type Logger interface {
	// Printf logs a formatted progress line.
	Printf(format string, args ...any)
}

// IndexerConfig groups the options accepted by NewIndexer.
type IndexerConfig struct {
	VaultRoot         string
	Collection        string
	BatchSize         int
	EmbeddingDim      int
	Reindex           bool
	Workers           int
	StateSaveInterval int
	Chunker           *Chunker
	Embedder          Embedder
	Store             VectorStore
	State             *State
	Logger            Logger
}

// Indexer walks a vault, chunks markdown, embeds chunks, and upserts the
// resulting points into Qdrant while tracking incremental state on disk.
type Indexer struct {
	cfg          IndexerConfig
	flushCapable bool
}

// NewIndexer constructs an Indexer from cfg.
//
// Expected:
//   - cfg.VaultRoot, cfg.Collection, cfg.Chunker, cfg.Embedder, cfg.Store,
//     and cfg.State are populated.
//   - cfg.BatchSize and cfg.EmbeddingDim default to DefaultBatchSize /
//     DefaultEmbeddingDim when zero or negative.
//
// Returns:
//   - A configured *Indexer.
//
// Side effects:
//   - None.
func NewIndexer(cfg IndexerConfig) *Indexer {
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = DefaultBatchSize
	}
	if cfg.EmbeddingDim <= 0 {
		cfg.EmbeddingDim = DefaultEmbeddingDim
	}
	if cfg.Workers <= 0 {
		cfg.Workers = DefaultWorkers
	}
	if cfg.StateSaveInterval <= 0 {
		cfg.StateSaveInterval = DefaultStateSaveInterval
	}
	if cfg.Logger == nil {
		cfg.Logger = noopLogger{}
	}
	_, flushCapable := cfg.Store.(Flusher)
	i := &Indexer{cfg: cfg, flushCapable: flushCapable}
	return i
}

// flushCapable records whether the configured store implements Flusher,
// deciding between deferred (wait=false + final Flush) and synchronous
// (wait=true) upserts.

// EnsureCollection creates the Qdrant collection if it does not already exist.
//
// Expected:
//   - The Qdrant server at the configured URL is reachable.
//
// Returns:
//   - nil on success.
//   - A wrapped error when the existence check or the create call fails.
//
// Side effects:
//   - Issues HTTP requests to Qdrant.
func (i *Indexer) EnsureCollection(ctx context.Context) error {
	exists, err := i.cfg.Store.CollectionExists(ctx, i.cfg.Collection)
	if err != nil {
		return fmt.Errorf("checking collection %s: %w", i.cfg.Collection, err)
	}
	if exists {
		return nil
	}
	cfg := qdrant.CollectionConfig{
		VectorSize: i.cfg.EmbeddingDim,
		Distance:   DefaultDistance,
	}
	if err := i.cfg.Store.CreateCollection(ctx, i.cfg.Collection, cfg); err != nil {
		return fmt.Errorf("creating collection %s: %w", i.cfg.Collection, err)
	}
	return nil
}

// IndexAll walks the vault and indexes every file whose mtime has advanced
// past the sidecar's record (or every file when Reindex is true).
//
// Expected:
//   - EnsureCollection has been called previously, or the collection exists.
//
// Returns:
//   - A summary with counts of files indexed, files skipped, and chunks
//     upserted.
//   - An error from any step in the pipeline.
//
// Side effects:
//   - Reads every markdown file under VaultRoot.
//   - Sends embed requests and upserts to the configured services.
//   - Writes the sidecar once per StateSaveInterval files and once at the
//     end of the pass (previously after EVERY file — O(N²) sidecar writes).
func (i *Indexer) IndexAll(ctx context.Context) (Summary, error) {
	files, err := WalkVault(i.cfg.VaultRoot)
	if err != nil {
		return Summary{}, err
	}

	// Partition first (cheap mtime checks), then fan only the dirty files
	// out over the bounded worker pool.
	var pending []MarkdownFile
	summary := Summary{Total: len(files)}
	for _, f := range files {
		if i.cfg.Reindex || i.cfg.State.NeedsReindex(f.RelPath, f.Mtime) {
			pending = append(pending, f)
		} else {
			summary.Skipped++
		}
	}

	firstErr := i.indexPending(ctx, pending, &summary)
	// Persist whatever completed before the failure/finish.
	if err := i.cfg.State.Save(); err != nil {
		return summary, err
	}
	if firstErr != nil {
		return summary, firstErr
	}

	// Durability barrier: all per-file upserts were issued with wait=false
	// (when the store supports Flush), so block once here until Qdrant has
	// persisted the whole pass — the same end-state durability as the old
	// per-file wait=true, without N synchronous flushes.
	if i.flushCapable {
		if err := i.cfg.Store.(Flusher).Flush(ctx, i.cfg.Collection); err != nil {
			return summary, fmt.Errorf("flushing collection %s: %w", i.cfg.Collection, err)
		}
	}
	return summary, nil
}

// indexPending runs the bounded worker pool over the dirty files,
// mutating summary (Indexed/Skipped/Chunks) under a mutex and returning
// the first error. State checkpoints are written every
// StateSaveInterval files; the caller performs the final Save.
//
// Expected: parameters for indexPending.
// Returns: result of indexPending.
// Side effects: embed/upsert requests; periodic sidecar checkpoints.
func (i *Indexer) indexPending(ctx context.Context, pending []MarkdownFile, summary *Summary) error {
	var (
		mu        sync.Mutex
		sinceSave int
		firstErr  error
	)
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(i.cfg.Workers)
	for _, f := range pending {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			chunkCount, err := i.indexFile(ctx, f)
			mu.Lock()
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("indexing %s: %w", f.RelPath, err)
				}
			} else {
				i.cfg.State.Update(f.RelPath, f.Mtime, chunkCount)
				summary.Indexed++
				summary.Chunks += chunkCount
				sinceSave++
				i.cfg.Logger.Printf("indexed %s (%d chunks)", f.RelPath, chunkCount)
				if sinceSave >= i.cfg.StateSaveInterval {
					sinceSave = 0
					if err := i.cfg.State.Save(); err != nil {
						// Checkpoint failure is non-fatal here; the final
						// Save below surfaces the same error if it persists.
						i.cfg.Logger.Printf("checkpoint save failed: %v", err)
					}
				}
			}
			mu.Unlock()
			return err
		})
	}
	// Wait for the pool to drain; the first error cancels ctx for the rest.
	_ = g.Wait()
	return firstErr
}

// indexFile reads a single file, chunks it, embeds it in batches, and
// upserts the resulting points.
//
// Expected: parameters for indexFile.
// Returns: result of indexFile.
// Side effects: None.
func (i *Indexer) indexFile(ctx context.Context, f MarkdownFile) (int, error) {
	body, err := os.ReadFile(f.AbsPath)
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", f.AbsPath, err)
	}
	chunks := i.cfg.Chunker.Chunk(string(body))
	if len(chunks) == 0 {
		return 0, nil
	}
	points, err := i.embedBatches(ctx, f, chunks)
	if err != nil {
		return 0, err
	}
	if len(points) == 0 {
		i.cfg.Logger.Printf("skipping upsert for %s: all chunks failed to embed", f.RelPath)
		return 0, nil
	}
	// wait=false when the store supports Flush: Qdrant batches the write
	// internally and the end-of-pass Flush guarantees durability. Stores
	// without Flush keep wait=true so points are durable per upsert.
	wait := !i.flushCapable
	if err := i.cfg.Store.Upsert(ctx, i.cfg.Collection, points, wait); err != nil {
		return 0, fmt.Errorf("upserting %s: %w", f.RelPath, err)
	}
	return len(points), nil
}

// embedBatches chunks the embedding work into BatchSize windows and
// produces the corresponding qdrant.Point slice.
//
// Expected: parameters for embedBatches.
// Returns: result of embedBatches.
// Side effects: None.
func (i *Indexer) embedBatches(ctx context.Context, f MarkdownFile, chunks []string) ([]qdrant.Point, error) {
	points := make([]qdrant.Point, 0, len(chunks))
	batcher, canBatch := i.cfg.Embedder.(BatchEmbedder)
	for start := 0; start < len(chunks); start += i.cfg.BatchSize {
		end := start + i.cfg.BatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		window := chunks[start:end]

		vectors := i.embedWindow(ctx, batcher, canBatch, f, window, start)
		for offset, vec := range vectors {
			if vec == nil {
				continue
			}
			idx := start + offset
			points = append(points, qdrant.Point{
				ID:     pointID(f.RelPath, idx),
				Vector: vec,
				Payload: map[string]any{
					"content":     chunks[idx],
					"source_file": f.RelPath,
					"chunk_index": idx,
					"mtime":       f.Mtime.Unix(),
				},
			})
		}
	}
	return points, nil
}

// embedWindow embeds one BatchSize window of chunks, returning one vector
// (or nil, on per-chunk failure) per input chunk.
//
// When the embedder implements BatchEmbedder the window goes out as a
// single request; on batch failure the window falls back to per-chunk
// Embed calls so a server that rejects multi-input /embed requests only
// costs the old behaviour, not the sync. Embedders without EmbedBatch
// use the per-chunk path in that case.
//
// Expected: parameters for embedWindow.
// Returns: result of embedWindow.
// Side effects: None.
func (i *Indexer) embedWindow(ctx context.Context, batcher BatchEmbedder, canBatch bool, f MarkdownFile, window []string, windowStart int) [][]float64 {
	if canBatch {
		vecs, err := batcher.EmbedBatch(ctx, window)
		if err == nil && len(vecs) == len(window) {
			return vecs
		}
		if err != nil {
			i.cfg.Logger.Printf("batch embed failed for %s (falling back to per-chunk): %v", f.RelPath, err)
		} else {
			i.cfg.Logger.Printf("batch embed for %s returned %d/%d vectors (falling back to per-chunk)", f.RelPath, len(vecs), len(window))
		}
	}
	vecs := make([][]float64, len(window))
	for offset := range window {
		vec, err := i.cfg.Embedder.Embed(ctx, window[offset])
		if err != nil {
			i.cfg.Logger.Printf("skipping %s chunk %d: %v", f.RelPath, windowStart+offset, err)
			continue
		}
		vecs[offset] = vec
	}
	return vecs
}

// pointID returns a deterministic UUID-v5 identifier for a (file, chunk) pair.
//
// Qdrant accepts either an unsigned integer or an RFC 4122 UUID as a point
// ID. The UUID-v5 derivation gives every (file, chunk_index) tuple a stable
// identifier, so re-indexes overwrite the existing row rather than
// duplicating it. The namespace is fixed in pointNamespace.
//
// Expected: parameters for pointID.
// Returns: result of pointID.
// Side effects: None.
func pointID(relPath string, chunkIndex int) string {
	return uuid.NewSHA1(pointNamespace, fmt.Appendf(nil, "%s:%d", relPath, chunkIndex)).String()
}

// Summary describes the outcome of an IndexAll pass.
type Summary struct {
	Total   int
	Indexed int
	Skipped int
	Chunks  int
}

// noopLogger discards every progress line.
type noopLogger struct{}

// Printf implements Logger by doing nothing.
//
// Expected: parameters for Printf.
// Returns: result of Printf.
// Side effects: None.
func (noopLogger) Printf(_ string, _ ...any) {}
