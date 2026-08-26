package coordination

import (
	"strings"
)

// ApprovalCallback is invoked whenever a coordination_store key matching
// `<chainID>/review` is Set with a value containing "APPROVE".
// Implementations typically read `<chainID>/plan` from the underlying store
// and persist it to disk via plan.Store.Create — closing the loop where a
// reviewed plan ends up on disk even if the plan-writer agent forgot to
// call the plan_write tool itself.
//
// Expected:
//   - chainID is the prefix of the review key (i.e. the key with the
//     trailing "/review" stripped).
//   - store is the underlying coordination store the callback should read
//     the plan body from. The PersistingStore wrapper does not pre-fetch
//     the plan to keep the Set hot path narrow.
//
// Returns:
//   - None.
//
// Side effects:
//   - Implementation-defined; typically reads `<chainID>/plan` from the
//     store and writes a plan.File to disk.
type ApprovalCallback func(chainID string, store Store)

// PersistingStore wraps a Store and invokes an ApprovalCallback whenever
// a write to `<chainID>/review` carries a value containing "APPROVE".
// All other operations pass through unchanged.
//
// The wrapper exists because the planner-loop's "approved plan" event
// has no first-class signal: agents write to coord-store and the runner
// inspects the verdict text. Wrapping the store turns those writes into
// an observable event without forcing every agent to remember to call
// a separate tool. Acts as a belt-and-braces backup behind the
// agent-facing plan_write tool — a plan-writer agent that forgets to
// call plan_write still ends up with a persisted plan on disk because
// the post-review approval write fires the callback.
type PersistingStore struct {
	Store
	onApprove ApprovalCallback
}

// NewPersistingStore creates a PersistingStore wrapping inner.
//
// Expected:
//   - inner is a non-nil coordination.Store.
//   - cb may be nil. When nil, the wrapper is a transparent passthrough
//     — useful in tests that exercise the chain without app-level wiring.
//
// Returns:
//   - A *PersistingStore that delegates Get/List/Delete/Increment to inner
//     and intercepts Set to fire cb on approval-verdict writes.
//
// Side effects:
//   - None at construction.
func NewPersistingStore(inner Store, cb ApprovalCallback) *PersistingStore {
	return &PersistingStore{Store: inner, onApprove: cb}
}

// Set delegates to the inner store. After a successful write, if the key
// looks like a review verdict and the value contains "APPROVE", the
// approval callback fires asynchronously so a slow disk write inside the
// callback does not block the calling agent's tool call.
//
// Expected:
//   - key is a non-empty string in the canonical
//     `<chainID>/<keyname>` shape used throughout FlowState's
//     delegation chains.
//   - value is the byte slice to store.
//
// Returns:
//   - The error from the underlying Set call, or nil on success.
//
// Side effects:
//   - Persists the key/value pair via the inner store.
//   - May spawn a goroutine to invoke the approval callback.
func (p *PersistingStore) Set(key string, value []byte) error {
	if err := p.Store.Set(key, value); err != nil {
		return err
	}
	if p.onApprove != nil && isApprovalKey(key) && containsApprovalVerdict(value) {
		chainID := strings.TrimSuffix(key, "/review")
		// Async so a slow plan persistence does not stall the agent's
		// next tool call. The callback gets the underlying store
		// (Store, not p) to avoid recursion if it ever ends up
		// writing back to the coord-store itself.
		go p.onApprove(chainID, p.Store)
	}
	return nil
}

// isApprovalKey reports whether key matches the canonical review-verdict
// shape (`<chainID>/review`). A bare "review" key is not an approval
// signal — the chainID prefix is required so the callback can scope its
// follow-up work to the right chain.
//
// Expected:
//   - key is the coordination-store key being inspected.
//
// Returns:
//   - True when key has the form `<non-empty-prefix>/review`, false otherwise.
//
// Side effects:
//   - None.
func isApprovalKey(key string) bool {
	if !strings.HasSuffix(key, "/review") {
		return false
	}
	prefix := strings.TrimSuffix(key, "/review")
	return prefix != ""
}

// ApprovalVerdictToken is the verdict token the approve/reject loop keys
// on. The PersistingStore approval callback and app.App.PersistApprovedPlan
// both grep this exact substring (case-sensitive) to decide a plan is
// approved, so it is the single source of truth for "approved" — any gate
// or consumer that needs the same signal must reference this constant
// rather than re-spelling the literal.
const ApprovalVerdictToken = "APPROVE"

// RecognisedVerdictTokens is the full set of verdict tokens the planning-loop
// review surface uses. APPROVE / REJECT are the prose verbs the
// plan-reviewer agent actually emits ("VERDICT: APPROVE | REJECT"); REVISE /
// ABORT are the schema-contract aliases the review-verdict-v1 doc names.
// A review payload carrying ANY of these tokens is a recognised verdict;
// the swarm result-schema gate validates "did the reviewer emit a
// recognised verdict" against this set, which is the SAME family of signals
// the approve/reject loop keys on (ApprovalVerdictToken is the
// approved-specifically member). Centralised here so the gate
// (internal/swarm) and the persisting store cannot drift apart.
var RecognisedVerdictTokens = []string{
	ApprovalVerdictToken, // APPROVE
	"REJECT",
	"REVISE",
	"ABORT",
}

// ContainsRecognisedVerdict reports whether the review payload carries any
// recognised verdict token (case-sensitive). The swarm result-schema gate
// uses this to validate the plan-reviewer's output against the SAME signal
// the approve/reject loop reads, instead of a JSON `verdict` enum no Go
// consumer parses.
//
// Expected:
//   - value is the raw review-verdict payload written to the coordination store.
//
// Returns:
//   - True when value contains at least one RecognisedVerdictTokens entry.
//
// Side effects:
//   - None.
func ContainsRecognisedVerdict(value []byte) bool {
	s := string(value)
	for _, tok := range RecognisedVerdictTokens {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}

// containsApprovalVerdict reports whether the review payload contains
// the word "APPROVE" (case-sensitive). Matches the existing
// PersistApprovedPlan check on app.App so the two paths stay aligned.
//
// Expected:
//   - value is the raw review-verdict payload written to the coordination store.
//
// Returns:
//   - True when value contains the substring "APPROVE", false otherwise.
//
// Side effects:
//   - None.
func containsApprovalVerdict(value []byte) bool {
	return strings.Contains(string(value), ApprovalVerdictToken)
}
