// Package main provides the FlowState vault-rag MCP server.
//
// flowstate-vault-server walks an Obsidian vault, chunks every markdown
// file into overlapping token windows, embeds those chunks via Ollama
// (nomic-embed-text by default), upserts them into a Qdrant collection,
// and exposes a query_vault MCP tool that returns the chunk shape consumed
// by internal/recall/vault.Source.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
	"github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

const (
	defaultQdrantURL        = "http://localhost:6333"
	defaultQdrantCollection = "flowstate-vault"
	defaultOllamaHost       = "http://localhost:11434"
	defaultEmbeddingModel   = "nomic-embed-text"
	serverName              = "flowstate-vault-server"
	serverVersion           = "0.1.0"
)

// runtimeConfig holds the resolved CLI/env configuration for one run.
type runtimeConfig struct {
	VaultRoot        string
	QdrantURL        string
	QdrantCollection string
	OllamaHost       string
	EmbeddingModel   string
	ChunkSize        int
	ChunkOverlap     int
	Reindex          bool
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// newRootCmd builds the cobra root command for the vault server.
//
// Returns:
//   - A configured *cobra.Command whose RunE walks the vault, indexes new
//     content, and runs the MCP server on stdio.
//
// Side effects:
//   - None at construction; flags are bound but not parsed.
func newRootCmd() *cobra.Command {
	cfg := defaultRuntimeConfig()
	cmd := &cobra.Command{
		Use:           serverName,
		Short:         "Walks an Obsidian vault, indexes chunks into Qdrant, and serves query_vault over MCP.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(c *cobra.Command, _ []string) error {
			applyEnvDefaults(&cfg)
			if err := validate(&cfg); err != nil {
				return err
			}
			return run(c.Context(), cfg)
		},
	}
	bindFlags(cmd, &cfg)
	return cmd
}

// defaultRuntimeConfig returns the baseline runtime configuration.
func defaultRuntimeConfig() runtimeConfig {
	return runtimeConfig{
		QdrantURL:        defaultQdrantURL,
		QdrantCollection: defaultQdrantCollection,
		OllamaHost:       defaultOllamaHost,
		EmbeddingModel:   defaultEmbeddingModel,
		ChunkSize:        vaultindex.DefaultChunkSize,
		ChunkOverlap:     vaultindex.DefaultChunkOverlap,
	}
}

// bindFlags wires every CLI flag onto cmd.
func bindFlags(cmd *cobra.Command, cfg *runtimeConfig) {
	cmd.Flags().StringVar(&cfg.VaultRoot, "vault-root", cfg.VaultRoot, "Path to the Obsidian vault root (or set FLOWSTATE_VAULT_ROOT).")
	cmd.Flags().StringVar(&cfg.QdrantURL, "qdrant-url", cfg.QdrantURL, "Qdrant base URL (or QDRANT_URL).")
	cmd.Flags().StringVar(&cfg.QdrantCollection, "qdrant-collection", cfg.QdrantCollection, "Qdrant collection name (or QDRANT_COLLECTION).")
	cmd.Flags().StringVar(&cfg.OllamaHost, "ollama-host", cfg.OllamaHost, "Ollama base URL (or OLLAMA_HOST).")
	cmd.Flags().StringVar(&cfg.EmbeddingModel, "embedding-model", cfg.EmbeddingModel, "Embedding model name (or EMBEDDING_MODEL).")
	cmd.Flags().IntVar(&cfg.ChunkSize, "chunk-size", cfg.ChunkSize, "Chunk size in tokens.")
	cmd.Flags().IntVar(&cfg.ChunkOverlap, "chunk-overlap", cfg.ChunkOverlap, "Chunk overlap in tokens.")
	cmd.Flags().BoolVar(&cfg.Reindex, "reindex", false, "Re-walk and re-embed every file regardless of sidecar state.")
}

