package vaultindex

import "strings"

// DefaultChunkSize is the default chunk size in tokens (whitespace-separated words).
const DefaultChunkSize = 512

// DefaultChunkOverlap is the default overlap between adjacent chunks in tokens.
const DefaultChunkOverlap = 50

// Chunker splits markdown text into overlapping token windows.
//
// The Chunker treats whitespace-separated words as a coarse approximation of
// tokens. nomic-embed-text accepts up to ~8K tokens per request, so the
// default 512-token window with 50-token overlap leaves ample headroom while
// preserving cross-chunk context.
type Chunker struct {
	size    int
	overlap int
}

// NewChunker constructs a Chunker with the supplied size and overlap.
//
// Expected:
//   - size is positive; non-positive values fall back to DefaultChunkSize.
//   - overlap is non-negative and strictly less than size; out-of-range
//     values fall back to DefaultChunkOverlap (capped at size-1).
//
// Returns:
//   - A *Chunker ready to split markdown.
//
// Side effects:
//   - None.
func NewChunker(size, overlap int) *Chunker {
	if size <= 0 {
		size = DefaultChunkSize
	}
	if overlap < 0 || overlap >= size {
		overlap = DefaultChunkOverlap
		if overlap >= size {
			overlap = size - 1
		}
	}
	return &Chunker{size: size, overlap: overlap}
}

// Size returns the configured chunk size in tokens.
func (c *Chunker) Size() int { return c.size }

// Overlap returns the configured chunk overlap in tokens.
func (c *Chunker) Overlap() int { return c.overlap }

// Chunk splits text into overlapping token windows.
//
// Expected:
//   - text is the raw markdown body; empty or whitespace-only input yields
//     no chunks.
//
// Returns:
//   - A slice of chunk strings. Each chunk holds at most Size() tokens, and
//     consecutive chunks share Overlap() trailing tokens with the next.
//
// Side effects:
//   - None.
func (c *Chunker) Chunk(text string) []string {
	tokens := tokenize(text)
	if len(tokens) == 0 {
		return nil
	}
	stride := c.size - c.overlap
	if stride <= 0 {
		stride = 1
	}
	chunks := make([]string, 0, (len(tokens)/stride)+1)
	for start := 0; start < len(tokens); start += stride {
		end := start + c.size
		if end > len(tokens) {
			end = len(tokens)
		}
		chunks = append(chunks, strings.Join(tokens[start:end], " "))
		if end == len(tokens) {
			break
		}
	}
	return chunks
}

// tokenize splits text into whitespace-separated tokens.
//
// Expected:
//   - text is any string; leading and trailing whitespace are ignored.
//
// Returns:
//   - A slice of non-empty whitespace-separated tokens.
//
// Side effects:
//   - None.
func tokenize(text string) []string {
	return strings.Fields(text)
}
