package engine_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/prompt"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/tool"
)

func promptBody(agentID string) (string, error) {
	return prompt.GetPrompt(agentID)
}

// These specs pin the context-anchoring behaviour that prevents the
// "agent responds to tool content instead of original user prompt" drift.
// Live evidence: session 089c7cd5-37d8-4a59-868d-366d2dca0cfb (May 2026)
// — a 506-char user prompt was eclipsed by 689k chars of tool_result
// content; the model answered about a draft message inside one of the
// tool reads instead of the user's actual question. The seam is
// engine.appendToolResultsBatchToMessages: when a tool-result batch is
// non-trivial (> 5 KiB) the engine MUST append a system-role reminder
// referencing the user's most recent user-role message so the next
// provider request re-anchors the model on the user's request rather
// than on the trailing tool output.
var _ = Describe("Context anchoring after tool-result waves", func() {
	var (
		eng       *engine.Engine
		manifest  agent.Manifest
		baseUser  string
		baseSys   provider.Message
		userMsg   provider.Message
		largeData string
		smallData string
	)

	BeforeEach(func() {
		manifest = agent.Manifest{
			ID:                "test-agent",
			Name:              "Test Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are a helpful assistant."},
			ContextManagement: agent.DefaultContextManagement(),
		}
		eng = engine.New(engine.Config{Manifest: manifest})

		baseUser = "Will sex with Nicola muddy the waters? please give a structured answer"
		baseSys = provider.Message{Role: "system", Content: "system prompt"}
		userMsg = provider.Message{Role: "user", Content: baseUser}

		// Large enough to cross the > 5 KiB injection threshold.
		largeData = strings.Repeat("A", 6*1024)
		// Well under the threshold — typical small tool result.
		smallData = strings.Repeat("b", 256)
	})

	Context("when the tool-result batch exceeds the injection threshold", func() {
		It("appends a system-role reminder referencing the user's most recent user-role message", func() {
			messages := []provider.Message{baseSys, userMsg}
			calls := []*provider.ToolCall{{ID: "call_1", Name: "read", Arguments: map[string]any{"path": "/tmp/x"}}}
			results := []tool.Result{{Output: largeData}}

			out := eng.AppendToolResultsBatchToMessagesForTest(messages, calls, results)

			// Expect: original system + user, one assistant tool_call message,
			// one tool message with the result, then a system reminder.
			Expect(out).To(HaveLen(len(messages) + 3))

			reminder := out[len(out)-1]
			Expect(reminder.Role).To(Equal("system"),
				"the re-anchor reminder must be a system-role message so the provider treats it as a constraint")

			// Reminder must distinguish reference material from the user's actual request,
			// and it must quote (a truncated form of) the user's prompt verbatim.
			Expect(reminder.Content).To(ContainSubstring(baseUser),
				"reminder must echo the user's actual request so the model re-anchors on it")
			Expect(strings.ToLower(reminder.Content)).To(ContainSubstring("reference material"),
				"reminder must label tool results as reference material to discourage the model treating them as instructions")
		})

		It("anchors on the MOST RECENT user-role message in a multi-turn history", func() {
			latest := "What is the actual question — do NOT re-answer the earlier one."
			messages := []provider.Message{
				baseSys,
				{Role: "user", Content: "first user message — already answered"},
				{Role: "assistant", Content: "earlier reply"},
				{Role: "user", Content: latest},
			}
			calls := []*provider.ToolCall{{ID: "call_2", Name: "read", Arguments: map[string]any{"path": "/tmp/y"}}}
			results := []tool.Result{{Output: largeData}}

			out := eng.AppendToolResultsBatchToMessagesForTest(messages, calls, results)

			reminder := out[len(out)-1]
			Expect(reminder.Role).To(Equal("system"))
			Expect(reminder.Content).To(ContainSubstring(latest),
				"reminder must quote the latest user-role message, not earlier ones")
			Expect(reminder.Content).NotTo(ContainSubstring("first user message — already answered"),
				"reminder must not reference stale earlier user-role messages")
		})

		It("truncates very long user prompts to keep the reminder compact", func() {
			longPrompt := strings.Repeat("u", 2000)
			messages := []provider.Message{baseSys, {Role: "user", Content: longPrompt}}
			calls := []*provider.ToolCall{{ID: "call_3", Name: "read", Arguments: map[string]any{"path": "/tmp/z"}}}
			results := []tool.Result{{Output: largeData}}

			out := eng.AppendToolResultsBatchToMessagesForTest(messages, calls, results)

			reminder := out[len(out)-1]
			Expect(reminder.Role).To(Equal("system"))
			// Reminder body must be substantially shorter than the raw user prompt
			// (the brief calls for max ~500 chars of user prompt inside the reminder).
			Expect(len(reminder.Content)).To(BeNumerically("<", 1000),
				"reminder must stay compact even when the user prompt is very long")
		})
	})

	Context("when the tool-result batch is small", func() {
		It("does NOT inject a reminder so noise is avoided on routine turns", func() {
			messages := []provider.Message{baseSys, userMsg}
			calls := []*provider.ToolCall{{ID: "call_small", Name: "read", Arguments: map[string]any{"path": "/tmp/x"}}}
			results := []tool.Result{{Output: smallData}}

			out := eng.AppendToolResultsBatchToMessagesForTest(messages, calls, results)

			// Expect: original system + user, one assistant tool_call, one tool — no reminder.
			Expect(out).To(HaveLen(len(messages) + 2))
			for _, msg := range out {
				if msg.Role == "system" {
					Expect(strings.ToLower(msg.Content)).NotTo(ContainSubstring("reference material"),
						"no anchoring reminder should be injected for sub-threshold tool-result batches")
				}
			}
		})

		It("sums output across the batch when deciding whether to inject", func() {
			messages := []provider.Message{baseSys, userMsg}
			// Each result is below threshold individually but their sum is well above it.
			calls := []*provider.ToolCall{
				{ID: "c1", Name: "read", Arguments: map[string]any{"path": "/a"}},
				{ID: "c2", Name: "read", Arguments: map[string]any{"path": "/b"}},
				{ID: "c3", Name: "read", Arguments: map[string]any{"path": "/c"}},
			}
			results := []tool.Result{
				{Output: strings.Repeat("x", 2*1024)},
				{Output: strings.Repeat("y", 2*1024)},
				{Output: strings.Repeat("z", 2*1024)},
			}

			out := eng.AppendToolResultsBatchToMessagesForTest(messages, calls, results)

			// 1 assistant + 3 tool messages + 1 reminder = 5 appended.
			Expect(out).To(HaveLen(len(messages) + 5))
			reminder := out[len(out)-1]
			Expect(reminder.Role).To(Equal("system"))
			Expect(reminder.Content).To(ContainSubstring(baseUser))
		})
	})

	Context("when there is no user-role message in the history (defensive)", func() {
		It("does NOT inject a reminder because there is nothing to anchor on", func() {
			messages := []provider.Message{baseSys}
			calls := []*provider.ToolCall{{ID: "call_orphan", Name: "read", Arguments: map[string]any{"path": "/tmp/x"}}}
			results := []tool.Result{{Output: largeData}}

			out := eng.AppendToolResultsBatchToMessagesForTest(messages, calls, results)

			// No reminder when the function cannot identify a user-role message to quote.
			Expect(out).To(HaveLen(len(messages) + 2))
		})
	})
})

var _ = Describe("Default-assistant prompt re-anchoring directive", func() {
	It("includes a Turn Rule that anchors the assistant on the user's most recent user-role message", func() {
		// This is the prompt-side half of the fix. The engine injects a re-anchor
		// reminder after large tool-result batches; the prompt directive ensures
		// the model treats tool results as reference material at all times — not
		// just on turns where the threshold trips.
		body, err := promptBody("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		lower := strings.ToLower(body)
		Expect(lower).To(ContainSubstring("anchor"),
			"default-assistant prompt must contain an anchoring directive")
		Expect(lower).To(ContainSubstring("tool results"),
			"directive must explicitly call out tool results as the failure mode")
		Expect(lower).To(SatisfyAny(
			ContainSubstring("reference material"),
			ContainSubstring("not instructions"),
		), "directive must label tool results as reference material / not as user instructions")
	})
})
