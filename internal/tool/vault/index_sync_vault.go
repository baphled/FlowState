package vault

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
	qdrantclient "github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/vaultindex"
)

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
func NewIndexVaultTool(cfg IndexerConfig) *IndexVaultTool {
	return &IndexVaultTool{cfg: cfg}
}

// Name returns the tool name used by agents.
func (t *IndexVaultTool) Name() string { return "vault_index" }

// Description summarises the tool for the model.
func (t *IndexVaultTool) Description() string {
	return "Index (or re-index) the Obsidian vault into Qdrant so agents can query up-to-date knowledge"
}

// Schema returns the input schema for the tool.
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
func NewSyncVaultTool(cfg IndexerConfig) *SyncVaultTool {
	return &SyncVaultTool{cfg: cfg}
}

// Name returns the tool name used by agents.
func (t *SyncVaultTool) Name() string { return "vault_sync" }

// Description summarises the tool for the model.
func (t *SyncVaultTool) Description() string {
	return "Sync changed vault files into Qdrant (incremental — skips files whose mtime has not advanced)"
}

// Schema returns the input schema for the tool.
func (t *SyncVaultTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"vault_root": {Type: "string", Description: "Vault root path (overrides config when set)"},
		},
	}
}

// Execute runs an incremental vault sync pass and returns a summary.
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
func runVaultIndexerTool(ctx context.Context, cfg IndexerConfig) (vaultindex.Summary, error) {
	if cfg.VaultRoot == "" {
		return vaultindex.Summary{}, fmt.Errorf("vault_root is required")
	}
	if _, err := os.Stat(cfg.VaultRoot); err != nil {
		return vaultindex.Summary{}, fmt.Errorf("vault root %q: %w", cfg.VaultRoot, err)
	}

	prov, err := ollama.NewWithClient(cfg.OllamaHost, &http.Client{})
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

// vaultToolOllamaAdapter bridges ollama.Provider onto vaultindex.Embedder.
type vaultToolOllamaAdapter struct {
	provider *ollama.Provider
	model    string
}

// Embed implements vaultindex.Embedder.
func (a vaultToolOllamaAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	return a.provider.Embed(ctx, provider.EmbedRequest{Input: text, Model: a.model})
}
