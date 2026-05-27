package engine

import (
	"fmt"
	"sort"
	"strings"

	"github.com/baphled/flowstate/internal/tool"
)

// ValidationErrorClass classifies the failure mode of a ValidateToolArgs
// rejection so downstream telemetry (bus event tool.args.validation_failed,
// see internal/engine/engine.go's call site) can group failures without
// re-parsing the error message string.
//
// The class is also surfaced in the error message text — both the model and
// dashboards see the same categorical label.
type ValidationErrorClass string

const (
	// ValidationClassUnknownKeys marks failures where the args map contains
	// at least one key the tool's Schema.Properties does not declare. The
	// glm-4.6 `librarian` mis-call captured in the May 2026 codebase-explorer
	// investigation is the canonical instance.
	ValidationClassUnknownKeys ValidationErrorClass = "unknown_keys"

	// ValidationClassMissingRequired marks failures where every key is
	// known but Schema.Required names a key the args map does not carry.
	// Reported only when no unknown keys are present (unknown keys are the
	// more common failure mode and yield the most actionable feedback to
	// the model).
	ValidationClassMissingRequired ValidationErrorClass = "missing_required"

	// ValidationClassXMLBleed marks failures where the model has emitted
	// XML-like markers (`<arg_key>` / `</arg_key>`) into a string-valued
	// argument. Indicates provider-side serialiser corruption rather than
	// genuine schema confusion — the May 2026 glm-4.6 session captured
	// `message</arg_key>actual content` as a top-level value. The validator
	// does NOT strip the markers (Option C from the investigation is
	// explicitly rejected — masks real corruption / prompt injection); it
	// classifies so dashboards can attribute provider issues.
	//
	// XML bleed takes precedence over unknown_keys when both are present
	// because the bleed is the upstream cause; the unknown-key shape is
	// downstream of the serialiser corruption that produced it.
	ValidationClassXMLBleed ValidationErrorClass = "xml_bleed_detected"
)

// ValidationError describes a structured argument-validation failure. It
// implements the error interface so existing callers using fmt.Errorf-style
// patterns (errors.As, error string assertions) keep working, and exposes
// the typed Class plus key slices so the engine call site can stamp a
// tool.args.validation_failed bus event without parsing the message string.
//
// Expected: returned by ValidateToolArgs when args do not conform to schema.
// Returns: pointer used with errors.As to extract the typed fields.
// Side effects: none.
type ValidationError struct {
	// Class is the structured failure category (see ValidationErrorClass
	// constants).
	Class ValidationErrorClass
	// UnknownKeys lists every args key the schema does not declare. Sorted
	// ascending. Populated only when Class is ValidationClassUnknownKeys
	// or ValidationClassXMLBleed.
	UnknownKeys []string
	// MissingKeys lists every Schema.Required key absent from args. Sorted
	// ascending. Populated only when Class is ValidationClassMissingRequired.
	MissingKeys []string
	// ExpectedKeys lists every schema-declared property name. Sorted
	// ascending. Populated for every Class so the model receives the
	// schema-aware hint regardless of failure mode.
	ExpectedKeys []string
	// Message is the human-readable error text the model and slog lines see.
	Message string
}

// Error implements the error interface.
func (e *ValidationError) Error() string { return e.Message }

