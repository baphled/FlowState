package app

import (
	"encoding/json"
	"unicode/utf8"
)

// toolResultContent preserves fitting durable bytes. Oversized results become a
// valid JSON envelope; prefix search measures escaped serialized bytes, never
// clips a JSON token or splits a UTF8 rune, and does not modify the journal.
func toolResultContent(raw json.RawMessage, maxBytes int) (string, error) {
	if !json.Valid(raw) || !utf8.Valid(raw) {
		return "", ErrInvalidHistory
	}
	if maxBytes <= 0 {
		return "", ErrHistoryLimit
	}
	if len(raw) <= maxBytes {
		return string(raw), nil
	}
	envelope := historyResultPreview{Truncated: true, OriginalBytes: len(raw)}
	best, err := json.Marshal(envelope)
	if err != nil || len(best) > maxBytes {
		return "", ErrHistoryLimit
	}
	low, high := 0, maxBytes
	for low < high {
		mid := low + (high-low+1)/2
		end := mid
		for end > 0 && !utf8.RuneStart(raw[end]) {
			end--
		}
		envelope.Preview = string(raw[:end])
		encoded, encodeErr := json.Marshal(envelope)
		if encodeErr != nil {
			return "", encodeErr
		}
		if len(encoded) <= maxBytes {
			best, low = encoded, mid
		} else {
			high = mid - 1
		}
	}
	return string(best), nil
}
