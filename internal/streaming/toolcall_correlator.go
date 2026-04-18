package streaming

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"sync"
)

// ToolCallCorrelator assigns a stable FlowState-internal identifier to every
// logical tool call observed on a streaming path and reuses that identifier
// on subsequent lookups. It is the translation layer that lets downstream
// consumers (activity pane coalescer, Ctrl+E modal, event persistence)
// correlate tool_call and tool_result events that cross a provider
// boundary — the failover case where provider A minted "toolu_01abc" and
// provider B later references the same logical call as "call_xyz123"
// because each provider lives in its own opaque ID space.
//
// The correlator uses two resolution strategies in order:
//
//  1. Direct provider-scoped ID match. The first time a provider-scoped id
//     is seen in a session it is registered against a newly minted internal
//     id. Subsequent sightings of the same provider-scoped id in the same
//     session return the registered internal id without re-hashing.
//
//  2. Fuzzy match on (tool_name, arguments-fingerprint). When a provider
//     replays history under its own ID scheme — typical for an OpenAI-family
//     provider inheriting an Anthropic tool_use block — the provider-scoped
//     id is new to the registry but the (name, args) pair matches an
//     already-registered call. Hitting this branch reuses the existing
//     internal id and cross-registers the new provider-scoped id so future
//     direct lookups short-circuit.
//
// Registries are per-session: cross-session ID reuse is impossible because
// arguments are namespaced by sessionID before hashing. This is the
// primary isolation guarantee that P5's session-isolation invariants rely
// on.
//
// Safe for concurrent use by the multiple stream-worker goroutines that
// share the engine.
type ToolCallCorrelator struct {
	mu sync.Mutex
	// directByProviderID maps "sessionID|providerID" to the internal id that
	// was minted or matched on first sight. Keyed this way rather than using
	// a nested map because the direct-lookup path is the hot path — one map
	// hit instead of two.
	directByProviderID map[string]string
	// fuzzyByFingerprint maps "sessionID|tool_name|args-fingerprint" to an
	// already-minted internal id. Populated alongside directByProviderID so
	// a later lookup with a different provider-scoped id but the same
	// (name, args) resolves to the same internal id.
	fuzzyByFingerprint map[string]string
}

// NewToolCallCorrelator constructs an empty ToolCallCorrelator ready for use.
//
// Returns:
//   - A pointer to an initialised ToolCallCorrelator.
//
// Side effects:
//   - None.
func NewToolCallCorrelator() *ToolCallCorrelator {
	return &ToolCallCorrelator{
		directByProviderID: make(map[string]string),
		fuzzyByFingerprint: make(map[string]string),
	}
}

// InternalID returns the stable FlowState-internal identifier for the tool
// call described by (providerID, toolName, args) within the given session.
// A fresh internal id is minted on first sight; repeated lookups for the
// same provider-scoped id — or a different provider-scoped id with the
// same (toolName, args) fingerprint — return the same internal id so
// downstream coalescing pairs the call with its result even across
// failover boundaries.
//
// Expected:
//   - sessionID identifies the session whose registry the lookup targets.
//     Empty sessionID is tolerated and treated as its own registry; callers
//     that care about cross-session isolation must pass a non-empty value.
//   - providerID is the upstream provider's tool-use identifier (Anthropic
//     block.ID, OpenAI tool_calls[].id). An empty providerID returns "".
//   - toolName names the tool being invoked and gates fuzzy matching.
//   - args is the tool-call argument map. Map iteration order does not
//     affect the fingerprint because the implementation sorts keys before
//     hashing.
//
// Returns:
//   - The stable internal id for this tool call, or the empty string when
//     providerID is empty.
//
// Side effects:
//   - Registers the (sessionID, providerID) → internal-id mapping on first
//     sight so future direct lookups are O(1).
//   - Registers the (sessionID, toolName, args-fingerprint) → internal-id
//     mapping so later sightings from a different provider under its own
//     id scheme resolve back to the same internal id.
func (c *ToolCallCorrelator) InternalID(sessionID, providerID, toolName string, args map[string]any) string {
	if providerID == "" {
		return ""
	}

	directKey := sessionID + "|" + providerID
	fuzzyKey := sessionID + "|" + toolName + "|" + argsFingerprint(args)

	c.mu.Lock()
	defer c.mu.Unlock()

	if id, ok := c.directByProviderID[directKey]; ok {
		return id
	}
	if id, ok := c.fuzzyByFingerprint[fuzzyKey]; ok {
		// Cross-register so subsequent direct lookups under this
		// provider-scoped id short-circuit instead of re-hashing args.
		c.directByProviderID[directKey] = id
		return id
	}

	id := mintInternalID(sessionID, providerID, toolName)
	c.directByProviderID[directKey] = id
	c.fuzzyByFingerprint[fuzzyKey] = id
	return id
}