// applyEnvDefaults overlays environment variables onto unset fields.
//
// CLI flags take precedence; env values fill in gaps and override the
// hard-coded defaults assigned by defaultRuntimeConfig.
func applyEnvDefaults(cfg *runtimeConfig) {
	if v := os.Getenv("FLOWSTATE_VAULT_ROOT"); v != "" && cfg.VaultRoot == "" {
		cfg.VaultRoot = v
	}
	if v := os.Getenv("QDRANT_URL"); v != "" && cfg.QdrantURL == defaultQdrantURL {
		cfg.QdrantURL = v
	}
	if v := os.Getenv("QDRANT_COLLECTION"); v != "" && cfg.QdrantCollection == defaultQdrantCollection {
		cfg.QdrantCollection = v
	}
	if v := os.Getenv("OLLAMA_HOST"); v != "" && cfg.OllamaHost == defaultOllamaHost {
		cfg.OllamaHost = v
	}
	if v := os.Getenv("EMBEDDING_MODEL"); v != "" && cfg.EmbeddingModel == defaultEmbeddingModel {
		cfg.EmbeddingModel = v
	}
}

// validate enforces required fields.
func validate(cfg *runtimeConfig) error {
	if cfg.VaultRoot == "" {
		return fmt.Errorf("vault root is required: set --vault-root or FLOWSTATE_VAULT_ROOT")
	}
	info, err := os.Stat(cfg.VaultRoot)
	if err != nil {
		return fmt.Errorf("stat vault root %s: %w", cfg.VaultRoot, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("vault root %s is not a directory", cfg.VaultRoot)
	}
	return nil
}

// run wires every dependency and starts the MCP server.
//
// The function indexes the vault on startup, then registers query_vault
// and blocks on stdio until the parent process disconnects.
func run(ctx context.Context, cfg runtimeConfig) error {
	if ctx == nil {
		ctx = context.Background()
	}
	logger := log.New(os.Stderr, serverName+": ", log.LstdFlags)

	embedder, err := buildEmbedder(cfg)
	if err != nil {
		return err
	}
	qdrantClient := qdrant.NewClient(cfg.QdrantURL, "", nil)

	if err := indexOnStartup(ctx, cfg, embedder, qdrantClient, logger); err != nil {
		return err
	}

	server := mcp.NewServer(
		&mcp.Implementation{Name: serverName, Version: serverVersion},
		nil,
	)
	handler := vaultindex.NewQueryHandler(embedder, qdrantClient, cfg.QdrantCollection)
	vaultindex.RegisterQueryTool(server, handler)

	logger.Printf("ready: collection=%s vault=%s model=%s",
		cfg.QdrantCollection, cfg.VaultRoot, cfg.EmbeddingModel)

	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp server: %w", err)
	}
	return nil
}

// indexOnStartup performs the walk + embed + upsert pass before serving.
func indexOnStartup(ctx context.Context, cfg runtimeConfig, embedder vaultindex.Embedder, store vaultindex.VectorStore, logger *log.Logger) error {
	state, err := vaultindex.LoadState(vaultindex.SidecarPath(cfg.VaultRoot))
	if err != nil {
		return err
	}
	indexer := vaultindex.NewIndexer(vaultindex.IndexerConfig{
		VaultRoot:    cfg.VaultRoot,
		Collection:   cfg.QdrantCollection,
		EmbeddingDim: vaultindex.DefaultEmbeddingDim,
		Reindex:      cfg.Reindex,
		Chunker:      vaultindex.NewChunker(cfg.ChunkSize, cfg.ChunkOverlap),
		Embedder:     embedder,
		Store:        store,
		State:        state,
		Logger:       logger,
	})
	if err := indexer.EnsureCollection(ctx); err != nil {
		return err
	}
	summary, err := indexer.IndexAll(ctx)
	if err != nil {
		return err
	}
	logger.Printf("startup index: total=%d indexed=%d skipped=%d chunks=%d",
		summary.Total, summary.Indexed, summary.Skipped, summary.Chunks)
	return nil
}

// buildEmbedder constructs an Embedder backed by the Ollama HTTP client.
func buildEmbedder(cfg runtimeConfig) (vaultindex.Embedder, error) {
	prov, err := ollama.NewWithClient(cfg.OllamaHost, nil)
	if err != nil {
		return nil, fmt.Errorf("creating ollama client: %w", err)
	}
	return ollamaAdapter{provider: prov, model: cfg.EmbeddingModel}, nil
}

// ollamaAdapter bridges the ollama provider's Embed signature onto the
// vaultindex.Embedder interface.
type ollamaAdapter struct {
	provider *ollama.Provider
	model    string
}

// Embed implements vaultindex.Embedder by delegating to the Ollama provider.
func (o ollamaAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	return o.provider.Embed(ctx, provider.EmbedRequest{Input: text, Model: o.model})
}
