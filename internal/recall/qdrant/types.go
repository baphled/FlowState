package qdrant

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrScoredPointMissingID is returned when a Qdrant scored point JSON
// object has no id field (or the id is empty JSON).
var ErrScoredPointMissingID = errors.New("qdrant: ScoredPoint missing id")

// Point represents a Qdrant point for upsert operations.
type Point struct {
	ID      string         `json:"id"`
	Vector  []float64      `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

// ScoredPoint represents a Qdrant search result with its relevance score.
//
// Qdrant point IDs are either unsigned integers or UUID strings. The ID
// field is always surfaced as a Go string: integer IDs are formatted in
// base-10 during decode so downstream callers can treat IDs uniformly.
// See UnmarshalJSON for the decode contract.
type ScoredPoint struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload,omitempty"`
}

// CollectionConfig describes the vector collection configuration.
type CollectionConfig struct {
	VectorSize int    `json:"vector_size"`
	Distance   string `json:"distance"`
}

// scoredPointWire is an intermediate JSON shape used to decode a Qdrant
// search hit. ID uses json.RawMessage so we can accept either a JSON
// string (UUID from FlowState-native writes) or a JSON number (integer
// ID from legacy mem0 / OpenCode writes).
type scoredPointWire struct {
	ID      json.RawMessage `json:"id"`
	Score   float64         `json:"score"`
	Payload map[string]any  `json:"payload,omitempty"`
}

// UnmarshalJSON decodes a Qdrant scored point whose id may be either a
// JSON string or a JSON number. The decoded ID is always returned as a
// string: numeric IDs are preserved as their base-10 decimal digits
// (e.g. JSON `1776075781962028658` becomes "1776075781962028658").
//
// Expected:
//   - data is a JSON object containing at least the "id" field.
//   - id is a JSON string or a JSON number (no other shapes are accepted).
//
// Returns:
//   - nil on success.
//   - A descriptive error wrapping json.UnmarshalTypeError or
//     json.SyntaxError when the payload is malformed or the id has an
//     unsupported shape (object, array, boolean, null).
//
// Side effects:
//   - Populates p.ID, p.Score, p.Payload.
func (p *ScoredPoint) UnmarshalJSON(data []byte) error {
	var wire scoredPointWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	p.Score = wire.Score
	p.Payload = wire.Payload

	raw := bytes.TrimSpace(wire.ID)
	if len(raw) == 0 {
		return ErrScoredPointMissingID
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("qdrant: decoding ScoredPoint.id as string: %w", err)
		}
		p.ID = s
	case '-', '0', '1', '2', '3', '4', '5', '6', '7', '8', '9':
		// json.Number preserves the exact decimal representation,
		// which matters for IDs that overflow int64 in scientific form.
		var n json.Number
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&n); err != nil {
			return fmt.Errorf("qdrant: decoding ScoredPoint.id as number: %w", err)
		}
		p.ID = n.String()
	default:
		return fmt.Errorf("qdrant: ScoredPoint.id must be a string or number, got %q", string(raw))
	}
	return nil
}
