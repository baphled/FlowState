package session

import "testing"

// Temporary demonstration: proves the Test check actually gates PRs to next.
func TestControlledFailureDemo(t *testing.T) {
	t.Fatal("controlled failure: proving required CI gates the PR")
}
