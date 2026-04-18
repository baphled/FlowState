package support

import (
	"context"
	"testing"
	"time"
)

// TestStreamingCancelStepsNoDataRace is a focused regression test that
// exercises the iSend / iSeeTokensAppearing sequence the BDD scenario
// "Streaming can be interrupted by double-Escape" runs, and verifies the
// step pair is free of data races.
//
// Background: godog executes scenario steps serially on the main test
// goroutine, but iSend spawns a background goroutine to drain the mock
// provider's stream. That goroutine writes to StepDefinitions.responseParts
// while iSeeTokensAppearing concurrently reads it. Prior to the fix for
// P9/T1 this produced a "WARNING: DATA RACE" under `go test -race`.
//
// The test loops the interaction deliberately so that a regression
// (removal of synchronisation) is caught even if the timing happens
// to line up favourably on a single iteration.
//
// This test must be run with `-race` to meaningfully assert the fix
// (the race detector is the oracle). Under `go test` without `-race`
// it still passes when the code is correct but cannot detect a race.
func TestStreamingCancelStepsNoDataRace(t *testing.T) {
	t.Parallel()

	const iterations = 50
	for i := range iterations {
		s := &StepDefinitions{
			ctx: context.Background(),
			app: &TestApp{provider: NewMockProvider()},
		}
		// Put the provider into long-stream mode so chunks arrive
		// gradually; this maximises the window where the reader and
		// writer goroutines overlap.
		s.app.provider.SetLongStream(true)
		s.streamFullLen = LongStreamFullLen()

		if err := s.iSend("race probe"); err != nil {
			t.Fatalf("iteration %d: iSend failed: %v", i, err)
		}

		// Poll iSeeTokensAppearing until it returns. Under the old
		// (unsynchronised) implementation this read concurrently with
		// the drain goroutine's write at streaming_cancel_steps.go:118,
		// which the race detector reported as the P1-era regression.
		if err := s.iSeeTokensAppearing(); err != nil {
			// Tokens may not arrive within budget on a slow CI box —
			// that's not what we're testing. Cancel and continue.
			s.streamCancel()
			drainWait(t, s.streamDrainDone)
			continue
		}

		// Issue the cancel and wait for the drain to exit cleanly.
		if err := s.iPressEscapeTwiceWithin500ms(); err != nil {
			t.Fatalf("iteration %d: cancel failed: %v", i, err)
		}
		drainWait(t, s.streamDrainDone)
	}
}

// drainWait blocks until the drain goroutine signals completion or a
// generous budget elapses. The budget prevents a hang from masquerading
// as a pass.
func drainWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("drain goroutine did not exit within 2s")
	}
}
