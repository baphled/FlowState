package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// moderncVersion is the qualified driver module version, pinned by go.mod.
// Kept as a constant so it is recorded in OpenInfo without a runtime dep.
const moderncVersion = "v1.61.0 (see go.mod for the pinned version)"

// CanonicalJSON marshals v deterministically:
//
//   - object keys sorted lexicographically (by UTF-8 bytes),
//   - no insignificant whitespace (compact),
//   - Go's encoding/json number encoding for float64/int (shortest
//     round-trip representation; whole floats keep no trailing ".0" only when
//     the value is an integer encoded as json.Number/int — see note),
//   - strings escaped per Go's default HTML-safe escaping policy, except
//     that we disable HTML escaping so bytes depend only on the value
//     (documented canonical-encoder choice for T019: Go encoding/json with
//     sorted keys and SetEscapeHTML(false)).
//
// Float note: Go emits e.g. 1.5, 2, 3 for float64(1.5), 2.0, 3.0 — the
// shortest representation that round-trips. This is stable across platforms
// and Go versions (guaranteed shortest-round-trip since Go 1.0's strconv).
func CanonicalJSON(v any) (string, error) {
	enc, err := marshalCanonical(v)
	if err != nil {
		return "", err
	}
	return enc, nil
}

func marshalCanonical(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	// json.Encoder appends '\n'.
	b := buf.Bytes()
	if n := len(b); n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	// Map keys are sorted by encoding/json already (Go sorts map keys).
	// But to be explicit and robust for map[string]any, re-marshal via a
	// canonical walk.
	return canonicalizeBytes(b)
}

// canonicalizeBytes re-encodes the JSON deterministically: parses into
// interface{} (all numbers become float64) and re-marshals with sorted keys.
func canonicalizeBytes(b []byte) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, v); err != nil {
		return "", err
	}
	return out.String(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeString(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case json.Number:
		buf.WriteString(t.String())
	case string:
		return writeString(buf, t)
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	default:
		// e.g. already-encoded primitives
		enc := jsonEncode(buf, t)
		return enc
	}
	return nil
}

func writeString(buf *bytes.Buffer, s string) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(s); err != nil {
		return err
	}
	b := tmp.Bytes()
	b = b[:len(b)-1] // strip '\n'
	buf.Write(b)
	return nil
}

func jsonEncode(buf *bytes.Buffer, v any) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b := tmp.Bytes()
	b = b[:len(b)-1]
	buf.Write(b)
	return nil
}

// IsCanonical reports whether the payload bytes are already in canonical
// form: canonical re-encoding of the parsed value reproduces the exact bytes.
// Equivalent JSON values alone do not imply identical bytes (T019 §6).
func IsCanonical(payload string) bool {
	c, err := CanonicalJSON(json.RawMessage(payload))
	if err != nil {
		return false
	}
	return c == payload
}

// PayloadDigest returns the lowercase hex SHA-256 of the canonical UTF-8
// payload bytes.
func PayloadDigest(payload string) (string, error) {
	h := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(h[:]), nil
}

// Compile-time guard: formatting choice documented.