// ForgetSession removes every registry entry owned by sessionID so the
// correlator does not grow unbounded across a long-running process. Callers
// wire this into session teardown (session end, session delete).
//
// Expected:
//   - sessionID identifies the session whose entries must be released.
//     A sessionID with no registered entries is a no-op.
//
// Side effects:
//   - Deletes direct and fuzzy map entries whose key begins with
//     "sessionID|".
func (c *ToolCallCorrelator) ForgetSession(sessionID string) {
	if sessionID == "" {
		return
	}
	prefix := sessionID + "|"

	c.mu.Lock()
	defer c.mu.Unlock()

	for k := range c.directByProviderID {
		if hasPrefix(k, prefix) {
			delete(c.directByProviderID, k)
		}
	}
	for k := range c.fuzzyByFingerprint {
		if hasPrefix(k, prefix) {
			delete(c.fuzzyByFingerprint, k)
		}
	}
}

// argsFingerprint returns a stable, order-independent fingerprint of the
// tool-call arguments suitable for fuzzy matching across providers.
//
// The fingerprint sorts keys lexicographically and JSON-encodes each
// value before hashing so maps whose Go iteration order differs still
// produce identical fingerprints. Values are JSON-encoded rather than
// %v-formatted so nested maps, slices, and numeric types round-trip
// canonically — %v on a map[string]any is iteration-order dependent and
// would defeat fuzzy matching.
//
// Expected:
//   - args may be nil, empty, or arbitrarily deep. Nil and empty produce
//     distinct-but-stable fingerprints so calls with no arguments still
//     match each other.
//
// Returns:
//   - A 32-character hex prefix of the SHA-256 digest of the canonical
//     serialisation.
//
// Side effects:
//   - None.
func argsFingerprint(args map[string]any) string {
	if len(args) == 0 {
		return "empty"
	}

	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		// Errors from json.Marshal on a map[string]any carrying only
		// wire-safe values are not expected — tool arguments come from
		// the provider's own JSON stream. A fallback to %v keeps the
		// fingerprint deterministic for pathological shapes.
		b, err := json.Marshal(args[k])
		if err != nil {
			b = []byte(strconv.Quote(fallbackFormat(args[k])))
		}
		h.Write(b)
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return hex.EncodeToString(sum)[:32]
}

// mintInternalID generates a fresh internal id for a previously unseen
// logical tool call. The id is derived deterministically from the session
// and the provider-scoped id so repeated mints for the same triple
// produce the same value — useful for replaying session logs and for
// golden-file tests. The "fs_" prefix disambiguates FlowState-internal
// ids from provider-scoped ids ("toolu_" / "call_") at a glance in logs.
//
// Expected:
//   - sessionID may be empty; providerID must be non-empty (the caller
//     enforces this).
//   - toolName is included in the hash so the incredibly unlikely
//     collision between different tools that happen to share a
//     sessionID+providerID is eliminated.
//
// Returns:
//   - A FlowState-internal id with the form "fs_<32 hex chars>".
//
// Side effects:
//   - None.
func mintInternalID(sessionID, providerID, toolName string) string {
	h := sha256.New()
	h.Write([]byte(sessionID))
	h.Write([]byte{0})
	h.Write([]byte(providerID))
	h.Write([]byte{0})
	h.Write([]byte(toolName))
	sum := h.Sum(nil)
	return "fs_" + hex.EncodeToString(sum)[:32]
}

// hasPrefix reports whether s starts with prefix. Implemented locally to
// avoid a strings import churn on this hot-but-short path.
//
// Expected:
//   - s and prefix are arbitrary strings.
//
// Returns:
//   - true when s begins with prefix.
//
// Side effects:
//   - None.
func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// fallbackFormat returns a string representation of v when json.Marshal
// fails. This is strictly defensive — tool arguments are provider-emitted
// JSON and should always marshal.
//
// Expected:
//   - v may be any value.
//
// Returns:
//   - A best-effort string representation.
//
// Side effects:
//   - None.
func fallbackFormat(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	default:
		b, err := json.Marshal(struct{}{})
		if err != nil {
			return "unmarshallable"
		}
		return string(b)
	}
}
