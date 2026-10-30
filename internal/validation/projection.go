package validation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalPayload reserializes the parsed payload as sorted-key JSON with no
// insignificant whitespace and returns its UTF-8 digest.
func CanonicalPayload(payload map[string]any) ([]byte, string) {
	canonical, err := json.Marshal(payload)
	if err != nil {
		return nil, ""
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, canonical); err != nil {
		return nil, ""
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// CompareProjection implements VA-E2: the canonical projection of the
// persisted events must equal the parsed stdout lines exactly, order
// included; any extra untyped final stdout object is a mismatch.
func CompareProjection(persisted []PersistedEvent, stdoutLines []string) []string {
	var violations []string
	if len(persisted) != len(stdoutLines) {
		violations = append(violations, fmt.Sprintf("stdout line count %d differs from persisted %d", len(stdoutLines), len(persisted)))
		return violations
	}
	for i, event := range persisted {
		expected, err := ProjectionLine(event)
		if err != nil {
			violations = append(violations, fmt.Sprintf("persisted event %d not projectable: %v", i, err))
			continue
		}
		if stdoutLines[i] != expected {
			violations = append(violations, fmt.Sprintf("stdout line %d differs from persisted projection", i))
		}
	}
	return violations
}

// ValidateEventLog checks VA-E1: persisted sequence contiguous from 0 and
// every per-payload digest matching the canonical payload bytes.
func ValidateEventLog(events []PersistedEvent) []string {
	var violations []string
	for i, event := range events {
		if event.Seq != int64(i) {
			violations = append(violations, fmt.Sprintf("event seq gap at index %d: got seq %d", i, event.Seq))
		}
		_, digest := CanonicalPayload(event.Payload)
		if digest == "" {
			violations = append(violations, fmt.Sprintf("event seq %d payload is not canonical JSON", event.Seq))
			continue
		}
		if digest != event.PayloadSHA256 {
			violations = append(violations, fmt.Sprintf("corrupt payload hash at seq %d", event.Seq))
		}
	}
	return violations
}

// ProjectionLine returns the canonical sorted-key JSON stdout projection of a
// single persisted event for VA-E2 comparison against captured stdout.
func ProjectionLine(event PersistedEvent) (string, error) {
	_, digest := CanonicalPayload(event.Payload)
	if digest == "" {
		return "", fmt.Errorf("payload not canonicalizable")
	}
	fields := map[string]any{
		"session_id":     event.SessionID,
		"seq":            event.Seq,
		"created_at_utc": event.CreatedAtUTC,
		"actor":          event.Actor,
		"type":           event.Type,
		"payload":        event.Payload,
		"payload_sha256": digest,
	}
	encoded, err := marshalSorted(fields)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func marshalSorted(value map[string]any) ([]byte, error) {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		keyJSON, err := json.Marshal(key)
		if err != nil {
			return nil, err
		}
		buf.Write(keyJSON)
		buf.WriteByte(':')
		valueJSON, err := json.Marshal(value[key])
		if err != nil {
			return nil, err
		}
		buf.Write(valueJSON)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
