package learning

// NoveltyDetector decides whether a given output is sufficiently novel to warrant
// a learning capture.
//
// Implementations may use semantic similarity, keyword heuristics, or any other
// strategy. The detector must not block and must be safe to call concurrently.
type NoveltyDetector interface {
	// IsNovel returns true when the output should trigger a learning capture.
	IsNovel(output string) bool
}

// DuplicateCheckDetector uses a RecallClient to determine novelty: an output
// is considered novel when no existing entry scores above the similarity threshold.
type DuplicateCheckDetector struct {
	recall    RecallClient
	threshold float64
	limit     int
}

// NewDuplicateCheckDetector creates a DuplicateCheckDetector with the given recall client
// and similarity threshold.
//
// Expected:
//   - rc is a non-nil RecallClient.
//   - threshold is a similarity score in [0, 1]; outputs with no match above this
//     threshold are considered novel.
//
// Returns:
//   - A configured *DuplicateCheckDetector.
//
// Side effects:
//   - None.
func NewDuplicateCheckDetector(rc RecallClient, threshold float64) *DuplicateCheckDetector {
	return &DuplicateCheckDetector{recall: rc, threshold: threshold, limit: 1}
}

// IsNovel returns true when no recalled entry scores at or above the threshold.
//
// Expected:
//   - output is the agent output to evaluate.
//
// Returns:
//   - true if the output is novel (no close match found); false otherwise.
//   - Falls back to true (novel) if the recall search fails.
//
// Side effects:
//   - Calls RecallClient.Search with a limit of 1.
func (d *DuplicateCheckDetector) IsNovel(output string) bool {
	matches, err := d.recall.Search(output, d.limit)
	if err != nil {
		return true
	}
	for _, m := range matches {
		if m.Score >= d.threshold {
			return false
		}
	}
	return true
}
