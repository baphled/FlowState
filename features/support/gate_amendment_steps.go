//go:build e2e

package support

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/swarm"
)

// GateAmendmentSteps holds state for the gate amendment directive
// scenarios (features/engine/gate_amendment_directive.feature). Each
// scenario builds a gate failure, runs appendGateDirective through the
// exported engine.AppendGateDirective seam, and asserts on the retry
// message the lead would re-dispatch.
type GateAmendmentSteps struct {
	gateErr  *swarm.GateError
	message  string
	result   string
	original string
}

// RegisterGateAmendmentSteps registers the gate amendment directive BDD
// step definitions.
//
// Expected:
//   - ctx is a valid godog ScenarioContext for step registration.
//
// Side effects:
//   - Registers step definitions on the provided scenario context.
func RegisterGateAmendmentSteps(ctx *godog.ScenarioContext) {
	s := &GateAmendmentSteps{}

	ctx.Step(`^a gate failure where the member wrote no output$`, s.aGateFailureWhereTheMemberWroteNoOutput)
	ctx.Step(`^a gate failure from (\S+) with reason "([^"]*)"$`, s.aGateFailureFromGateWithReason)
	ctx.Step(`^a gate failure with empty Reason$`, s.aGateFailureWithEmptyReason)
	ctx.Step(`^appendGateDirective constructs the retry message$`, s.appendGateDirectiveConstructsTheRetryMessage)
	ctx.Step(`^appendGateDirective is called$`, s.appendGateDirectiveIsCalled)
	ctx.Step(`^the directive mentions coordination_store$`, s.theDirectiveMentionsCoordinationStore)
	ctx.Step(`^the directive contains "([^"]*)"$`, s.theDirectiveContains)
	ctx.Step(`^the message is returned unchanged$`, s.theMessageIsReturnedUnchanged)
}

// aGateFailureWhereTheMemberWroteNoOutput builds the no-output failure:
// the result-schema runner's Reason carries the coordination_store
// write instruction.
//
// Side effects:
//   - Records the gate error on the step state.
func (s *GateAmendmentSteps) aGateFailureWhereTheMemberWroteNoOutput() {
	s.gateErr = &swarm.GateError{
		GateName: "post-member-explorer-evidence-grounding",
		Reason:   "no member output found at [dd-swarm/explorer/output]: the member did not write its output — re-delegate with an explicit coordination_store write.",
	}
	s.message = "original prompt"
	s.original = s.message
}

// aGateFailureFromGateWithReason builds a schema-validation failure with
// the named gate and reason.
//
// Side effects:
//   - Records the gate error on the step state.
func (s *GateAmendmentSteps) aGateFailureFromGateWithReason(gateName, reason string) {
	s.gateErr = &swarm.GateError{GateName: gateName, Reason: reason}
	s.message = "original prompt"
	s.original = s.message
}

// aGateFailureWithEmptyReason builds a gate failure whose Reason is empty.
//
// Side effects:
//   - Records the gate error on the step state.
func (s *GateAmendmentSteps) aGateFailureWithEmptyReason() {
	s.gateErr = &swarm.GateError{GateName: "post-member-writer-depth-budget", Reason: ""}
	s.message = "original prompt"
	s.original = s.message
}

// appendGateDirectiveConstructsTheRetryMessage runs the directive
// construction via the exported engine seam.
//
// Side effects:
//   - Records the result on the step state.
func (s *GateAmendmentSteps) appendGateDirectiveConstructsTheRetryMessage() {
	s.result = engine.AppendGateDirective(s.message, s.gateErr)
}

// appendGateDirectiveIsCalled is the unchanged-message variant of the
// When step; behaviour is identical.
//
// Side effects:
//   - Records the result on the step state.
func (s *GateAmendmentSteps) appendGateDirectiveIsCalled() {
	s.result = engine.AppendGateDirective(s.message, s.gateErr)
}

// theDirectiveMentionsCoordinationStore asserts the retry message tells
// the member to write to the coordination_store.
//
// Returns:
//   - An error when the assertion fails.
func (s *GateAmendmentSteps) theDirectiveMentionsCoordinationStore() error {
	return expectContains(s.result, "coordination_store")
}

// theDirectiveContains asserts the retry message carries an expected
// substring (gate name or part of the reason).
//
// Returns:
//   - An error when the assertion fails.
func (s *GateAmendmentSteps) theDirectiveContains(substr string) error {
	return expectContains(s.result, substr)
}

// theMessageIsReturnedUnchanged asserts the original message came back
// verbatim.
//
// Returns:
//   - An error when the assertion fails.
func (s *GateAmendmentSteps) theMessageIsReturnedUnchanged() error {
	if s.result != s.original {
		return errf("expected message unchanged %q, got %q", s.original, s.result)
	}
	return nil
}

// expectContains fails when substr is not in s.
//
// Returns:
//   - An error when s does not contain substr; nil otherwise.
func expectContains(s, substr string) error {
	if !strings.Contains(s, substr) {
		return errf("expected result to contain %q, got %q", substr, s)
	}
	return nil
}

// errf formats an assertion error.
//
// Returns:
//   - A formatted error.
func errf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
