// Package qualify contains the T019 SQLite driver qualification spike: an
// isolated-database opener, forward-only versioned migrations, a single
// serialized event writer per session, a canonical JSON encoder, and a replay
// oracle used to verify sequence contiguity and payload digests.
//
// It is deliberately a qualification spike plus reusable tests, not the M1
// runtime.
package qualify

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// Canonical returns the canonical JSON encoding of v: object keys sorted
// lexicographically by UTF-8 bytes, no insignificant whitespace, and a fully
// defined Unicode policy. Go's encoding/json escapes <, >, & as \u003c,
// \u003e, \u0026, control characters as \uXXXX, and U+2028/U+2029 as
// \u2028/\u2029; all other valid UTF-8 is emitted as-is; invalid UTF-8 is
// replaced by U+FFFD. json.Number values are emitted verbatim, which fixes
// integer encoding (no exponent/fraction) and a documented float form. The
// output is deterministic byte-for-byte for equal values.
func Canonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(true)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	b := bytes.TrimRight(buf.Bytes(), "\n")
	return normalizeCanonical(b), nil
}

// normalizeCanonical re-encodes the value tree with recursively sorted object
// keys. encoding/json already emits map keys in sorted order; this also
// canonicalizes keys arriving via json.RawMessage or unkeyed input.
func normalizeCanonical(b []byte) []byte {
	var v any
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if err := d.Decode(&v); err != nil {
		return b
	}
	var out bytes.Buffer
	writeCanonical(&out, v)
	return out.Bytes()
}

func writeCanonical(buf *bytes.Buffer, v any) {
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
			kb, _ := json.Marshal(k)
			buf.Write(kb)
			buf.WriteByte(':')
			writeCanonical(buf, t[k])
		}
		buf.WriteByte('}')
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeCanonical(buf, e)
		}
		buf.WriteByte(']')
	default:
		var sub bytes.Buffer
		enc := json.NewEncoder(&sub)
		enc.SetEscapeHTML(true)
		if err := enc.Encode(t); err != nil {
			buf.WriteString("null")
			return
		}
		buf.Write(bytes.TrimRight(sub.Bytes(), "\n"))
	}
}

// Digest returns the lowercase hex SHA-256 of the canonical bytes b.
func Digest(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// VerifyDigest reports whether sha256(canonical JSON of v) equals want.
func VerifyDigest(v any, want string) error {
	b, err := Canonical(v)
	if err != nil {
		return err
	}
	if got := Digest(b); got != want {
		return fmt.Errorf("payload digest mismatch: got %s want %s", got, want)
	}
	return nil
}
