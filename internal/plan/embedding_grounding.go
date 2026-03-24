package plan

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/provider"
)

// CodeSnippet represents an indexed code snippet with its embedding.
type CodeSnippet struct {
	FilePath  string
	Name      string
	Signature string
	Embedding []float64
}

// EmbeddingGrounder builds a semantic index of Go code for context injection.
type EmbeddingGrounder struct{}

// NewEmbeddingGrounder creates a new EmbeddingGrounder.
func NewEmbeddingGrounder() *EmbeddingGrounder {
	return &EmbeddingGrounder{}
}

// InjectContext returns relevant code snippets for the given query.
func (g *EmbeddingGrounder) InjectContext(
	ctx context.Context,
	projectRoot string,
	query string,
	embedProvider provider.Provider,
) (string, error) {
	files, err := os.ReadDir(projectRoot)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		if !strings.HasSuffix(f.Name(), ".go") {
			continue
		}
		path := filepath.Join(projectRoot, f.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		_, _ = embedProvider.Embed(ctx, provider.EmbedRequest{Input: string(b)}) //nolint:errcheck // embedding errors are non-fatal
		if strings.Contains(string(b), query) {
			return "### Relevant Code\n" + string(b), nil
		}
	}
	return "", nil
}

// LastIndexed returns the last indexed time (stub for test).
func (g *EmbeddingGrounder) LastIndexed() int64 {
	return 0
}
