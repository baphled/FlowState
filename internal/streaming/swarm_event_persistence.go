package streaming

import (
	"bufio"
	"encoding/json"
	"io"
)

// WriteEventsJSONL serialises events to the writer as JSON Lines (one JSON
// object per line). Timestamps are encoded in RFC3339 format by the standard
// encoding/json marshaller (time.Time implements json.Marshaler). An empty
// slice produces no output and no error.
//
// Expected:
//   - w is a non-nil io.Writer.
//   - events may be nil or empty (produces no output).
//
// Returns:
//   - An error if any event cannot be marshalled or if a write fails.
//
// Side effects:
//   - Writes to w.
func WriteEventsJSONL(w io.Writer, events []SwarmEvent) error {
	for i := range events {
		data, err := json.Marshal(&events[i])
		if err != nil {
			return err
		}
		data = append(data, '\n')
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return nil
}

// ReadEventsJSONL reads JSON Lines from the reader and returns the parsed
// SwarmEvent entries. Lines that fail to unmarshal are silently skipped so
// that a single corrupt line does not discard the entire timeline.
//
// Expected:
//   - r is a non-nil io.Reader producing JSON Lines content (one JSON object
//     per line).
//
// Returns:
//   - A slice of successfully parsed SwarmEvent entries (may be empty).
//   - An error only if the underlying reader fails for a reason other than
//     EOF.
//
// Side effects:
//   - Reads from r until EOF.
func ReadEventsJSONL(r io.Reader) ([]SwarmEvent, error) {
	var events []SwarmEvent
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var ev SwarmEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// Skip corrupted lines gracefully.
			continue
		}
		events = append(events, ev)
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}
