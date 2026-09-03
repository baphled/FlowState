package engine_test

import (
	"context"
	"testing"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

// TestHasSubstantiveOutputForTestCover asserts the re-exported
// predicate classifies whitespace and empty containers as
// non-substantive and prose as substantive.
func TestHasSubstantiveOutputForTestCover(t *testing.T) {
	for _, val := range []string{"   ", "{}", "[]", ``} {
		if engine.HasSubstantiveOutputForTest([]byte(val)) {
			t.Fatalf("HasSubstantiveOutputForTest(%q) = true, want false", val)
		}
	}
	if !engine.HasSubstantiveOutputForTest([]byte("substantive analysis")) {
		t.Fatal("HasSubstantiveOutputForTest(prose) = false, want true")
	}
}

// TestCollectDelegationCompletionFailClosedOnEmpty asserts the
// completion seam fails closed on a drained-but-empty child stream.
func TestCollectDelegationCompletionFailClosedOnEmpty(t *testing.T) {
	dt := engine.NewDelegateTool(map[string]*engine.Engine{}, agent.Delegation{}, "orchestrator")
	chunks := make(chan provider.StreamChunk)
	close(chunks)
	_, err := engine.CollectDelegationCompletion(context.Background(), dt, chunks, time.Now())
	if err == nil {
		t.Fatal("CollectDelegationCompletion on empty stream = nil error, want fail-closed error")
	}
}

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
