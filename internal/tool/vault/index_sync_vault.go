package vault

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/provider/shared"
	qdrantclient "github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/vaultindex"
)

// vaultSyncWorkersEnv names the environment variable overriding the number
// of concurrent file workers during a vault sync/index pass. Bounded
// concurrency keeps a local Ollama embedder from saturating the machine;
// the default (vaultindex.DefaultWorkers = 2) is deliberately conservative.
const vaultSyncWorkersEnv = "VAULT_SYNC_WORKERS"

// IndexerConfig carries the settings needed to build and run a vault indexer.
type IndexerConfig struct {
	VaultRoot      string
	Collection     string
	QdrantURL      string
	OllamaHost     string
	EmbeddingModel string
	Reindex        bool
}

// IndexVaultTool implements vault_index: walks and embeds all vault files.
type IndexVaultTool struct {
	cfg IndexerConfig
}

// NewIndexVaultTool creates an IndexVaultTool with the supplied config.
//
// Expected: parameters for NewIndexVaultTool.
// Returns: result of NewIndexVaultTool.
// Side effects: None.
func NewIndexVaultTool(cfg IndexerConfig) *IndexVaultTool {
	return &IndexVaultTool{cfg: cfg}
}

// Name returns the tool name used by agents.
//
// Expected: parameters for Name.
// Returns: result of Name.
// Side effects: None.
func (t *IndexVaultTool) Name() string { return "vault_index" }

// Description summarises the tool for the model.
//
// Expected: parameters for Description.
// Returns: result of Description.
// Side effects: None.
func (t *IndexVaultTool) Description() string {
	return "Index (or re-index) the Obsidian vault into Qdrant so agents can query up-to-date knowledge"
}

// Schema returns the input schema for the tool.
//
// Expected: parameters for Schema.
// Returns: result of Schema.
// Side effects: None.
func (t *IndexVaultTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"vault_root": {Type: "string", Description: "Vault root path (overrides config when set)"},
			"reindex":    {Type: "boolean", Description: "Force re-embedding of all files (default false)"},
		},
	}
}

// Execute runs the full vault index pass and returns a summary.
//
// Expected: parameters for Execute.
// Returns: result of Execute.
// Side effects: None.
func (t *IndexVaultTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	cfg := t.cfg
	if v, ok := input.Arguments["vault_root"].(string); ok && v != "" {
		cfg.VaultRoot = v
	}
	if r, ok := input.Arguments["reindex"].(bool); ok {
		cfg.Reindex = r
	}
	cfg.Reindex = true

	summary, err := runVaultIndexerTool(ctx, cfg)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Output: fmt.Sprintf("vault indexed: total=%d indexed=%d skipped=%d chunks=%d",
			summary.Total, summary.Indexed, summary.Skipped, summary.Chunks),
	}, nil
}

// SyncVaultTool implements vault_sync: embeds only changed vault files.
type SyncVaultTool struct {
	cfg IndexerConfig
}

// NewSyncVaultTool creates a SyncVaultTool with the supplied config.
//
// Expected: parameters for NewSyncVaultTool.
// Returns: result of NewSyncVaultTool.
// Side effects: None.
func NewSyncVaultTool(cfg IndexerConfig) *SyncVaultTool {
	return &SyncVaultTool{cfg: cfg}
}

// Name returns the tool name used by agents.
//
// Expected: parameters for Name.
// Returns: result of Name.
// Side effects: None.
func (t *SyncVaultTool) Name() string { return "vault_sync" }

// Description summarises the tool for the model.
//
// Expected: parameters for Description.
// Returns: result of Description.
// Side effects: None.
func (t *SyncVaultTool) Description() string {
	return "Sync changed vault files into Qdrant (incremental — skips files whose mtime has not advanced)"
}

// Schema returns the input schema for the tool.
//
// Expected: parameters for Schema.
// Returns: result of Schema.
// Side effects: None.
func (t *SyncVaultTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"vault_root": {Type: "string", Description: "Vault root path (overrides config when set)"},
		},
	}
}