// ValidateToolArgs checks that args conform to the tool's schema. Unknown keys
// produce an error naming them so the model can self-correct on the next
// turn. Missing required keys also produce an error. The unknown-key check
// is reported first because it is the more common failure mode and yields
// the most actionable feedback to the model — a missing key is often a
// downstream consequence of an unknown key carrying the same payload under
// the wrong name.
//
// Recommendation B (May 2026 codebase-explorer investigation of the glm-4.6
// `librarian` mis-call): when unknown keys are present, the error message
// names the expected schema keys and — when exactly one unknown key is
// present — offers a "Did the value 'X' belong in '<expected_key>'?" hint
// pairing the unknown key's value with each expected key. Mirrors the
// skill-name redirect pattern at engine.go:4817-4834 and the fuzzy "Did
// you mean" suggestion at engine.go:4838-4848. Makes self-correction
// faster — the one observed turn becomes zero on the easy cases.
//
// Recommendation E: returns a *ValidationError carrying the structured
// failure class so the engine call site can stamp a tool.args.validation_failed
// bus event without re-parsing the message string. The error class is also
// embedded in the message text so model-side observers see the same label.
//
// Expected:
//   - schema is the tool's declared input schema. A schema with no Properties
//     opts out of validation entirely (the tool accepts anything).
//   - args is the map from the LLM's tool call.
//
// Returns:
//   - The args map unchanged on success (no key removal).
//   - nil on error.
//   - A *ValidationError naming the unknown keys when any are present.
//   - A *ValidationError naming the missing required keys when no unknown
//     keys are present and required keys are absent.
//
// Side effects:
//   - None. The args map is never mutated; callers see exactly the keys the
//     model produced. Earlier behaviour silently stripped unknown keys and
//     reported success — that masked hallucinated arguments and let the
//     model double down on them on subsequent turns. See the vault note
//     "MCP Manifest Gating Regression and Tool Arg Strip (April 2026)" for
//     the canonical reference; Option D (mid-turn repair) from the May 2026
//     investigation is also explicitly rejected — it literally reintroduces
//     the silent-strip bug 235d321 fixed.
func ValidateToolArgs(schema tool.Schema, args map[string]interface{}) (map[string]interface{}, error) {
	if len(schema.Properties) == 0 {
		return args, nil
	}

	expectedKeys := sortedSchemaKeys(schema)

	var unknown []string
	for key := range args {
		if _, known := schema.Properties[key]; !known {
			unknown = append(unknown, key)
		}
	}

	// XML bleed detection takes precedence over the unknown-keys path: the
	// bleed is the upstream cause and unknown-key entries are typically the
	// downstream symptom (the serialiser emitted `message</arg_key>...`
	// into a value, then the next key boundary landed inside what was meant
	// to be the value). Detect bleed by scanning every string value AND
	// every key (the May 2026 capture had `librarian` as a top-level key
	// with `message</arg_key>...` as its value) for the canonical markers.
	if hasXMLBleed(args) {
		sort.Strings(unknown)
		return nil, &ValidationError{
			Class:        ValidationClassXMLBleed,
			UnknownKeys:  unknown,
			ExpectedKeys: expectedKeys,
			Message:      buildBleedMessage(unknown, expectedKeys),
		}
	}

	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, &ValidationError{
			Class:        ValidationClassUnknownKeys,
			UnknownKeys:  unknown,
			ExpectedKeys: expectedKeys,
			Message:      buildUnknownKeysMessage(unknown, expectedKeys, args),
		}
	}

	var missing []string
	for _, req := range schema.Required {
		if _, ok := args[req]; !ok {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return args, &ValidationError{
			Class:        ValidationClassMissingRequired,
			MissingKeys:  missing,
			ExpectedKeys: expectedKeys,
			Message: fmt.Sprintf(
				"missing required arguments: %s. Expected: %s.",
				strings.Join(missing, ", "),
				strings.Join(expectedKeys, ", "),
			),
		}
	}

	return args, nil
}

// sortedSchemaKeys returns every Schema.Properties key in sorted order.
func sortedSchemaKeys(schema tool.Schema) []string {
	keys := make([]string, 0, len(schema.Properties))
	for k := range schema.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// hasXMLBleed returns true when any key OR any string-typed value carries
// the canonical `<arg_key>` / `</arg_key>` marker the May 2026 glm-4.6
// session captured. Conservative — only flags the exact substring, not
// arbitrary angle-bracketed text, so legitimate XML-bearing payloads
// (model output, code snippets quoted into a string argument) do not trip
// the classifier.
func hasXMLBleed(args map[string]interface{}) bool {
	for key, v := range args {
		if containsArgKeyMarker(key) {
			return true
		}
		if s, ok := v.(string); ok && containsArgKeyMarker(s) {
			return true
		}
	}
	return false
}

func containsArgKeyMarker(s string) bool {
	return strings.Contains(s, "<arg_key>") || strings.Contains(s, "</arg_key>")
}

// buildUnknownKeysMessage assembles the schema-aware hint. The base message
// names the unknown keys (preserves the canonical "unknown arguments:"
// substring downstream grep tooling and existing detectors rely on) and the
// expected keys. When exactly one unknown key is present we add a redirect
// hint pairing the unknown key's value with each expected key — the
// canonical recovery hint shape ("Did the value 'X' belong in 'Y'?"), and
// the case the glm-4.6 `librarian` capture matches.
func buildUnknownKeysMessage(unknown, expectedKeys []string, args map[string]interface{}) string {
	var b strings.Builder
	fmt.Fprintf(&b, "unknown arguments: %s. Expected: %s.",
		strings.Join(unknown, ", "), strings.Join(expectedKeys, ", "))

	if len(unknown) == 1 && len(expectedKeys) > 0 {
		// The model emitted a single mystery key. The historical pattern
		// captured in the investigation is "agent name as a key" — the
		// VALUE is what should have ended up under one of the expected
		// keys. We render the key itself (not the value, which is
		// frequently long free-form text) as the candidate payload so
		// the hint reads "Did the value 'librarian' belong in
		// 'subagent_type'?".
		fmt.Fprintf(&b, " Did the value '%s' belong in one of: %s?",
			unknown[0], strings.Join(expectedKeys, ", "))
	}
	return b.String()
}

func buildBleedMessage(unknown, expectedKeys []string) string {
	var b strings.Builder
	b.WriteString("invalid arguments: serialised tool-call contains XML markers (<arg_key> / </arg_key>) in keys or values")
	if len(unknown) > 0 {
		fmt.Fprintf(&b, "; unknown arguments: %s", strings.Join(unknown, ", "))
	}
	if len(expectedKeys) > 0 {
		fmt.Fprintf(&b, ". Expected: %s.", strings.Join(expectedKeys, ", "))
	} else {
		b.WriteString(".")
	}
	b.WriteString(" Re-emit the tool call without the markers — they indicate the provider serialiser bled the arg-key tag into the value.")
	return b.String()
}
