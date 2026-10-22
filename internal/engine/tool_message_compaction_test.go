package engine

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("compactOldToolResultsForBudget", func() {
	fatMsgs := func(n int) []provider.Message {
		msgs := make([]provider.Message, 0, n*2+1)
		msgs = append(msgs, provider.Message{Role: "user", Content: "hello"})
		for i := 0; i < n; i++ {
			msgs = append(msgs, provider.Message{Role: "assistant", Content: "call"})
			msgs = append(msgs, provider.Message{Role: "tool", Content: strings.Repeat("line of tool output\n", 200)})
		}
		return msgs
	}

	It("compacts when estimated tokens exceed the budget ratio even below the count threshold", func() {
		msgs := fatMsgs(3) // 3 tool messages, below count threshold 5, but huge
		out := compactOldToolResultsForBudget(msgs, 5, 1, &stubTokenCounter{}, 3_000)
		Expect(out[2].Content).To(ContainSubstring(toolMessageCompactionDetector))
		Expect(out[4].Content).To(ContainSubstring(toolMessageCompactionDetector)) // older results stubbed
	})

	It("keeps the most recent tool result intact when the token floor fires", func() {
		msgs := fatMsgs(3)
		out := compactOldToolResultsForBudget(msgs, 5, 1, &stubTokenCounter{}, 3_000)
		Expect(out[len(out)-1].Content).To(Equal(msgs[len(msgs)-1].Content))
	})

	It("returns the input unchanged when the estimate fits the budget ratio", func() {
		msgs := fatMsgs(2)
		out := compactOldToolResultsForBudget(msgs, 5, 1, &stubTokenCounter{}, 10_000_000)
		Expect(out).To(Equal(msgs))
	})

	It("is idempotent under the token floor", func() {
		msgs := fatMsgs(3)
		once := compactOldToolResultsForBudget(msgs, 5, 1, &stubTokenCounter{}, 3_000)
		twice := compactOldToolResultsForBudget(once, 5, 1, &stubTokenCounter{}, 3_000)
		Expect(twice).To(Equal(once))
	})
})

var _ = Describe("compactOldToolResults", func() {
	mkMsgs := func(n int) []provider.Message {
		msgs := make([]provider.Message, 0, n*2+1)
		msgs = append(msgs, provider.Message{Role: "user", Content: "hello"})
		for i := 0; i < n; i++ {
			msgs = append(msgs, provider.Message{Role: "assistant", Content: "call"})
			msgs = append(msgs, provider.Message{Role: "tool", Content: strings.Repeat("line of tool output\n", 40)})
		}
		return msgs
	}

	It("returns the input unchanged when below the threshold", func() {
		msgs := mkMsgs(3)
		out := compactOldToolResults(msgs, 5, 1)
		Expect(out).To(Equal(msgs))
	})

	It("returns the input unchanged at exactly the threshold", func() {
		msgs := mkMsgs(5)
		out := compactOldToolResults(msgs, 5, 1)
		Expect(out).To(Equal(msgs))
	})

	It("compacts older tool results and keeps the most recent ones intact", func() {
		msgs := mkMsgs(8)
		out := compactOldToolResults(msgs, 5, 2)
		Expect(out).To(HaveLen(len(msgs)))
		var toolIdx []int
		for i, m := range out {
			if m.Role == "tool" {
				toolIdx = append(toolIdx, i)
			}
		}
		Expect(toolIdx).To(HaveLen(8))
		for _, i := range toolIdx[:6] {
			Expect(out[i].Content).To(ContainSubstring(toolMessageCompactionDetector))
			Expect(len(out[i].Content)).To(BeNumerically("<", len(msgs[i].Content)))
		}
		for _, i := range toolIdx[6:] {
			Expect(out[i].Content).To(Equal(msgs[i].Content))
		}
	})

	It("is idempotent when run twice", func() {
		msgs := mkMsgs(8)
		once := compactOldToolResults(msgs, 5, 2)
		twice := compactOldToolResults(once, 5, 2)
		Expect(twice).To(Equal(once))
	})

	It("leaves non-tool messages untouched", func() {
		msgs := mkMsgs(8)
		out := compactOldToolResults(msgs, 5, 2)
		for i, m := range out {
			if m.Role != "tool" {
				Expect(m).To(Equal(msgs[i]))
			}
		}
	})

	It("does not mutate the input slice", func() {
		msgs := mkMsgs(8)
		snapshot := make([]provider.Message, len(msgs))
		copy(snapshot, msgs)
		_ = compactOldToolResults(msgs, 5, 2)
		Expect(msgs).To(Equal(snapshot))
	})

	It("handles an empty slice", func() {
		out := compactOldToolResults(nil, 5, 2)
		Expect(out).To(BeNil())
	})

	It("keeps the placeholder deterministic with first line, last line and byte count", func() {
		msgs := mkMsgs(8)
		out := compactOldToolResults(msgs, 5, 2)
		first := msgs[2].Content
		placeholder := out[2].Content
		Expect(placeholder).To(ContainSubstring(toolMessageCompactionDetector))
		Expect(placeholder).To(ContainSubstring("line of tool output"))
		Expect(placeholder).To(ContainSubstring("800 bytes"))
		_ = first
	})
})

// stubTokenCounter is a minimal TokenCounter for compaction tests:
// ~1 token per 4 bytes, no model-specific limits.
type stubTokenCounter struct{}

func (s *stubTokenCounter) Count(text string) int { return len(text) / 4 }
func (s *stubTokenCounter) ModelLimit(string) int { return 0 }
