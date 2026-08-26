package swarm

import (
	"strings"
	"testing"
)

// These are package-internal tests for the readable-filename helper
// (planFileName) and the plan-document validator (isPlanDocument). They
// assert directly against the unexported functions — the behavioural
// regression is also pinned through the public PublishPlanToVault and
// artifact-published gate paths in publish_test.go / gates_test.go.

// containsUnsafe reports whether name contains any filesystem-unsafe or
// control character — the property planFileName must always strip.
func containsUnsafe(name string) bool {
	for _, r := range name {
		if strings.ContainsRune(unsafeFileNameChars, r) || r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func TestPlanFileNamePreservesCaseAndSpaces(t *testing.T) {
	// THE FIX (memory: vault convention is readable Title Case with spaces):
	// a plan title must publish with its case + spaces intact, NOT as a
	// kebab-slug. The headline regression: the off-chain plan.
	title := "Off-Chain Write Rejection for Swarm Coordination Layer"

	got := planFileName(title, "", "off-chain-chain")

	if got != title {
		t.Fatalf("planFileName(%q) = %q, want the title verbatim", title, got)
	}
	if !strings.Contains(got, " ") {
		t.Fatalf("readable filename must contain spaces, got %q", got)
	}
	if got == strings.ToLower(got) {
		t.Fatalf("readable filename must preserve case (not all-lowercase), got %q", got)
	}
	if !strings.Contains(got, "-") {
		t.Fatalf("an internal hyphen must be preserved, got %q", got)
	}
	if containsUnsafe(got) {
		t.Fatalf("filename must not contain unsafe characters, got %q", got)
	}
}

func TestPlanFileNamePreservesVaultConventionGlyphs(t *testing.T) {
	// Parentheses and the "—" em-dash are valid in filenames and match the
	// user's vault naming; they must survive unchanged.
	cases := []string{
		"Permission Mode ModeAskUser Extension (May 2026)",
		"Mental Health Companion Swarm — Plan",
	}
	for _, title := range cases {
		got := planFileName(title, "", "fallback-chain")
		if got != title {
			t.Errorf("planFileName(%q) = %q, want the title verbatim", title, got)
		}
		if containsUnsafe(got) {
			t.Errorf("filename must not contain unsafe characters, got %q", got)
		}
	}
}

func TestPlanFileNameStripsUnsafeChars(t *testing.T) {
	// Path separators and reserved characters are removed (replaced by a
	// space then collapsed), and the result is safe + readable. "/readyz"
	// loses the slash but keeps the readable words around it.
	cases := map[string]string{
		"Add /readyz Readiness Endpoint": "Add readyz Readiness Endpoint",
		"Auth: Hardening Plan":           "Auth Hardening Plan",
		`Plan "Quoted" <Draft> | v2`:     "Plan Quoted Draft v2",
		`a/b\c:d*e?f"g<h>i|j`:            "a b c d e f g h i j",
	}
	for title, want := range cases {
		got := planFileName(title, "", "fallback-chain")
		if got != want {
			t.Errorf("planFileName(%q) = %q, want %q", title, got, want)
		}
		if containsUnsafe(got) {
			t.Errorf("planFileName(%q) left unsafe chars: %q", title, got)
		}
	}
}

func TestPlanFileNameCapsLongTitleOnWordBoundary(t *testing.T) {
	// A long title is capped at fileNameMaxLen on a WORD boundary — no
	// partial trailing word, no trailing punctuation/space, and the readable
	// lead words are kept.
	longTitle := "A Conversational Mental Health Companion Agent That Serves As The " +
		"Users Daily Entry Point For Managing AuDHD Specific Mental Health"

	got := planFileName(longTitle, "", "mhc-long")

	if len([]rune(got)) > fileNameMaxLen {
		t.Fatalf("filename length %d exceeds cap %d: %q", len([]rune(got)), fileNameMaxLen, got)
	}
	if strings.HasSuffix(got, " ") || strings.HasSuffix(got, "-") || strings.HasSuffix(got, ".") {
		t.Fatalf("filename has trailing punctuation/space: %q", got)
	}
	if !strings.HasPrefix(got, "A Conversational Mental Health") {
		t.Fatalf("filename should keep the readable lead words, got %q", got)
	}
	// The cap must land on a word boundary: every kept word must be a whole
	// word from the source title.
	sourceWords := map[string]bool{}
	for _, w := range strings.Fields(longTitle) {
		sourceWords[w] = true
	}
	for _, w := range strings.Fields(got) {
		if !sourceWords[w] {
			t.Fatalf("filename word %q is not a whole word from the title — cap cut mid-word: %q", w, got)
		}
	}
}

func TestPlanFileNameFallsBackToReadableChainID(t *testing.T) {
	// No usable title (and no H1): the filename is a READABLE form of the
	// chainID (hyphens → spaces, title-cased), NOT a raw kebab slug.
	got := planFileName("", "", "off-chain-write-rejection")

	want := "Off Chain Write Rejection"
	if got != want {
		t.Fatalf("planFileName fallback = %q, want %q", got, want)
	}
	if strings.Contains(got, "-") {
		t.Fatalf("fallback must not be a raw kebab slug, got %q", got)
	}
	if got == strings.ToLower(got) {
		t.Fatalf("fallback must be title-cased, got %q", got)
	}
}

func TestPlanFileNameIsIdempotent(t *testing.T) {
	// The same inputs must always produce the same filename so re-publishing
	// overwrites the same file rather than littering.
	cases := []struct{ title, body, chain string }{
		{"Off-Chain Write Rejection for Swarm Coordination Layer", "", "off-chain"},
		{"", "", "off-chain-write-rejection"},
		{
			"A Conversational Mental Health Companion Agent That Serves As The " +
				"Users Daily Entry Point For Managing AuDHD Specific Mental Health",
			"", "mhc-long",
		},
	}
	for _, c := range cases {
		first := planFileName(c.title, c.body, c.chain)
		second := planFileName(c.title, c.body, c.chain)
		if first != second {
			t.Fatalf("planFileName not idempotent for %+v: first %q != second %q", c, first, second)
		}
	}
}

// --- isPlanDocument (the plan-shape validator) ---

func TestIsPlanDocumentRejectsJSONSpecBlob(t *testing.T) {
	// THE INCIDENT, at the unit level: a JSON agent-spec object is NOT a
	// plan document. This is the high-confidence discriminator the publisher
	// and the honesty gate both rely on.
	jsonSpec := `{"purpose":"A companion.","responsibilities":["listen","signpost"],` +
		`"boundaries":{"must_not":["diagnose","prescribe"]}}`
	ok, reason := isPlanDocument(jsonSpec)
	if ok {
		t.Fatalf("a JSON agent-spec blob must NOT validate as a plan document")
	}
	if reason != reasonJSONSpecBlob {
		t.Fatalf("reason = %q, want the JSON-spec-blob reason", reason)
	}
}

func TestIsPlanDocumentRejectsEmpty(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\t \n"} {
		ok, reason := isPlanDocument(body)
		if ok {
			t.Fatalf("empty body %q must not validate", body)
		}
		if reason != reasonEmptyPlan {
			t.Fatalf("body %q: reason = %q, want empty-plan reason", body, reason)
		}
	}
}

func TestIsPlanDocumentRejectsHeadinglessProse(t *testing.T) {
	ok, reason := isPlanDocument("Just a paragraph of prose with no heading structure at all.")
	if ok {
		t.Fatalf("heading-less prose must not validate as a plan document")
	}
	if reason != reasonNoHeadings {
		t.Fatalf("reason = %q, want no-headings reason", reason)
	}
}

func TestIsPlanDocumentRejectsBareHeading(t *testing.T) {
	// A title line with no body beneath it is a stub, not a plan.
	for _, body := range []string{"# TBD", "## Section", "# Plan\n\n  \n"} {
		ok, reason := isPlanDocument(body)
		if ok {
			t.Fatalf("bare heading %q must not validate", body)
		}
		if reason != reasonHeadingOnly {
			t.Fatalf("body %q: reason = %q, want heading-only reason", body, reason)
		}
	}
}

func TestIsPlanDocumentAcceptsRealPlans(t *testing.T) {
	cases := []string{
		"# Plan\n\nbody",
		"# Auth Hardening Plan\n\n## Overview\n\nHarden the auth layer.",
		"## Section\n\nA plan body with sub-headings but no top-level H1 title.",
		"# Short\nx", // heading + minimal content is still a plan shape
	}
	for _, body := range cases {
		ok, reason := isPlanDocument(body)
		if !ok {
			t.Fatalf("real plan %q rejected with reason %q", body, reason)
		}
	}
}

func TestIsJSONObjectDiscriminates(t *testing.T) {
	objects := []string{
		`{"a":1}`,
		`  {"purpose":"x"}  `,
		`{}`,
	}
	for _, s := range objects {
		if !isJSONObject(s) {
			t.Errorf("isJSONObject(%q) = false, want true", s)
		}
	}
	notObjects := []string{
		"# A markdown plan",
		`["a","b"]`,  // JSON array, not an object
		`"a string"`, // JSON string, not an object
		"42",         // JSON number
		"# Plan\n\n{not really json",
	}
	for _, s := range notObjects {
		if isJSONObject(s) {
			t.Errorf("isJSONObject(%q) = true, want false", s)
		}
	}
}

func TestResolveValidationBodyUnwrapsEnvelope(t *testing.T) {
	// A {"markdown":...} envelope is unwrapped to its inner markdown so the
	// gate validates the SAME body the publisher writes.
	got := resolveValidationBody([]byte(`{"markdown":"# Real Plan\n\nbody","title":"Real Plan"}`))
	if got != "# Real Plan\n\nbody" {
		t.Fatalf("resolveValidationBody did not unwrap the envelope, got %q", got)
	}

	// A non-envelope value (a bare JSON object) is returned verbatim so
	// isPlanDocument then rejects it as a spec blob.
	raw := `{"purpose":"x"}`
	if got := resolveValidationBody([]byte(raw)); got != raw {
		t.Fatalf("resolveValidationBody mangled a non-envelope value: %q", got)
	}
}
