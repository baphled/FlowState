package engine_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ctxstore "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

// marshalSummaryForTest wraps buildSummaryJSON's behaviour but lets the
// caller supply the summary struct directly. Keeps the H1 specs
// self-contained without forcing buildSummaryJSON's signature to fan out
// to every test file.
func marshalSummaryForTest(summary ctxstore.CompactionSummary) string {
	data, err := json.Marshal(summary)
	Expect(err).NotTo(HaveOccurred(), "marshal summary")
	return string(data)
}

// windowContains is true when any message in msgs has Content that
// contains substr. Used to assert rehydrated file bodies end up in the
// built window without pinning the exact message-role choice the
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
// failure messages so a test failure shows the caller what the window
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

// H1 audit coverage for rehydration of FilesToRestore after an L2
// compaction.
//
// AutoCompactor.Rehydrate has always existed with its own tests and BDD
// steps, but no production call site ever invoked it. Compaction
// serialised FilesToRestore into the placeholder text and then moved on;
// the files listed as "need to keep reading" stayed off disk between the
// compaction turn and the end of the session.
//
// H1 wires Rehydrate into buildContextWindow: on the turn AFTER a
// successful compaction (and any subsequent turn the summary still holds
// FilesToRestore), resolve and read the files once, prepend their contents
// to the window as tool-role messages, then mark the summary consumed so
// subsequent turns do not re-read the same files every build.
var _ = Describe("Engine.buildContextWindow rehydration trigger", func() {
	It("rehydrates the files listed in FilesToRestore on the compaction turn", func() {
		// Create a deterministic file the compaction summary will claim.
		tmp := GinkgoT().TempDir()
		restored := filepath.Join(tmp, "restored.txt")
		payload := "rehydrated-file-canary-token-xyz123"
		Expect(os.WriteFile(restored, []byte(payload), 0o600)).To(Succeed())

		summary := ctxstore.CompactionSummary{
			Intent:             "continue the H1 work",
			KeyDecisions:       []string{"call Rehydrate from buildContextWindow"},
			NextSteps:          []string{"assert the file content is injected"},
			FilesToRestore:     []string{restored},
			OriginalTokenCount: 0,
			SummaryTokenCount:  0,
		}
		summariser := &recordingSummariser{response: marshalSummaryForTest(summary)}

		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)
		seedMessages(store)

		msgs := eng.BuildContextWindowForTest(context.Background(), "sess-h1-rehydrate", "turn 1")
		Expect(windowContains(msgs, payload)).To(BeTrue(),
			"turn 1 window did not contain rehydrated file canary; messages=%v", summariseMsgs(msgs))
	})

	It("does not re-read FilesToRestore on subsequent turns once consumed", func() {
		tmp := GinkgoT().TempDir()
		restored := filepath.Join(tmp, "once.txt")
		Expect(os.WriteFile(restored, []byte("visible-on-compaction-turn"), 0o600)).To(Succeed())

		summary := ctxstore.CompactionSummary{
			Intent:         "H1 consume-once",
			NextSteps:      []string{"drain FilesToRestore after first rehydration"},
			FilesToRestore: []string{restored},
		}
		summariser := &recordingSummariser{response: marshalSummaryForTest(summary)}

		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)
		seedMessages(store)

		msgs1 := eng.BuildContextWindowForTest(context.Background(), "sess-h1-once", "turn 1")
		Expect(windowContains(msgs1, "visible-on-compaction-turn")).To(BeTrue(),
			"turn 1: expected file content in window on compaction turn")

		// Swap the file's contents — if rehydration re-runs on turn 2 it
		// would pick up this new content. With the H2 memo the turn 2
		// maybeAutoCompact hits the cached summary (cold prefix
		// unchanged) so the only way the fresh content could leak in is
		// a re-entry into rehydration, which is exactly what we are
		// testing against.
		Expect(os.WriteFile(restored, []byte("SHOULD-NOT-APPEAR-ON-TURN-2"), 0o600)).To(Succeed())
		msgs2 := eng.BuildContextWindowForTest(context.Background(), "sess-h1-once", "turn 2")
		Expect(windowContains(msgs2, "SHOULD-NOT-APPEAR-ON-TURN-2")).To(BeFalse(),
			"turn 2: rehydration re-ran; file's fresh content leaked into the window")
	})

	It("logs and continues when a FilesToRestore entry is missing", func() {
		tmp := GinkgoT().TempDir()
		present := filepath.Join(tmp, "present.txt")
		Expect(os.WriteFile(present, []byte("present-canary"), 0o600)).To(Succeed())
		missing := filepath.Join(tmp, "does-not-exist.txt")

		summary := ctxstore.CompactionSummary{
			Intent:         "H1 partial missing",
			NextSteps:      []string{"log missing, skip, continue"},
			FilesToRestore: []string{missing, present}, // missing first
		}
		summariser := &recordingSummariser{response: marshalSummaryForTest(summary)}

		eng, store := newTestEngineWithCompactor(summariser, 0.60, true)
		seedMessages(store)

		_ = eng.BuildContextWindowForTest(context.Background(), "sess-h1-missing", "turn 1")

		// Must not crash. Should either skip the missing file and include
		// the present one, or skip the whole rehydration — either is
		// acceptable; a crash is not.
		msgs := eng.BuildContextWindowForTest(context.Background(), "sess-h1-missing", "turn 2")
		Expect(msgs).NotTo(BeEmpty(),
			"missing file caused build to abort; got empty window")
	})
})
