package chat_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tui/views/chat"
)

// Regression: long single-stream responses (planner reviewer summaries
// that arrive as 3000+ chunks over several minutes) appeared truncated
// on screen even though the engine committed the full content to the
// session JSON. The renderPartialResponseThrottled cache lags behind
// v.response by up to 100ms; the question this spec answers is whether
// finaliseChunk + RenderContent reliably emit the *complete* committed
// content at end-of-turn regardless of throttle state.
//
// If this test passes, the truncation symptom is in the viewport
// SetContent path (Bubble Tea) or somewhere downstream of view.go;
// if it fails, RenderContent is the culprit.
var _ = Describe("View burst-stream finalisation", func() {
	It("renders the full committed content after a long burst-stream Done", func() {
		v := chat.NewView()
		v.SetDimensions(120, 40)
		// Inject an identity render function so the test does not depend
		// on glamour's exact output formatting; only the presence of the
		// content matters for the truncation regression.
		v.SetMarkdownRenderer(func(s string, _ int) string { return s })

		// Build ~12KB of recognisable content split into 200 chunks —
		// large enough to push past the throttle's 100ms cool-down many
		// times during the burst, mirroring the real-world stalled-
		// session profile (3000+ chunks in 5min, 11823-char committed).
		const chunks = 200
		var fullExpected strings.Builder
		for n := 0; n < chunks; n++ {
			chunk := chunkBody(n)
			fullExpected.WriteString(chunk)
			v.HandleChunk(chunk, false, "", "", "")
		}
		// Final Done with no content (matches the real-world shape:
		// the last few content chunks arrive on Done=false followed by
		// an empty Done=true sentinel).
		v.HandleChunk("", true, "", "", "")

		rendered := v.RenderContent(120)

		// Sentinel checks at the start, middle, and end of the burst —
		// if the throttle drops the final committed render or
		// renderMessage truncates, at least one of these will fail.
		Expect(rendered).To(ContainSubstring(chunkBody(0)),
			"first chunk's marker missing — start of the response is truncated")
		Expect(rendered).To(ContainSubstring(chunkBody(chunks/2)),
			"mid-burst chunk's marker missing — middle of the response is truncated")
		Expect(rendered).To(ContainSubstring(chunkBody(chunks-1)),
			"last content chunk's marker missing — tail of the response is truncated")
		Expect(rendered).To(ContainSubstring(fullExpected.String()),
			"the rendered output must contain the complete concatenation of all chunks")
	})

	It("renders correctly when Done arrives WITH a final content payload", func() {
		v := chat.NewView()
		v.SetDimensions(120, 40)
		v.SetMarkdownRenderer(func(s string, _ int) string { return s })

		// Real provider streams sometimes pack the last content into
		// the Done chunk itself rather than emitting a separate
		// Done-with-empty-Content sentinel. Both shapes must surface
		// the full content end-to-end.
		v.HandleChunk("first part. ", false, "", "", "")
		v.HandleChunk("second part. ", false, "", "", "")
		v.HandleChunk("third part. ", true, "", "", "") // Done with content

		rendered := v.RenderContent(120)

		Expect(rendered).To(ContainSubstring("first part."))
		Expect(rendered).To(ContainSubstring("second part."))
		Expect(rendered).To(ContainSubstring("third part."),
			"Done-with-content payload must appear in the final render")
	})
})

// chunkBody returns a deterministic, recognisable chunk body for the
// burst test. Including the chunk index in the body lets the assertions
// pinpoint exactly which chunks (start/middle/end) survived the throttle.
func chunkBody(n int) string {
	return "[chunk-" + paddedIdx(n) + "-payload-with-enough-length-to-matter] "
}

func paddedIdx(n int) string {
	s := ""
	switch {
	case n < 10:
		s = "00"
	case n < 100:
		s = "0"
	}
	switch {
	case n < 10:
		s += string(rune('0' + n))
	case n < 100:
		s += string(rune('0'+n/10)) + string(rune('0'+n%10))
	default:
		s += string(rune('0'+n/100)) + string(rune('0'+(n/10)%10)) + string(rune('0'+n%10))
	}
	return s
}
