//go:build e2e

package support

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/engine"
)

// inFlightToolResultCapSteps holds per-scenario state for the
// in-flight tool-result cap feature. The cap is exercised through the
// exported-for-test seam (engine.CapInFlightToolResultForTest), the
// same seam the engine's appendToolResultsBatchToMessages wiring calls
// via capInFlightToolResult.
type inFlightToolResultCapSteps struct {
	original      string
	alreadyCapped bool
	got           string
}

// RegisterInFlightToolResultCapSteps wires the in-flight tool-result cap
// feature steps onto the godog scenario context.
func RegisterInFlightToolResultCapSteps(ctx *godog.ScenarioContext) {
	s := &inFlightToolResultCapSteps{}
	ctx.Step(`^a tool result of (\d+) chars destined for the in-flight messages$`, s.toolResultOfChars)
	ctx.Step(`^a tool result of (\d+) chars already capped by the persist cap$`, s.toolResultAlreadyCapped)
	ctx.Step(`^the in-flight tool result cap is applied$`, s.capApplied)
	ctx.Step(`^the capped content is at most (\d+) chars$`, s.atMostChars)
	ctx.Step(`^the capped content carries the truncation marker$`, s.carriesMarker)
	ctx.Step(`^the capped content is identical to the original result$`, s.identicalToOriginal)
	ctx.Step(`^the capped content carries exactly one truncation marker$`, s.exactlyOneMarker)
}

// toolResultOfChars prepares a fresh oversized (or under-cap) tool result.
func (s *inFlightToolResultCapSteps) toolResultOfChars(n int) error {
	s.original = strings.Repeat("x", n)
	s.alreadyCapped = false
	return nil
}

// toolResultAlreadyCapped prepares a result first truncated by the
// persist cap so idempotency across the two caps is exercised.
func (s *inFlightToolResultCapSteps) toolResultAlreadyCapped(n int) error {
	s.original = engine.CapPersistedToolResultForTest(strings.Repeat("y", n))
	s.alreadyCapped = true
	return nil
}

// capApplied applies the in-flight cap under test.
func (s *inFlightToolResultCapSteps) capApplied() error {
	s.got = engine.CapInFlightToolResultForTest(s.original)
	return nil
}

// atMostChars asserts the capped content stays within the bound.
func (s *inFlightToolResultCapSteps) atMostChars(n int) error {
	if len(s.got) > n {
		return fmt.Errorf("capped content is %d chars, want at most %d", len(s.got), n)
	}
	return nil
}

// carriesMarker asserts the visible truncation marker is present.
func (s *inFlightToolResultCapSteps) carriesMarker() error {
	if !strings.Contains(s.got, engine.TruncationMarkerForTest()) {
		return fmt.Errorf("capped content missing truncation marker")
	}
	return nil
}

// identicalToOriginal asserts under-cap content passes through untouched.
func (s *inFlightToolResultCapSteps) identicalToOriginal() error {
	if s.got != s.original {
		return fmt.Errorf("under-cap content was modified by the in-flight cap")
	}
	return nil
}

// exactlyOneMarker asserts the persist cap's marker was not doubled.
func (s *inFlightToolResultCapSteps) exactlyOneMarker() error {
	count := strings.Count(s.got, engine.TruncationMarkerForTest())
	if !s.alreadyCapped {
		return fmt.Errorf("scenario expected already-capped input")
	}
	if count != 1 {
		return fmt.Errorf("expected exactly one truncation marker, found %d", count)
	}
	return nil
}