// Execute runs an incremental vault sync pass and returns a summary.
//
// Expected: parameters for Execute.
// Returns: result of Execute.
// Side effects: None.
func (t *SyncVaultTool) Execute(ctx context.Context, input tool.Input) (tool.Result, error) {
	cfg := t.cfg
	if v, ok := input.Arguments["vault_root"].(string); ok && v != "" {
		cfg.VaultRoot = v
	}
	cfg.Reindex = false

	summary, err := runVaultIndexerTool(ctx, cfg)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Output: fmt.Sprintf("vault synced: total=%d indexed=%d skipped=%d chunks=%d",
			summary.Total, summary.Indexed, summary.Skipped, summary.Chunks),
	}, nil
}

// runVaultIndexerTool wires the indexer from cfg and runs a single pass.
//
// Expected: parameters for runVaultIndexerTool.
// Returns: result of runVaultIndexerTool.
// Side effects: None.
func runVaultIndexerTool(ctx context.Context, cfg IndexerConfig) (vaultindex.Summary, error) {
	if cfg.VaultRoot == "" {
		return vaultindex.Summary{}, fmt.Errorf("vault_root is required")
	}
	if _, err := os.Stat(cfg.VaultRoot); err != nil {
		return vaultindex.Summary{}, fmt.Errorf("vault root %q: %w", cfg.VaultRoot, err)
	}

	// StreamGuardHTTPClient gives the embedder a ResponseHeaderTimeout so a
	// stalled Ollama process (accepts the connection, never responds) fails
	// the sync within a bounded window instead of hanging the tool thread
	// forever. Previously a bare &http.Client{} had no timeout at all.
	prov, err := ollama.NewWithClient(cfg.OllamaHost, shared.StreamGuardHTTPClient(shared.DefaultResponseHeaderTimeout))
	if err != nil {
		return vaultindex.Summary{}, fmt.Errorf("creating ollama client: %w", err)
	}
	embedder := vaultToolOllamaAdapter{provider: prov, model: cfg.EmbeddingModel}

	store := qdrantclient.NewClient(cfg.QdrantURL, "", nil)

	state, err := vaultindex.LoadState(vaultindex.SidecarPath(cfg.VaultRoot))
	if err != nil {
		return vaultindex.Summary{}, fmt.Errorf("loading sidecar: %w", err)
	}

	indexer := vaultindex.NewIndexer(vaultindex.IndexerConfig{
		VaultRoot:    cfg.VaultRoot,
		Collection:   cfg.Collection,
		EmbeddingDim: vaultindex.DefaultEmbeddingDim,
		Reindex:      cfg.Reindex,
		Workers:      vaultSyncWorkers(),
		Chunker:      vaultindex.NewChunker(vaultindex.DefaultChunkSize, vaultindex.DefaultChunkOverlap),
		Embedder:     embedder,
		Store:        store,
		State:        state,
	})

	if err := indexer.EnsureCollection(ctx); err != nil {
		return vaultindex.Summary{}, fmt.Errorf("ensure collection: %w", err)
	}
	return indexer.IndexAll(ctx)
}

// vaultSyncWorkers resolves the worker count for a sync pass: the
// VAULT_SYNC_WORKERS env var when set to a positive integer, otherwise the
// conservative vaultindex default.
//
// Expected: parameters for vaultSyncWorkers.
// Returns: result of vaultSyncWorkers.
// Side effects: None.
func vaultSyncWorkers() int {
	if raw := os.Getenv(vaultSyncWorkersEnv); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return vaultindex.DefaultWorkers
}

// vaultToolOllamaAdapter bridges ollama.Provider onto vaultindex.Embedder.
type vaultToolOllamaAdapter struct {
	provider *ollama.Provider
	model    string
}

// Embed implements vaultindex.Embedder.
//
// Expected: parameters for Embed.
// Returns: result of Embed.
// Side effects: None.
func (a vaultToolOllamaAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	return a.provider.Embed(ctx, provider.EmbedRequest{Input: text, Model: a.model})
}

// EmbedBatch implements vaultindex.BatchEmbedder: one multi-input Ollama
// /api/embed request per batch window instead of N single-text round-trips.
// A server that rejects batching returns an error and the indexer falls
// back to per-chunk Embed calls automatically.
//
// Expected: parameters for EmbedBatch.
// Returns: result of EmbedBatch.
// Side effects: None.
func (a vaultToolOllamaAdapter) EmbedBatch(ctx context.Context, input []string) ([][]float64, error) {
	return a.provider.EmbedMany(ctx, provider.EmbedManyRequest{Inputs: input, Model: a.model})
}
