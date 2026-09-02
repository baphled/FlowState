package engine_test

import (
	"testing"

	"github.com/baphled/flowstate/internal/engine"
)

// TestHasSubstantiveOutputForTestCases pins the empty-response policy
// predicate across representative values: whitespace and empty JSON
// containers are non-substantive; content is substantive.
func TestHasSubstantiveOutputForTestCases(t *testing.T) {
	cases := []struct {
		name string
		val  string
		want bool
	}{
		{"empty", "", false},
		{"whitespace", "   \n\t ", false},
		{"empty object", "{}", false},
		{"empty array", "[]", false},
		{"text", "findings here", true},
		{"object with content", `{"a":1}`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := engine.HasSubstantiveOutputForTest([]byte(tc.val)); got != tc.want {
				t.Fatalf("HasSubstantiveOutputForTest(%q) = %v, want %v", tc.val, got, tc.want)
			}
		})
	}
}

// TestHasSubstantiveOutputForTestNil asserts nil input fails closed.
func TestHasSubstantiveOutputForTestNil(t *testing.T) {
	if engine.HasSubstantiveOutputForTest(nil) {
		t.Fatal("nil input reported substantive, want false")
	}
}
