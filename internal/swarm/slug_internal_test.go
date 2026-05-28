package swarm

import (
	"strings"
	"testing"
)

// These are package-internal tests for the filename-slug helpers
// (slugifyPlanName) and the plan-document validator (isPlanDocument). They
// assert directly against the unexported functions — the behavioural
// regression is also pinned through the public PublishPlanToVault and
// artifact-published gate paths in publish_test.go / gates_test.go.

func TestSlugifyPlanNameShortTitlePassesThroughUnchanged(t *testing.T) {
	// A short, already-sane title must be slugified verbatim — the cap
	// must not mangle titles that are already under the limit.
	cases := map[string]string{
		"Add /readyz Readiness Endpoint": "add-readyz-readiness-endpoint",
		"Auth Hardening Plan":            "auth-hardening-plan",
		"Companion Charter":              "companion-charter",
	}
	for title, want := range cases {
		got := slugifyPlanName(title, "", "fallback-chain")
		if got != want {
			t.Errorf("slugifyPlanName(%q) = %q, want %q", title, got, want)
		}
		if len(got) > slugMaxLen {
			t.Errorf("short title %q produced an over-cap slug %q", title, got)
		}
	}
}

func TestSlugifyPlanNameCapsLongTitle(t *testing.T) {
	// A long title (e.g. one accidentally derived from a run-on heading) is
	// capped to a short, readable slug — no leading/trailing hyphen, never
	// over slugMaxLen, and not the whole sentence.
	longTitle := "A conversational mental health companion agent that serves as the " +
		"users daily entry point for managing AuDHD specific mental health"

	slug := slugifyPlanName(longTitle, "", "mhc-long")

	if len(slug) > slugMaxLen {
		t.Fatalf("slug length %d exceeds cap %d: %q", len(slug), slugMaxLen, slug)
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		t.Fatalf("slug has a leading/trailing hyphen: %q", slug)
	}
	if !strings.HasPrefix(slug, "a-conversational-mental-health") {
		t.Fatalf("slug should keep the readable lead words, got %q", slug)
	}
}

func TestSlugifyPlanNameIsIdempotent(t *testing.T) {
	// The same inputs must always produce the same capped slug so
	// re-publishing overwrites the same file rather than littering.
	longTitle := "A conversational mental health companion agent that serves as the " +
		"users daily entry point for managing AuDHD specific mental health"

	first := slugifyPlanName(longTitle, "", "mhc-long")
	second := slugifyPlanName(longTitle, "", "mhc-long")
	if first != second {
		t.Fatalf("slug not idempotent: first %q != second %q", first, second)
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
