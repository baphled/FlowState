package swarm

import (
	"strings"
	"testing"
)

// These are package-internal tests for the filename-slug helpers
// (slugifyPlanName + the purpose-fragment fallback). They assert the
// length cap, short-title passthrough, idempotency, and the lead-fragment
// fallback directly against the unexported functions — the behavioural
// regression is also pinned through the public PublishPlanToVault path in
// publish_test.go.

func TestSlugifyPlanNameCapsLongPurposeDerivedTitle(t *testing.T) {
	// The live mental-health plan's only title source is a single run-on
	// "purpose" sentence. firstPurposeFragment trims it to a lead clause;
	// slugifyPlanName then caps it. The slug must be short and readable.
	longPurpose := "A conversational mental health companion agent that serves as the " +
		"user's daily entry point for managing AuDHD-specific mental health, " +
		"medical cannabis tracking, biochemistry monitoring, and N-1 experimentation."
	title := firstPurposeFragment(longPurpose)

	slug := slugifyPlanName(title, "", "mhc-long")

	if len(slug) > slugMaxLen {
		t.Fatalf("slug length %d exceeds cap %d: %q", len(slug), slugMaxLen, slug)
	}
	if strings.HasPrefix(slug, "-") || strings.HasSuffix(slug, "-") {
		t.Fatalf("slug has a leading/trailing hyphen: %q", slug)
	}
	if !strings.HasPrefix(slug, "a-conversational-mental-health") {
		t.Fatalf("slug should keep the readable lead fragment, got %q", slug)
	}
	if strings.Contains(slug, "experimentation") {
		t.Fatalf("the whole run-on purpose leaked into the slug: %q", slug)
	}
}

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

func TestSlugifyPlanNameIsIdempotent(t *testing.T) {
	// The same inputs must always produce the same capped slug so
	// re-publishing overwrites the same file rather than littering.
	longPurpose := "A conversational mental health companion agent that serves as the " +
		"user's daily entry point for managing AuDHD-specific mental health, " +
		"medical cannabis tracking, biochemistry monitoring, and N-1 experimentation."
	title := firstPurposeFragment(longPurpose)

	first := slugifyPlanName(title, "", "mhc-long")
	second := slugifyPlanName(title, "", "mhc-long")
	if first != second {
		t.Fatalf("slug not idempotent: first %q != second %q", first, second)
	}
}

func TestFirstPurposeFragmentTakesLeadClause(t *testing.T) {
	// When an early comma/em-dash exists, the fragment is the lead clause
	// only — not the whole sentence — before the slug cap even applies.
	cases := []struct {
		in   string
		want string
	}{
		{"Be kind, be safe, be present.", "Be kind"},
		{"A companion — daily entry point — for wellbeing.", "A companion"},
		{"No early delimiter here at all so the whole thing returns", "No early delimiter here at all so the whole thing returns"},
	}
	for _, tc := range cases {
		got := firstPurposeFragment(tc.in)
		if got != tc.want {
			t.Errorf("firstPurposeFragment(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
