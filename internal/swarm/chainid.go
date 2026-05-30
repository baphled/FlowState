package swarm

import (
	"context"
	"strings"
)

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

// memberOutputSuffixes is the canonical, tail-anchored set of
// coordination-store key suffixes a planning swarm member (or the lead's
// own bookkeeping) writes under the run's chainID. Every entry mirrors a
// "{chainID}/<suffix>" key advertised by an agent prompt or a gate
// template:
//
//   - codebase-findings — explorer (evidence-bundle-v1).
//   - external-refs      — librarian (external-refs-v1).
//   - analysis           — analyst (analysis-bundle-v1).
//   - plan               — plan-writer (plan-document-v1).
//   - review             — plan-reviewer (review-verdict-v1).
//   - requirements       — lead bookkeeping (the original request).
//   - interview          — lead bookkeeping (the interview log).
//   - sections/          — SME sub-swarm section members write the
//     MULTI-segment "{chainID}/sections/<name>" key (section-v1); the
//     trailing slash marks it as a prefix so any "<name>" survives.
//
// The list lives in the swarm package — the same package that owns
// SlugifyChainID and the schema-name constants — because it is swarm-
// domain knowledge, not a property of the generic coordination tool. The
// tool delegates to NormaliseMemberCoordKey rather than embedding the
// vocabulary itself.
var memberOutputSuffixes = []string{
	"sections/", // multi-segment prefix; checked first so "sections/x" wins
	"codebase-findings",
	"external-refs",
	"requirements",
	"interview",
	"analysis",
	"review",
	"plan",
}

// NormaliseMemberCoordKey rewrites a coordination-store WRITE key so its
// chainID-prefix segment is the engine-authoritative chainID, leaving the
// semantic suffix intact. It is the WRITE-side counterpart of the gate's
// READ-side key resolution (gate_result_schema.go candidateKeys): the gate
// looks under "{chainID}/<suffix>" with chainID = SlugifyChainID(ChainPrefix)
// when the engine owns the chainID, so a member write the lead labelled
// "planning/planner/codebase-findings" MUST land at
// "{authoritativeChainID}/codebase-findings" — exactly where the gate reads.
//
// The recurring planning-loop doom-loop (live session 836526fd) was the
// lead free-forming a SLASHED chainID ("planning/planner") in its delegate
// brief; the member faithfully wrote "planning/planner/codebase-findings"
// while the gate resolved "planning-loop-<hash>/codebase-findings" and saw
// an empty store. Cooperation-based fixes (asking the lead to use the
// engine chainID) repeatedly failed because the model free-forms anyway.
// Normalising at the write boundary makes the lead/member free-form
// irrelevant: the engine, not the LLM, owns the key's chainID prefix.
//
// Resolution:
//
//  1. authoritativeChainID is empty → the key is returned unchanged (the
//     caller is not inside an engine-owned swarm; no authority to rewrite).
//  2. The key's TAIL matches a known member-output suffix (longest /
//     multi-segment match first) → return
//     "<authoritativeChainID>/<matchedSuffix>". This recovers the suffix
//     even when the lead used a multi-segment slashed prefix, because the
//     match is anchored at the tail, not at the first separator.
//  3. No known suffix matches → fall back to first-segment replacement: a
//     key with a "/" has its first segment swapped for the authoritative
//     chainID ("bogus/x" → "<chainID>/x"); a bare key with no "/" is
//     prefixed ("x" → "<chainID>/x"). This degrades safely for ad-hoc
//     keys the suffix vocabulary does not enumerate.
//
// A key already correctly prefixed with the authoritative chainID is a
// no-op under either branch (the suffix match re-emits the same prefix; the
// first-segment swap replaces an identical segment).
//
// Expected:
//   - authoritativeChainID is SlugifyChainID(swarmCtx.ChainPrefix) when the
//     engine owns the chainID, or "" otherwise.
//   - key is the raw coordination_store key the member supplied.
//
// Returns:
//   - The normalised key (or the original when authoritativeChainID == "").
//
// Side effects:
//   - None.
func NormaliseMemberCoordKey(authoritativeChainID, key string) string {
	if authoritativeChainID == "" || key == "" {
		return key
	}
	for _, suffix := range memberOutputSuffixes {
		if strings.HasSuffix(suffix, "/") {
			// Multi-segment prefix (e.g. "sections/"): match anywhere the
			// "/sections/" boundary appears, or when the whole key starts
			// with it, and re-anchor the matched tail under the chainID.
			if idx := strings.Index(key, "/"+suffix); idx >= 0 {
				return authoritativeChainID + "/" + key[idx+1:]
			}
			if strings.HasPrefix(key, suffix) {
				return authoritativeChainID + "/" + key
			}
			continue
		}
		// Single-segment suffix: the member key ends in "/<suffix>" (drifted
		// prefix) or IS the bare suffix (no prefix at all).
		if key == suffix {
			return authoritativeChainID + "/" + suffix
		}
		if strings.HasSuffix(key, "/"+suffix) {
			return authoritativeChainID + "/" + suffix
		}
	}
	// Unknown suffix: replace the first segment (or prefix a bare key).
	if idx := strings.Index(key, "/"); idx >= 0 {
		return authoritativeChainID + "/" + key[idx+1:]
	}
	return authoritativeChainID + "/" + key
}

// MemberCoordChainID returns the engine-authoritative chainID a member's
// coordination-store WRITE must be namespaced under, derived from the
// per-turn swarm scope on ctx, or "" when the turn is not inside an
// engine-owned swarm (so the caller leaves the key untouched).
//
// The "owned" condition mirrors resolveSwarmChainNamespace's engine-
// authority branch (delegation.go): the engine owns the chainID exactly
// when the dispatcher stamped a per-run id at swarm start
// (ChainIDAssigned && ChainPrefix != ""). In that case the authoritative
// value is SlugifyChainID(ChainPrefix) — the SAME value the post-member
// gate resolves — so write-side and read-side are guaranteed identical.
//
// The scope is read from ScopeFromContext (the dispatcher attaches it on
// every dispatch path via WithScope), so a member running on its own
// per-agent engine — whose engine.swarmContext field is nil — still sees
// the authoritative chainID off ctx.
//
// Expected:
//   - ctx may carry a per-turn swarm scope (WithScope); a nil or absent
//     scope yields "".
//
// Returns:
//   - SlugifyChainID(ChainPrefix) when the engine owns the chainID; ""
//     otherwise.
//
// Side effects:
//   - None.
func MemberCoordChainID(ctx context.Context) string {
	sc, scoped := ScopeFromContext(ctx)
	if !scoped || sc == nil {
		return ""
	}
	if !sc.ChainIDAssigned || sc.ChainPrefix == "" {
		return ""
	}
	return SlugifyChainID(sc.ChainPrefix)
}
