package engine_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/swarm"
)

// gateErrForTest builds a typed gate error the way the swarm runner
// does, so the seam exercises the real directive path.
func gateErrForTest() error {
	return &swarm.GateError{GateName: "result-schema", Reason: "output missing findings"}
}

// TestAppendGateDirectiveAppends asserts the exported seam appends the
// gate-retry directive to a non-empty message on a fresh paragraph.
func TestAppendGateDirectiveAppends(t *testing.T) {
	got := engine.AppendGateDirective("analyse the code", gateErrForTest())
	if !strings.HasPrefix(got, "analyse the code") {
		t.Fatalf("got %q, want original message preserved", got)
	}
	if !strings.Contains(got, "result-schema") {
		t.Fatalf("got %q, want gate name in directive", got)
	}
}

// TestAppendGateDirectiveEmptyMessage asserts the seam returns the
// directive alone when the original message is empty.
func TestAppendGateDirectiveEmptyMessage(t *testing.T) {
	got := engine.AppendGateDirective("", gateErrForTest())
	if !strings.Contains(got, "result-schema") {
		t.Fatalf("got %q, want directive alone", got)
	}
}

// TestAppendGateDirectiveIgnoresNonGateError asserts a plain error
// leaves the message unchanged; only *swarm.GateError triggers the
// directive path.
func TestAppendGateDirectiveIgnoresNonGateError(t *testing.T) {
	msg := "plain message"
	got := engine.AppendGateDirective(msg, errors.New("ordinary"))
	if got != msg {
		t.Fatalf("got %q, want unchanged %q", got, msg)
	}
}
