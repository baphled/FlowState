// Package engine_test — H1 audit coverage for rehydration of
// FilesToRestore after an L2 compaction.
//
// AutoCompactor.Rehydrate has always existed with its own tests and
// BDD steps, but no production call site ever invoked it. Compaction
// serialised FilesToRestore into the placeholder text and then moved
// on; the files listed as "need to keep reading" stayed off disk
// between the compaction turn and the end of the session.
//
// H1 wires Rehydrate into buildContextWindow: on the turn AFTER a
// successful compaction (and any subsequent turn the summary still
// holds FilesToRestore), resolve and read the files once, prepend
// their contents to the window as tool-role messages, then mark the
// summary consumed so subsequent turns do not re-read the same files
// every build.
package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// TestBuildContextWindow_RehydratesFilesOnCompactionTurn pins the
// core contract: a compaction whose summary lists FilesToRestore
// must result in those files' contents appearing in the built
// window. The engine reads them on the same turn the compaction
// fires so the model sees the summary and the referenced files
// together (no half-populated windows, no "you summarised then made
// me wait a turn before showing me the files" behaviour).
func TestBuildContextWindow_RehydratesFilesOnCompactionTurn(t *testing.T) {
	t.Parallel()

	// Create a deterministic file the compaction summary will claim.
	tmp := t.TempDir()
	restored := filepath.Join(tmp, "restored.txt")
	payload := "rehydrated-file-canary-token-xyz123"
	if err := os.WriteFile(restored, []byte(payload), 0o600); err != nil {
		t.Fatalf("write restored file: %v", err)
	}

	summary := ctxstore.CompactionSummary{
		Intent:             "continue the H1 work",
		KeyDecisions:       []string{"call Rehydrate from buildContextWindow"},
		NextSteps:          []string{"assert the file content is injected"},
		FilesToRestore:     []string{restored},
		OriginalTokenCount: 0,
		SummaryTokenCount:  0,
	}
	summariser := &recordingSummariser{response: marshalSummaryForTest(t, summary)}

	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)
	seedMessages(t, store)

	// Turn 1 fires compaction — and on the same turn, rehydrates
	// FilesToRestore so the file's contents appear in the window.
	msgs := eng.BuildContextWindowForTest(context.Background(), "sess-h1-rehydrate", "turn 1")
	if !windowContains(msgs, payload) {
		t.Fatalf("turn 1 window did not contain rehydrated file canary %q; messages=%v", payload, summariseMsgs(msgs))
	}
}

// TestBuildContextWindow_DoesNotRehydrateOnSubsequentTurns pins the
// "consume once" contract. After rehydration fires on the compaction
// turn, further turns with the same compaction context must NOT re-
// read the same files. Without this guard every subsequent turn
// would pay the disk I/O of re-reading files already in the window
// (and the H2 memo path would let those subsequent turns hit the
// memo without a re-compaction, so the same summary would trigger
// re-reads indefinitely).
func TestBuildContextWindow_DoesNotRehydrateOnSubsequentTurns(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	restored := filepath.Join(tmp, "once.txt")
	if err := os.WriteFile(restored, []byte("visible-on-compaction-turn"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	summary := ctxstore.CompactionSummary{
		Intent:         "H1 consume-once",
		NextSteps:      []string{"drain FilesToRestore after first rehydration"},
		FilesToRestore: []string{restored},
	}
	summariser := &recordingSummariser{response: marshalSummaryForTest(t, summary)}

	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)
	seedMessages(t, store)

	msgs1 := eng.BuildContextWindowForTest(context.Background(), "sess-h1-once", "turn 1")
	if !windowContains(msgs1, "visible-on-compaction-turn") {
		t.Fatalf("turn 1: expected file content in window on compaction turn")
	}

	// Swap the file's contents — if rehydration re-runs on turn 2 it
	// would pick up this new content. With the H2 memo the turn 2
	// maybeAutoCompact hits the cached summary (cold prefix
	// unchanged) so the only way the fresh content could leak in is
	// a re-entry into rehydration, which is exactly what we are
	// testing against.
	if err := os.WriteFile(restored, []byte("SHOULD-NOT-APPEAR-ON-TURN-2"), 0o600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	msgs2 := eng.BuildContextWindowForTest(context.Background(), "sess-h1-once", "turn 2")
	if windowContains(msgs2, "SHOULD-NOT-APPEAR-ON-TURN-2") {
		t.Fatalf("turn 2: rehydration re-ran; file's fresh content leaked into the window")
	}
}

// TestBuildContextWindow_RehydrationMissingFile_LogsAndContinues pins
// graceful degradation. A file listed in FilesToRestore that no
// longer exists must not crash the build; the build must continue
// without the missing file rather than abort. The audit flagged this
// explicitly as a risk.
func TestBuildContextWindow_RehydrationMissingFile_LogsAndContinues(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	present := filepath.Join(tmp, "present.txt")
	if err := os.WriteFile(present, []byte("present-canary"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	missing := filepath.Join(tmp, "does-not-exist.txt")

	summary := ctxstore.CompactionSummary{
		Intent:         "H1 partial missing",
		NextSteps:      []string{"log missing, skip, continue"},
		FilesToRestore: []string{missing, present}, // missing first
	}
	summariser := &recordingSummariser{response: marshalSummaryForTest(t, summary)}

	eng, store := newTestEngineWithCompactor(t, summariser, 0.60, true)
	seedMessages(t, store)

	_ = eng.BuildContextWindowForTest(context.Background(), "sess-h1-missing", "turn 1")

	// Must not crash. Should either skip the missing file and include
	// the present one, or skip the whole rehydration — either is
	// acceptable; a crash is not.
	msgs := eng.BuildContextWindowForTest(context.Background(), "sess-h1-missing", "turn 2")
	// The build completed — prove that by checking the window has the
	// usual system + user messages.
	if len(msgs) == 0 {
		t.Fatalf("missing file caused build to abort; got empty window")
	}
}

// marshalSummaryForTest wraps buildSummaryJSON's behaviour but lets
// the caller supply the summary struct directly. Keeps the H1 tests
// self-contained without forcing buildSummaryJSON's signature to fan
// out to every test file.
func marshalSummaryForTest(t *testing.T, summary ctxstore.CompactionSummary) string {
	t.Helper()
	data, err := json.Marshal(summary)
	if err != nil {
		t.Fatalf("marshal summary: %v", err)
	}
	return string(data)
}

// windowContains is true when any message in msgs has Content that
// contains substr. Used to assert rehydrated file bodies end up in
// the built window without pinning the exact message-role choice the
// production implementation uses for the tool messages.
func windowContains(msgs []provider.Message, substr string) bool {
	for i := range msgs {
		if strings.Contains(msgs[i].Content, substr) {
			return true
		}
	}
	return false
}

// summariseMsgs returns a slice of "role: content" lines suitable for
// error messages so a test failure shows the caller what the window
// actually held.
func summariseMsgs(msgs []provider.Message) []string {
	out := make([]string, 0, len(msgs))
	for i := range msgs {
		out = append(out, msgs[i].Role+": "+truncate(msgs[i].Content, 60))
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
