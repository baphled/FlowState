// Package cli provides the "vault" command group for FlowState.
package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/ollama"
	qdrantclient "github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

// newVaultCmd creates the "vault" command group with "index" and "sync"
// subcommands so operators and agents can keep the RAG knowledge base current
// without running the full vault-rag MCP server.
//
// Expected:
//   - getApp is a non-nil factory that returns the initialised App instance.
//
// Returns:
//   - A configured cobra.Command with index and sync subcommands.
func newVaultCmd(getApp func() *app.App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "vault",
		Short: "Manage vault RAG indexing",
		Long: "Commands to index and sync the Obsidian vault into Qdrant so agents can " +
			"query up-to-date knowledge via the vault_index and vault_sync tools. " +
			"\"vault index\" re-embeds everything; \"vault sync\" skips files whose " +
			"modification time has not changed since the last run.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newVaultIndexCmd(getApp))
	cmd.AddCommand(newVaultSyncCmd(getApp))
	return cmd
}

// newVaultIndexCmd creates the "vault index" subcommand.
//
// Expected:
//   - getApp is a non-nil factory that returns the initialised App instance.
//
// Returns:
//   - A configured cobra.Command with --vault-root, --collection,
//     --qdrant-url, --ollama-host, --embedding-model, and --reindex flags.
func newVaultIndexCmd(getApp func() *app.App) *cobra.Command {
	var (
		vaultRoot      string
		collection     string
		qdrantURL      string
		ollamaHost     string
		embeddingModel string
		reindex        bool
	)

	cmd := &cobra.Command{
		Use:   "index",
		Short: "Index the vault into Qdrant (force-embeds all files)",
		Long: "Walk the vault root, chunk every markdown file, embed via Ollama, and upsert " +
			"into Qdrant. Skips files that are already up-to-date unless --reindex is given. " +
			"Flags override the corresponding config file settings.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := getApp().Config
			opts := resolveVaultRunOptions(cfg, vaultRoot, collection, qdrantURL, ollamaHost, embeddingModel, reindex)
			return runVaultIndex(cmd.Context(), cmd, opts)
		},
	}

	addVaultFlags(cmd, &vaultRoot, &collection, &qdrantURL, &ollamaHost, &embeddingModel)
	cmd.Flags().BoolVar(&reindex, "reindex", false, "Force re-embedding of all files regardless of sidecar state")
	return cmd
}

// newVaultSyncCmd creates the "vault sync" subcommand.
//
// Expected:
//   - getApp is a non-nil factory that returns the initialised App instance.
//
// Returns:
//   - A configured cobra.Command with --vault-root, --collection,
//     --qdrant-url, --ollama-host, and --embedding-model flags.
func newVaultSyncCmd(getApp func() *app.App) *cobra.Command {
	var (
		vaultRoot      string
		collection     string
		qdrantURL      string
		ollamaHost     string
		embeddingModel string
	)

	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Sync changed vault files into Qdrant (incremental)",
		Long: "Walk the vault root and embed only files whose modification time has advanced " +
			"past the sidecar record. Use this for routine cron/agent-triggered updates " +
			"where a full reindex would be too slow.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := getApp().Config
			opts := resolveVaultRunOptions(cfg, vaultRoot, collection, qdrantURL, ollamaHost, embeddingModel, false)
			return runVaultIndex(cmd.Context(), cmd, opts)
		},
	}

	addVaultFlags(cmd, &vaultRoot, &collection, &qdrantURL, &ollamaHost, &embeddingModel)
	return cmd
}

// vaultRunOptions holds the resolved settings for a single vault index run.
type vaultRunOptions struct {
	VaultRoot      string
	Collection     string
	QdrantURL      string
	OllamaHost     string
	EmbeddingModel string
	Reindex        bool
}

// resolveVaultRunOptions merges config-file defaults with flag overrides.
//
// Flag values (non-empty strings, true bool) take precedence over config.
func resolveVaultRunOptions(
	cfg *config.AppConfig,
	vaultRoot, collection, qdrantURL, ollamaHost, embeddingModel string,
	reindex bool,
) vaultRunOptions {
	opts := vaultRunOptions{Reindex: reindex}

	opts.VaultRoot = firstNonEmpty(vaultRoot, cfg.VaultPath)
	opts.Collection = firstNonEmpty(collection, cfg.VaultCollection, "flowstate-vault")
	opts.QdrantURL = firstNonEmpty(qdrantURL, cfg.Qdrant.URL, "http://localhost:6333")
	opts.OllamaHost = firstNonEmpty(ollamaHost, cfg.Providers.Ollama.Host, "http://localhost:11434")
	opts.EmbeddingModel = firstNonEmpty(embeddingModel, cfg.ResolvedEmbeddingModel())
	return opts
}

