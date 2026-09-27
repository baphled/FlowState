//go:build e2e

package support

import (
	"testing"

	"github.com/baphled/flowstate/internal/vaultindex"
)

// TestMockProviderEmbedReturnsDefaultDim pins the mock provider's default
// embeddings to the collection dimensionality (vaultindex.DefaultEmbeddingDim).
// Stale 384-dim mock vectors caused dimension-mismatch failures against
// 768-dim Qdrant collections in BDD scenarios.
func TestMockProviderEmbedReturnsDefaultDim(t *testing.T) {
	p := NewMockProvider()
	vec, err := p.Embed(t.Context(), EmbedRequest{Input: "hello"})
	if err != nil {
		t.Fatalf("Embed returned error: %v", err)
	}
	if got, want := len(vec), vaultindex.DefaultEmbeddingDim; got != want {
		t.Fatalf("Embed vector length = %d, want %d (vaultindex.DefaultEmbeddingDim)", got, want)
	}
}
