package swarm

import "strings"

// chainIDSeparator is the single character a coordination-store key uses
// to separate the chain-namespace prefix from the per-role suffix:
// "<chainID>/<suffix>". A chainID that itself contains this character
// breaks the "split on the FIRST '/'" parsing every coord-store reader
// relies on (the publisher's scanForSuffix, the wave validator's
// suffix-scan, the gate key resolver's joinKey). SlugifyChainID removes
// it so a chainID can NEVER fracture key parsing.
const chainIDSeparator = "/"

// SlugifyChainID normalises a chainID into a key-safe slug so it can never
// break "<chainID>/<suffix>" coordination-store key parsing. It is the
// single boundary every chainID — engine-assigned, caller-supplied, or
// LLM-supplied — passes through before it becomes a coord-store namespace.
//
// The recurring planning-loop failure was an LLM free-forming a chainID
// WITH A SLASH (e.g. "planner/sme-sectional-plans"): members wrote
// evidence under "planner/sme-sectional-plans/codebase-findings" (THREE
// path segments) while the wave validator, publisher and gate split on
// the first "/" and looked under "planner/...", never finding the
// deliverable. Slugifying at the resolution boundary closes that class:
// the value is rendered key-safe regardless of what the LLM emits.
//
// The rule is friendlier than rejection — it slugifies rather than
// erroring so a near-miss value still resolves to a usable namespace:
//
//   - path separators ("/" and "\") collapse to "-";
//   - whitespace runs collapse to a single "-";
//   - any other character outside [A-Za-z0-9._-] is dropped;
//   - leading/trailing "-" and "." are trimmed (a leading "." would
//     otherwise produce a dotfile-style key segment).
//
// The engine-assigned "{swarmID}-{hash}" form (AssignRunChainID) is
// already within this alphabet, so slugifying it is a no-op — the
// authoritative value survives unchanged. An empty or all-unsafe input
// yields "" so callers keep their existing "empty chainID → suffix-scan
// fallback" behaviour rather than inventing a placeholder.
//
// Expected:
//   - raw is any chainID candidate; empty is permitted (returns "").
//
// Returns:
//   - A key-safe slug, or "" when nothing safe remains.
//
// Side effects:
//   - None.
func SlugifyChainID(raw string) string {
	if raw == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(raw))
	prevDash := false
	for _, r := range raw {
		switch {
		case r == '/' || r == '\\':
			// Path separators are the dangerous case: a chainID with a
			// "/" fractures coord-store key parsing. Collapse to "-".
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-':
			b.WriteRune(r)
			prevDash = r == '-'
		default:
			// Any other rune (punctuation, symbols, control chars) is
			// dropped without leaving a separator so "a@b" → "ab".
			prevDash = false
		}
	}
	// Trim leading/trailing separators and dots so the slug never starts
	// or ends with "-" or "." (a leading "." would be a dotfile-style key
	// segment; a trailing "-" is cosmetic noise).
	return strings.Trim(b.String(), "-.")
}