// runVaultIndex performs the walk + embed + upsert pass and prints a summary.
//
// Expected:
//   - opts.VaultRoot is a non-empty path to an existing directory.
//   - opts.QdrantURL and opts.OllamaHost are reachable endpoints.
//
// Returns:
//   - nil on success.
//   - A wrapped error on any failure; the exit code will be non-zero.
//
// Side effects:
//   - Sends embed requests to Ollama and upserts to Qdrant.
//   - Writes the sidecar state file inside VaultRoot.
//   - Prints a summary line to cmd.OutOrStdout().
func runVaultIndex(ctx context.Context, cmd *cobra.Command, opts vaultRunOptions) error {
	if opts.VaultRoot == "" {
		return fmt.Errorf("vault root is not set: use --vault-root or set vault_path in config")
	}
	if _, err := os.Stat(opts.VaultRoot); err != nil {
		return fmt.Errorf("vault root %q: %w", opts.VaultRoot, err)
	}

	ollamaProv, err := ollama.NewWithClient(opts.OllamaHost, &http.Client{})
	if err != nil {
		return fmt.Errorf("creating ollama client: %w", err)
	}
	embedder := vaultOllamaAdapter{provider: ollamaProv, model: opts.EmbeddingModel}

	store := qdrantclient.NewClient(opts.QdrantURL, "", nil)
	logger := log.New(os.Stderr, "flowstate vault: ", log.LstdFlags)

	state, err := vaultindex.LoadState(vaultindex.SidecarPath(opts.VaultRoot))
	if err != nil {
		return fmt.Errorf("loading sidecar: %w", err)
	}

	indexer := vaultindex.NewIndexer(vaultindex.IndexerConfig{
		VaultRoot:    opts.VaultRoot,
		Collection:   opts.Collection,
		EmbeddingDim: vaultindex.DefaultEmbeddingDim,
		Reindex:      opts.Reindex,
		Chunker:      vaultindex.NewChunker(vaultindex.DefaultChunkSize, vaultindex.DefaultChunkOverlap),
		Embedder:     embedder,
		Store:        store,
		State:        state,
		Logger:       logger,
	})

	if err := indexer.EnsureCollection(ctx); err != nil {
		return fmt.Errorf("ensure collection: %w", err)
	}

	summary, err := indexer.IndexAll(ctx)
	if err != nil {
		return fmt.Errorf("indexing vault: %w", err)
	}

	_, err = fmt.Fprintf(cmd.OutOrStdout(),
		"indexed vault: total=%d indexed=%d skipped=%d chunks=%d collection=%s\n",
		summary.Total, summary.Indexed, summary.Skipped, summary.Chunks, opts.Collection)
	return err
}

// addVaultFlags registers the shared vault flags onto cmd.
func addVaultFlags(cmd *cobra.Command, vaultRoot, collection, qdrantURL, ollamaHost, embeddingModel *string) {
	cmd.Flags().StringVar(vaultRoot, "vault-root", "", "Path to the Obsidian vault root (overrides vault_path in config)")
	cmd.Flags().StringVar(collection, "collection", "", "Qdrant collection name (overrides vault_collection in config)")
	cmd.Flags().StringVar(qdrantURL, "qdrant-url", "", "Qdrant base URL (overrides qdrant.url in config)")
	cmd.Flags().StringVar(ollamaHost, "ollama-host", "", "Ollama base URL (overrides providers.ollama.host in config)")
	cmd.Flags().StringVar(embeddingModel, "embedding-model", "", "Embedding model name (overrides embedding_model in config)")
}

// firstNonEmpty returns the first non-empty string in vals.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// vaultOllamaAdapter bridges the ollama.Provider Embed method onto
// vaultindex.Embedder.
type vaultOllamaAdapter struct {
	provider *ollama.Provider
	model    string
}

// Embed implements vaultindex.Embedder by delegating to the Ollama provider.
func (a vaultOllamaAdapter) Embed(ctx context.Context, text string) ([]float64, error) {
	return a.provider.Embed(ctx, provider.EmbedRequest{Input: text, Model: a.model})
}
