package compaction_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/context/compaction"
	"github.com/baphled/flowstate/internal/provider"
)

func toolResult(toolName, callID, content string) provider.Message {
	return provider.Message{
		Role:      "tool",
		Content:   content,
		ToolCalls: []provider.ToolCall{{ID: callID, Name: toolName}},
	}
}

func assistantToolUse(toolName, callID string) provider.Message {
	return provider.Message{
		Role: "assistant",
		ToolCalls: []provider.ToolCall{
			{ID: callID, Name: toolName, Arguments: map[string]any{}},
		},
	}
}

func userText(s string) provider.Message {
	return provider.Message{Role: "user", Content: s}
}

func assistantText(s string) provider.Message {
	return provider.Message{Role: "assistant", Content: s}
}

func newCompactor(storeDir string, hotMin, budget int) *compaction.MicroCompactor {
	return compaction.NewMicroCompactor(compaction.Options{
		StoreRoot:  storeDir,
		HotTailMin: hotMin,
		SizeBudget: budget,
	})
}

func contentSizes(msgs []provider.Message) []int {
	out := make([]int, len(msgs))
	for i := range msgs {
		out[i] = len(msgs[i].Content)
	}
	return out
}

const compactedSentinel = "[content offloaded to "

func isReference(content string) bool {
	return strings.HasPrefix(content, compactedSentinel)
}

var _ = Describe("MicroCompactor.Compact", func() {
	var (
		ctx       context.Context
		storeDir  string
		sessionID string
	)

	BeforeEach(func() {
		ctx = context.Background()
		storeDir = GinkgoT().TempDir()
		sessionID = "sess-1"
	})

	It("keeps the last N compactable results visible and offloads older ones", func() {
		mc := newCompactor(storeDir, 3, 0)

		msgs := []provider.Message{
			userText("kick off"),
			assistantToolUse("read", "r1"),
			toolResult("read", "r1", strings.Repeat("a", 200)),
			assistantToolUse("read", "r2"),
			toolResult("read", "r2", strings.Repeat("b", 200)),
			assistantToolUse("read", "r3"),
			toolResult("read", "r3", strings.Repeat("c", 200)),
			assistantToolUse("read", "r4"),
			toolResult("read", "r4", strings.Repeat("d", 200)),
			assistantToolUse("read", "r5"),
			toolResult("read", "r5", strings.Repeat("e", 200)),
		}

		out, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(HaveLen(len(msgs)))

		Expect(isReference(out[2].Content)).To(BeTrue(), "r1 should be cold")
		Expect(isReference(out[4].Content)).To(BeTrue(), "r2 should be cold")
		Expect(isReference(out[6].Content)).To(BeFalse(), "r3 stays hot")
		Expect(isReference(out[8].Content)).To(BeFalse(), "r4 stays hot")
		Expect(isReference(out[10].Content)).To(BeFalse(), "r5 stays hot")
	})

	It("never offloads non-compactable tool results (delegate, skill_load)", func() {
		mc := newCompactor(storeDir, 0, 0)

		msgs := []provider.Message{
			assistantToolUse("delegate", "d1"),
			toolResult("delegate", "d1", strings.Repeat("x", 5000)),
			assistantToolUse("skill_load", "s1"),
			toolResult("skill_load", "s1", strings.Repeat("y", 5000)),
		}

		out, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(contentSizes(out)).To(Equal(contentSizes(msgs)))
	})

	It("never offloads user or assistant text messages", func() {
		mc := newCompactor(storeDir, 0, 0)

		msgs := []provider.Message{
			userText(strings.Repeat("u", 9000)),
			assistantText(strings.Repeat("a", 9000)),
		}

		out, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())
		Expect(contentSizes(out)).To(Equal(contentSizes(msgs)))
	})

	It("emits a reference message that points at <sessionID>/compacted/<id>.txt", func() {
		mc := newCompactor(storeDir, 0, 0)

		msgs := []provider.Message{
			assistantToolUse("read", "r1"),
			toolResult("read", "r1", "payload-one"),
			assistantToolUse("read", "r2"),
			toolResult("read", "r2", "payload-two"),
		}

		out, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())

		Expect(out[1].Content).To(ContainSubstring(filepath.Join(sessionID, "compacted", "r1.txt")))
		Expect(out[3].Content).To(ContainSubstring(filepath.Join(sessionID, "compacted", "r2.txt")))

		body, err := os.ReadFile(filepath.Join(storeDir, sessionID, "compacted", "r1.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("payload-one"))
	})

	It("is idempotent: running compaction twice yields the same final slice", func() {
		mc := newCompactor(storeDir, 1, 0)

		msgs := []provider.Message{
			assistantToolUse("read", "r1"),
			toolResult("read", "r1", strings.Repeat("a", 1000)),
			assistantToolUse("read", "r2"),
			toolResult("read", "r2", strings.Repeat("b", 1000)),
			assistantToolUse("read", "r3"),
			toolResult("read", "r3", strings.Repeat("c", 1000)),
		}

		first, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())
		second, err := mc.Compact(ctx, sessionID, first)
		Expect(err).NotTo(HaveOccurred())

		Expect(second).To(Equal(first))
	})

	It("writes cold-store payload files with 0o600 perms", func() {
		mc := newCompactor(storeDir, 0, 0)

		msgs := []provider.Message{
			assistantToolUse("read", "r1"),
			toolResult("read", "r1", "payload"),
		}

		_, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())

		info, err := os.Stat(filepath.Join(storeDir, sessionID, "compacted", "r1.txt"))
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("offloads older results once the size budget is exceeded beyond HotTailMin", func() {
		mc := newCompactor(storeDir, 2, 1000)

		msgs := []provider.Message{
			assistantToolUse("read", "r1"),
			toolResult("read", "r1", strings.Repeat("x", 600)),
			assistantToolUse("read", "r2"),
			toolResult("read", "r2", strings.Repeat("y", 600)),
			assistantToolUse("read", "r3"),
			toolResult("read", "r3", strings.Repeat("z", 600)),
		}

		out, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())

		Expect(isReference(out[5].Content)).To(BeFalse(), "newest stays hot")
		Expect(isReference(out[3].Content)).To(BeFalse(), "second-newest fits in HotTailMin floor")
		Expect(isReference(out[1].Content)).To(BeTrue(),
			"oldest is cold once the budget is breached past HotTailMin")
	})

	It("preserves message order and does not mutate the caller's slice", func() {
		mc := newCompactor(storeDir, 0, 0)

		msgs := []provider.Message{
			assistantToolUse("read", "r1"),
			toolResult("read", "r1", "payload"),
		}
		original := make([]provider.Message, len(msgs))
		copy(original, msgs)

		_, err := mc.Compact(ctx, sessionID, msgs)
		Expect(err).NotTo(HaveOccurred())

		Expect(msgs[1].Content).To(Equal(original[1].Content))
	})
})
