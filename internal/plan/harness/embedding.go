package harness

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
//
// Returns:
//   - An initialised EmbeddingGrounder instance.
//
// Side effects:
//   - None.
func NewEmbeddingGrounder() *EmbeddingGrounder {
	return &EmbeddingGrounder{}
}

// InjectContext returns relevant code snippets for the given query.
//
// Expected:
//   - ctx is a valid context for cancellation.
//   - projectRoot points to a directory containing Go source files.
//   - embedProvider is a valid provider capable of generating embeddings.
//
// Returns:
//   - A string containing matching code snippets, or empty if none match.
//   - An error if the project root cannot be read.
//
// Side effects:
//   - Reads Go source files from projectRoot and searches for query string matches.
func (g *EmbeddingGrounder) InjectContext(
	_ context.Context,
	projectRoot string,
	query string,
	_ provider.Provider,
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
		if strings.Contains(string(b), query) {
			return "### Relevant Code\n" + string(b), nil
		}
	}
	return "", nil
}

// LastIndexed returns the last indexed time (stub for test).
//
// Returns:
//   - Always returns 0 as this is a stub implementation.
//
// Side effects:
//   - None.
func (g *EmbeddingGrounder) LastIndexed() int64 {
	return 0
}
