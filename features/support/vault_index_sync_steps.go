//go:build e2e

package support

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/recall/qdrant"
	"github.com/baphled/flowstate/internal/vaultindex"
)

// vaultIndexSyncState holds scenario-scoped state for vault index/sync steps.
type vaultIndexSyncState struct {
	vaultRoot   string
	collection  string
	lastSummary vaultindex.Summary
	lastErr     error
	exitCode    int
	output      string
}

var vaultSyncScenarioState *vaultIndexSyncState

func init() {
	vaultSyncScenarioState = &vaultIndexSyncState{}
}

// RegisterVaultIndexSyncSteps wires the vault index/sync BDD steps.
func RegisterVaultIndexSyncSteps(ctx *godog.ScenarioContext) {
	s := &vaultIndexSyncState{}

	ctx.Before(func(_ context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return nil, nil
	})

	ctx.Step(`^a temporary vault directory with markdown files$`, s.aTempVaultWithMarkdownFiles)
	ctx.Step(`^the vault has been indexed once$`, s.theVaultHasBeenIndexedOnce)
	ctx.Step(`^a vault file has been modified since the last index$`, s.aVaultFileHasBeenModified)
	ctx.Step(`^FlowState is configured with vault RAG$`, s.flowstateConfiguredWithVaultRAG)

	ctx.Step(`^I run the vault index command$`, s.iRunVaultIndex)
	ctx.Step(`^I run the vault index command with "--vault-root" set to the temp vault$`, s.iRunVaultIndexWithVaultRoot)
	ctx.Step(`^I run the vault index command with "--collection" set to "([^"]*)"$`, s.iRunVaultIndexWithCollection)
	ctx.Step(`^I run the vault index command with "--vault-root" set to "([^"]*)"$`, s.iRunVaultIndexWithVaultRootPath)
	ctx.Step(`^I run the vault index command with "--reindex"$`, s.iRunVaultIndexWithReindex)
	ctx.Step(`^I run the vault sync command$`, s.iRunVaultSync)
	ctx.Step(`^I run the vault sync command with "--vault-root" set to the temp vault$`, s.iRunVaultSyncWithVaultRoot)

	ctx.Step(`^the exit code should be 0$`, s.exitCodeShouldBeZero)
	ctx.Step(`^the exit code should not be 0$`, s.exitCodeShouldBeNonZero)
	ctx.Step(`^the output should contain "([^"]*)"$`, s.outputShouldContain)
	ctx.Step(`^the sidecar state file should exist$`, s.sidecarStateShouldExist)

	ctx.Step(`^an agent calls the "([^"]*)" tool with the temp vault path$`, s.agentCallsVaultTool)
	ctx.Step(`^the tool result should indicate success$`, s.toolResultShouldIndicateSuccess)
	ctx.Step(`^the result should include a summary with "([^"]*)" count$`, s.resultShouldIncludeSummaryWith)
}

func (s *vaultIndexSyncState) reset() {
	s.vaultRoot = ""
	s.collection = "test-vault-collection"
	s.lastSummary = vaultindex.Summary{}
	s.lastErr = nil
	s.exitCode = 0
	s.output = ""
}

func (s *vaultIndexSyncState) aTempVaultWithMarkdownFiles() error {
	dir, err := os.MkdirTemp("", "flowstate-vault-*")
	if err != nil {
		return fmt.Errorf("creating temp vault: %w", err)
	}
	s.vaultRoot = dir

	files := map[string]string{
		"note-a.md": "# Note A\n\nThis is the first test note.",
		"note-b.md": "# Note B\n\nThis is the second test note.",
		filepath.Join("sub", "note-c.md"): "# Note C\n\nThis is a nested note.",
	}
	for rel, body := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func (s *vaultIndexSyncState) theVaultHasBeenIndexedOnce() error {
	if s.vaultRoot == "" {
		if err := s.aTempVaultWithMarkdownFiles(); err != nil {
			return err
		}
	}
	_, err := s.runIndexer(false)
	return err
}

func (s *vaultIndexSyncState) aVaultFileHasBeenModified() error {
	if s.vaultRoot == "" {
		return fmt.Errorf("vault root not initialised")
	}
	target := filepath.Join(s.vaultRoot, "note-a.md")
	body := fmt.Sprintf("# Note A\n\nModified at %s.", time.Now().Format(time.RFC3339Nano))
	if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
		return err
	}
	future := time.Now().Add(2 * time.Second)
	return os.Chtimes(target, future, future)
}

func (s *vaultIndexSyncState) flowstateConfiguredWithVaultRAG() error {
	return s.aTempVaultWithMarkdownFiles()
}

func (s *vaultIndexSyncState) iRunVaultIndex() error {
	summary, err := s.runIndexer(false)
	s.lastSummary = summary
	s.lastErr = err
	if err != nil {
		s.exitCode = 1
		s.output = err.Error()
		return nil
	}
	s.exitCode = 0
	s.output = formatSummary(summary)
	return nil
}

func (s *vaultIndexSyncState) iRunVaultIndexWithVaultRoot() error {
	return s.iRunVaultIndex()
}

func (s *vaultIndexSyncState) iRunVaultIndexWithCollection(collection string) error {
	s.collection = collection
	return s.iRunVaultIndex()
}

func (s *vaultIndexSyncState) iRunVaultIndexWithVaultRootPath(path string) error {
	saved := s.vaultRoot
	s.vaultRoot = path
	err := s.iRunVaultIndex()
	if s.exitCode != 0 {
		s.vaultRoot = saved
		return nil
	}
	s.vaultRoot = saved
	return err
}

