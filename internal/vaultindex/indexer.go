package vaultindex

import (
	"context"
	"fmt"
	"os"

	"github.com/google/uuid"

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

// DefaultEmbeddingDim is the vector dimensionality emitted by nomic-embed-text.
const DefaultEmbeddingDim = 768

// DefaultDistance is the Qdrant distance metric used for vault collections.
const DefaultDistance = "Cosine"

// Embedder produces an embedding vector for a single piece of text.
type Embedder interface {
	// Embed returns the embedding vector for text.
	Embed(ctx context.Context, text string) ([]float64, error)
}

// EmbedderFunc adapts a plain function to Embedder.
type EmbedderFunc func(ctx context.Context, text string) ([]float64, error)

// Embed implements Embedder by invoking the wrapped function.
func (f EmbedderFunc) Embed(ctx context.Context, text string) ([]float64, error) {
	return f(ctx, text)
}

// VectorStore is the subset of qdrant.VectorStore the indexer requires.
type VectorStore interface {
	CollectionExists(ctx context.Context, name string) (bool, error)
	CreateCollection(ctx context.Context, name string, config qdrant.CollectionConfig) error
	Upsert(ctx context.Context, collection string, points []qdrant.Point, wait bool) error
}

// Logger is a minimal slog-shaped sink used by the indexer for progress
// reporting. The vault server uses log.Default; tests pass a no-op logger.
type Logger interface {
	// Printf logs a formatted progress line.
	Printf(format string, args ...any)
}

// IndexerConfig groups the options accepted by NewIndexer.
type IndexerConfig struct {
	VaultRoot      string
	Collection     string
	BatchSize      int
	EmbeddingDim   int
	Reindex        bool
	Chunker        *Chunker
	Embedder       Embedder
	Store          VectorStore
	State          *State
	Logger         Logger
}

// Indexer walks a vault, chunks markdown, embeds chunks, and upserts the
// resulting points into Qdrant while tracking incremental state on disk.
type Indexer struct {
	cfg IndexerConfig
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
	if cfg.Logger == nil {
		cfg.Logger = noopLogger{}
	}
	return &Indexer{cfg: cfg}
}

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
//   - Writes the sidecar after every successful file pass.
func (i *Indexer) IndexAll(ctx context.Context) (Summary, error) {
	files, err := WalkVault(i.cfg.VaultRoot)
	if err != nil {
		return Summary{}, err
	}

	summary := Summary{Total: len(files)}
	for _, f := range files {
		needs := i.cfg.Reindex || i.cfg.State.NeedsReindex(f.RelPath, f.Mtime)
		if !needs {
			summary.Skipped++
			continue
		}
		chunkCount, err := i.indexFile(ctx, f)
		if err != nil {
			return summary, fmt.Errorf("indexing %s: %w", f.RelPath, err)
		}
		i.cfg.State.Update(f.RelPath, f.Mtime, chunkCount)
		if err := i.cfg.State.Save(); err != nil {
			return summary, err
		}
		summary.Indexed++
		summary.Chunks += chunkCount
		i.cfg.Logger.Printf("indexed %s (%d chunks)", f.RelPath, chunkCount)
	}
	return summary, nil
}

// indexFile reads a single file, chunks it, embeds it in batches, and
// upserts the resulting points.
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
	if err := i.cfg.Store.Upsert(ctx, i.cfg.Collection, points, true); err != nil {
		return 0, fmt.Errorf("upserting %s: %w", f.RelPath, err)
	}
	return len(points), nil
}

// embedBatches chunks the embedding work into BatchSize windows and
// produces the corresponding qdrant.Point slice.
func (i *Indexer) embedBatches(ctx context.Context, f MarkdownFile, chunks []string) ([]qdrant.Point, error) {
	points := make([]qdrant.Point, 0, len(chunks))
	for start := 0; start < len(chunks); start += i.cfg.BatchSize {
		end := start + i.cfg.BatchSize
		if end > len(chunks) {
			end = len(chunks)
		}
		for idx := start; idx < end; idx++ {
			vec, err := i.cfg.Embedder.Embed(ctx, chunks[idx])
			if err != nil {
				return nil, fmt.Errorf("embedding %s chunk %d: %w", f.RelPath, idx, err)
			}
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

// pointID returns a deterministic UUID-v5 identifier for a (file, chunk) pair.
//
// Qdrant accepts either an unsigned integer or an RFC 4122 UUID as a point
// ID. The UUID-v5 derivation gives every (file, chunk_index) tuple a stable
// identifier, so re-indexes overwrite the existing row rather than
// duplicating it. The namespace is fixed in pointNamespace.
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
func (noopLogger) Printf(_ string, _ ...any) {}
