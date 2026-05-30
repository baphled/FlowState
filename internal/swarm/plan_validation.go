package swarm

import (
	"encoding/json"
	"errors"
	"strings"
)

// planValidationReason explains, in actionable terms, why a body is NOT a
// publishable plan document. It is surfaced verbatim by the honesty gate so
// the loop honest-fails with a reason the lead (and the user) can act on.
type planValidationReason string

const (
	// reasonEmptyPlan: the body is empty or whitespace-only.
	reasonEmptyPlan planValidationReason = "plan artifact is empty — there is nothing to publish"

	// reasonJSONSpecBlob: the body is a raw JSON object (an agent spec or
	// other structured blob), NOT a markdown plan document. This is THE
	// incident: the canonical "<chainID>/plan" key held a JSON agent-spec
	// object ({"purpose":..,"responsibilities":[..],"boundaries":{..}}) and
	// the old publisher rendered it to 200 lines of unusable markdown.
	reasonJSONSpecBlob planValidationReason = "plan artifact is a JSON spec object, not a plan document " +
		"(an OMO markdown plan with heading structure was expected; a structured agent/spec blob is not publishable)"

	// reasonNoHeadings: the body is non-JSON text but carries no markdown
	// heading structure — a coherent plan document has at least one ATX
	// heading (its title / section headers).
	reasonNoHeadings planValidationReason = "plan artifact has no markdown heading structure " +
		"(a plan document needs at least one '#' heading; the body looks like prose or data, not a plan)"

	// reasonHeadingOnly: the body is a bare heading with no content beneath
	// it — a title line alone ("# TBD") is a stub, not a plan.
	reasonHeadingOnly planValidationReason = "plan artifact is a bare heading with no content — a plan needs a body, not just a title"
)

// ValidatePlanDocumentBody is the gate-side predicate for the
// `plan-document-v1` member gate (post-member-plan-writer-plan-document). It
// returns nil iff payload is a body the post-swarm publisher can render into a
// non-empty plan document, and an error naming the actionable reason
// otherwise.
//
// It is the SINGLE SOURCE OF TRUTH the result-schema runner consults for
// plan-document-v1 instead of the raw JSON schema: by resolving the body
// through the SAME resolveValidationBody + isPlanDocument path the publisher
// (parsePlan / renderStructuredPlan) and the post-swarm honesty gate
// (gate_artifact_published.go) use, member-gate ACCEPTANCE is equivalent to
// publisher RENDERABILITY by construction — they cannot drift. This closes the
// incoherence where the tightened JSON schema rejected the structured
// `content`-object body AT THE MEMBER GATE even though renderStructuredPlan can
// turn that exact body into a coherent plan at publish time.
//
// Accepted shapes (whatever resolveValidationBody can turn into a plan):
//   - a {"markdown": "..."} / {"plan": "..."} envelope carrying a real
//     markdown plan body (the plan-writer happy path);
//   - the structured {title, content:{executive_summary, phased_slices:[...]}}
//     shape carrying real prose (renderStructuredPlan salvages it).
//
// Rejected shapes (the incident intent preserved):
//   - a contentless / metadata-only JSON blob ({"id":"x"}), an empty body,
//     a heading-less prose dump, a bare-heading stub, or a structured shape
//     with no renderable prose — none of which the publisher would write.
//
// Expected:
//   - payload is the raw coord-store "<chainID>/plan" value the member wrote;
//     it has already passed JSON-validity decoding at the runner.
//
// Returns:
//   - nil when the body renders to a non-empty plan document.
//   - An error carrying the planValidationReason otherwise.
//
// Side effects:
//   - None.
func ValidatePlanDocumentBody(payload []byte) error {
	body := resolveValidationBody(payload)
	if ok, reason := isPlanDocument(body); !ok {
		return errors.New(string(reason))
	}
	return nil
}

