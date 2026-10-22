//go:build e2e

package support

import (
	"fmt"
	"strings"

	"github.com/cucumber/godog"

	"github.com/baphled/flowstate/internal/session"
)

// transcriptToolResultCapSteps holds per-scenario state for the
// session-transcript tool-result cap feature. The cap is exercised
// through the e2e seam session.CapTranscriptToolResultForTest, the
// same helper accumulator.applyToolResult calls on every tool result
// appended to the session transcript.
type transcriptToolResultCapSteps struct {
	original string
	got      string
}

// RegisterTranscriptToolResultCapSteps wires the session-transcript
// tool-result cap feature steps onto the godog scenario context.
func RegisterTranscriptToolResultCapSteps(ctx *godog.ScenarioContext) {
	s := &transcriptToolResultCapSteps{}
	ctx.Step(`^a tool result of (\d+) chars destined for the session transcript$`, s.toolResultOfChars)
	ctx.Step(`^the transcript tool result cap is applied$`, s.capApplied)
	ctx.Step(`^the transcript content is at most (\d+) chars$`, s.atMostChars)
	ctx.Step(`^the transcript content carries the truncation marker$`, s.carriesMarker)
	ctx.Step(`^the transcript content is identical to the original result$`, s.identicalToOriginal)
	ctx.Step(`^the transcript content carries exactly one truncation marker$`, s.exactlyOneMarker)
}

// toolResultOfChars prepares a fresh oversized (or under-cap) tool result.
func (s *transcriptToolResultCapSteps) toolResultOfChars(n int) error {
	s.original = strings.Repeat("t", n)
	s.got = ""
	return nil
}

// capApplied applies the transcript cap under test.
func (s *transcriptToolResultCapSteps) capApplied() error {
	s.got = session.CapTranscriptToolResultForTest(s.original)
	return nil
}

// atMostChars asserts the capped content stays within the bound.
func (s *transcriptToolResultCapSteps) atMostChars(n int) error {
	if len(s.got) > n {
		return fmt.Errorf("transcript content is %d chars, want at most %d", len(s.got), n)
	}
	return nil
}

// carriesMarker asserts the visible truncation marker is present.
func (s *transcriptToolResultCapSteps) carriesMarker() error {
	if !strings.Contains(s.got, session.TranscriptTruncationMarkerForTest()) {
		return fmt.Errorf("transcript content missing truncation marker")
	}
	return nil
}

// identicalToOriginal asserts under-cap content passes through untouched.
func (s *transcriptToolResultCapSteps) identicalToOriginal() error {
	if s.got != s.original {
		return fmt.Errorf("under-cap content was modified by the transcript cap")
	}
	return nil
}

// exactlyOneMarker asserts a second cap pass does not double the marker.
func (s *transcriptToolResultCapSteps) exactlyOneMarker() error {
	s.got = session.CapTranscriptToolResultForTest(s.got)
	count := strings.Count(s.got, session.TranscriptTruncationMarkerForTest())
	if count != 1 {
		return fmt.Errorf("expected exactly one truncation marker, found %d", count)
	}
	return nil
}