func (s *vaultIndexSyncState) iRunVaultIndexWithReindex() error {
	summary, err := s.runIndexerReindex(true)
	s.lastSummary = summary
	s.lastErr = err
	if err != nil {
		s.exitCode = 1
		s.output = err.Error()
		return nil
	}
	s.exitCode = 0
	s.output = formatSummary(summary)
	return nil
}

func (s *vaultIndexSyncState) iRunVaultSync() error {
	summary, err := s.runIndexer(false)
	s.lastSummary = summary
	s.lastErr = err
	if err != nil {
		s.exitCode = 1
		s.output = err.Error()
		return nil
	}
	s.exitCode = 0
	s.output = formatSummary(summary)
	return nil
}

func (s *vaultIndexSyncState) iRunVaultSyncWithVaultRoot() error {
	return s.iRunVaultSync()
}

func (s *vaultIndexSyncState) exitCodeShouldBeZero() error {
	if s.exitCode != 0 {
		return fmt.Errorf("expected exit code 0, got %d (output: %s)", s.exitCode, s.output)
	}
	return nil
}

func (s *vaultIndexSyncState) exitCodeShouldBeNonZero() error {
	if s.exitCode == 0 {
		return fmt.Errorf("expected non-zero exit code, got 0")
	}
	return nil
}

func (s *vaultIndexSyncState) outputShouldContain(substr string) error {
	if !strings.Contains(s.output, substr) {
		return fmt.Errorf("output %q does not contain %q", s.output, substr)
	}
	return nil
}

func (s *vaultIndexSyncState) sidecarStateShouldExist() error {
	if s.vaultRoot == "" {
		return fmt.Errorf("vault root not set")
	}
	sidecar := filepath.Join(s.vaultRoot, vaultindex.SidecarFilename)
	if _, err := os.Stat(sidecar); err != nil {
		return fmt.Errorf("sidecar %s does not exist: %w", sidecar, err)
	}
	return nil
}

func (s *vaultIndexSyncState) agentCallsVaultTool(toolName string) error {
	switch toolName {
	case "vault_index":
		return s.iRunVaultIndex()
	case "vault_sync":
		return s.iRunVaultSync()
	default:
		return fmt.Errorf("unknown vault tool %q", toolName)
	}
}

func (s *vaultIndexSyncState) toolResultShouldIndicateSuccess() error {
	return s.exitCodeShouldBeZero()
}

func (s *vaultIndexSyncState) resultShouldIncludeSummaryWith(key string) error {
	return s.outputShouldContain(key + "=")
}

// runIndexer builds a stub indexer and runs IndexAll.
func (s *vaultIndexSyncState) runIndexer(reindex bool) (vaultindex.Summary, error) {
	return s.runIndexerReindex(reindex)
}

func (s *vaultIndexSyncState) runIndexerReindex(reindex bool) (vaultindex.Summary, error) {
	root := s.vaultRoot
	if root == "" {
		return vaultindex.Summary{}, fmt.Errorf("vault root not set")
	}

	if _, err := os.Stat(root); err != nil {
		return vaultindex.Summary{}, fmt.Errorf("vault root does not exist: %w", err)
	}

	store := newStubVectorStore()
	embedder := stubEmbedder{}
	state, err := vaultindex.LoadState(vaultindex.SidecarPath(root))
	if err != nil {
		return vaultindex.Summary{}, fmt.Errorf("loading sidecar: %w", err)
	}
	chunker := vaultindex.NewChunker(vaultindex.DefaultChunkSize, vaultindex.DefaultChunkOverlap)

	idx := vaultindex.NewIndexer(vaultindex.IndexerConfig{
		VaultRoot:    root,
		Collection:   s.collection,
		BatchSize:    vaultindex.DefaultBatchSize,
		EmbeddingDim: vaultindex.DefaultEmbeddingDim,
		Reindex:      reindex,
		Chunker:      chunker,
		Embedder:     embedder,
		Store:        store,
		State:        state,
	})

	ctx := context.Background()
	if err := idx.EnsureCollection(ctx); err != nil {
		return vaultindex.Summary{}, fmt.Errorf("ensure collection: %w", err)
	}
	return idx.IndexAll(ctx)
}

// formatSummary renders the indexer summary into a string the CLI would print.
func formatSummary(s vaultindex.Summary) string {
	return fmt.Sprintf("total=%d indexed=%d skipped=%d chunks=%d",
		s.Total, s.Indexed, s.Skipped, s.Chunks)
}

// stubEmbedder always returns a fixed 768-dim zero vector.
type stubEmbedder struct{}

func (stubEmbedder) Embed(_ context.Context, _ string) ([]float64, error) {
	vec := make([]float64, vaultindex.DefaultEmbeddingDim)
	return vec, nil
}

// stubVectorStore is an in-memory VectorStore for tests.
type stubVectorStore struct {
	collections map[string]bool
	points      map[string][]qdrant.Point
}

func newStubVectorStore() *stubVectorStore {
	return &stubVectorStore{
		collections: make(map[string]bool),
		points:      make(map[string][]qdrant.Point),
	}
}

func (s *stubVectorStore) CollectionExists(_ context.Context, name string) (bool, error) {
	return s.collections[name], nil
}

func (s *stubVectorStore) CreateCollection(_ context.Context, name string, _ qdrant.CollectionConfig) error {
	s.collections[name] = true
	return nil
}

func (s *stubVectorStore) Upsert(_ context.Context, collection string, points []qdrant.Point, _ bool) error {
	s.points[collection] = append(s.points[collection], points...)
	return nil
}