// isPlanDocument reports whether body is a publishable plan document and, when
// it is not, an actionable reason why.
//
// A body qualifies as a plan document when ALL hold:
//   - it is non-empty (after trimming);
//   - it is NOT a raw JSON object — a JSON agent-spec / structured blob is
//     the incident this guard exists to catch, and rendering it to markdown
//     (the old behaviour) produced garbage in the vault;
//   - it carries markdown heading structure (at least one ATX "# "/"## "...
//     heading) — a plan's title and sections are headings;
//   - it has content beneath the heading (a bare title line is a stub).
//
// This is DETERMINISTIC: it makes no judgement about plan QUALITY (whether
// the prose is good), only about plan SHAPE (markdown-with-headings-and-body
// vs a JSON blob / empty / heading-only stub). Plan quality stays
// model-dependent; plan shape is now code-enforced so garbage can never
// silently reach the vault. No arbitrary character floor is imposed — a
// short-but-structured plan is valid; the high-confidence discriminators are
// "is it a JSON object" (the actual incident signature) and "does it have a
// heading with a body".
//
// The {"markdown": "..."} envelope is NOT handled here — callers unwrap the
// envelope to its Markdown body BEFORE validating, so a valid envelope is
// validated on its inner markdown (which must itself have heading structure).
func isPlanDocument(body string) (ok bool, reason planValidationReason) {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return false, reasonEmptyPlan
	}
	if isJSONObject(trimmed) {
		return false, reasonJSONSpecBlob
	}
	if !hasMarkdownHeading(trimmed) {
		return false, reasonNoHeadings
	}
	if !hasContentBeyondHeadings(trimmed) {
		return false, reasonHeadingOnly
	}
	return true, ""
}

// hasContentBeyondHeadings reports whether body has at least one non-blank,
// non-heading line — i.e. there is plan CONTENT below the title/section
// headers, not just a bare heading. "# TBD" alone is a stub; "# Plan\n\nbody"
// has content.
func hasContentBeyondHeadings(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isATXHeading(trimmed) {
			continue
		}
		return true
	}
	return false
}

// resolveValidationBody returns the markdown body the publisher WOULD write
// for a raw coord-store "<chainID>/plan" value, so the honesty gate validates
// the SAME body the publisher produces rather than the raw envelope.
//
// Mirroring parsePlan's precedence: a {"markdown": "..."} envelope is
// unwrapped to its inner markdown (which must itself pass plan-document
// validation); a structured `content`-object body carrying real plan prose is
// RENDERED to the same markdown the publisher would write (so the gate
// validates the salvaged document, not the raw JSON); any other value (a
// contentless JSON blob / raw text) is validated verbatim. This keeps the
// gate's verdict consistent with the publisher's behaviour — a valid
// envelope-wrapped plan and a salvageable structured plan pass both; a
// contentless JSON spec blob fails both.
func resolveValidationBody(raw []byte) string {
	var env planEnvelope
	if err := json.Unmarshal(raw, &env); err == nil {
		if md := env.body(); md != "" {
			return md
		}
	}
	if _, md, ok := renderStructuredPlan(raw); ok {
		return md
	}
	return string(raw)
}

// isJSONObject reports whether s is a JSON object literal ("{...}"). It is
// the discriminator between a markdown plan and a structured-JSON spec blob.
// Only objects are flagged — a markdown body that merely CONTAINS a fenced
// JSON code block is not itself a JSON object, so it is not caught here.
//
// The cheap structural check (leading '{') gates the more expensive
// json.Unmarshal so a long markdown body is rejected as "not JSON" in O(1).
func isJSONObject(s string) bool {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "{") {
		return false
	}
	var obj map[string]json.RawMessage
	return json.Unmarshal([]byte(trimmed), &obj) == nil
}

// hasMarkdownHeading reports whether body contains at least one ATX markdown
// heading ("#", "##", ... up to "######") at the start of any line. A heading
// is "# Title" — one to six '#' followed by a space and text. A bare "#" with
// no following space/text (e.g. a C-style comment or a "#tag") does not count.
func hasMarkdownHeading(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if isATXHeading(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

// isATXHeading reports whether a (already-trimmed) line is an ATX heading:
// one to six leading '#' characters followed by a space and at least one
// non-space character of heading text.
func isATXHeading(line string) bool {
	hashes := 0
	for _, r := range line {
		if r == '#' {
			hashes++
			continue
		}
		break
	}
	if hashes < 1 || hashes > 6 || hashes >= len(line) {
		return false
	}
	rest := line[hashes:]
	if !strings.HasPrefix(rest, " ") {
		return false
	}
	return strings.TrimSpace(rest) != ""
}
