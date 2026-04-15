package context_test

import (
	"errors"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	flowctx "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("RenderSummaryPrompt", func() {
	Describe("empty input", func() {
		It("returns ErrEmptySummaryInput when msgs is nil", func() {
			out, err := flowctx.RenderSummaryPrompt(nil)

			Expect(out).To(BeEmpty())
			Expect(errors.Is(err, flowctx.ErrEmptySummaryInput)).To(BeTrue())
		})

		It("returns ErrEmptySummaryInput when msgs is an empty slice", func() {
			out, err := flowctx.RenderSummaryPrompt([]provider.Message{})

			Expect(out).To(BeEmpty())
			Expect(errors.Is(err, flowctx.ErrEmptySummaryInput)).To(BeTrue())
		})
	})

	Describe("happy-path rendering", func() {
		var msgs []provider.Message

		BeforeEach(func() {
			msgs = []provider.Message{
				{Role: "user", Content: "Refactor WindowBuilder to support L2 summaries."},
				{Role: "assistant", Content: "Plan: add CompactionSummary struct, prompt template, resolver."},
			}
		})

		It("produces a non-empty prompt string without error", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).NotTo(BeEmpty())
		})

		It("contains the forbidding-ids directive verbatim", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			// The exact phrasing called out in the task spec must be present —
			// any rewording invalidates the regex scrubber's assumptions.
			Expect(out).To(ContainSubstring("Do NOT include any tool_use_id or tool_call_id values"))
			Expect(out).To(ContainSubstring("toolu_"))
			Expect(out).To(ContainSubstring("call_"))
			Expect(out).To(ContainSubstring("Refer to tool calls by name and purpose only"))
		})

		It("references every LLM-authored CompactionSummary field by name", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			// `compacted_at` is intentionally excluded: the caller stamps
			// that field server-side after parse, so the prompt must not
			// teach the model to emit it (any non-RFC3339 value breaks
			// json.Unmarshal into time.Time).
			for _, field := range []string{
				"intent",
				"key_decisions",
				"errors",
				"next_steps",
				"files_to_restore",
				"original_token_count",
				"summary_token_count",
			} {
				Expect(out).To(ContainSubstring("`" + field + "`"))
			}
		})

		It("does not mention `compacted_at` or the legacy placeholder", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).NotTo(ContainSubstring("compacted_at"))
			Expect(out).NotTo(ContainSubstring("PLACEHOLDER_COMPACTED_AT"))
		})

		It("instructs JSON-only output with no preamble or fences", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("No preamble"))
			Expect(out).To(ContainSubstring("No trailing commentary"))
			Expect(out).To(ContainSubstring("No markdown code fences"))
		})

		It("instructs the model to use relative paths for files_to_restore", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("relative paths from the repository root"))
		})

		It("embeds the user's message content into the transcript block", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("Refactor WindowBuilder to support L2 summaries."))
			Expect(out).To(ContainSubstring("Plan: add CompactionSummary struct"))
		})

		It("renders the message count into the prompt", func() {
			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("2 message(s)"))
		})
	})

	Describe("tool-call id handling", func() {
		It("does not echo raw tool_use_id strings into the rendered prompt", func() {
			// A model that accidentally stored a toolu_ id on a ToolCall should
			// not have that id leak into the prompt. The renderer references
			// tool calls by name only.
			msgs := []provider.Message{
				{
					Role:    "assistant",
					Content: "Calling two tools in parallel.",
					ToolCalls: []provider.ToolCall{
						{ID: "toolu_01ABCxyz_never_emit_me", Name: "read_file"},
						{ID: "call_987_never_emit_me", Name: "grep"},
					},
				},
				{Role: "tool", Content: "file contents redacted"},
				{Role: "tool", Content: "grep results redacted"},
			}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			// Tool names ARE referenced.
			Expect(out).To(ContainSubstring("read_file"))
			Expect(out).To(ContainSubstring("grep"))
			// Specific id values MUST NOT leak.
			Expect(out).NotTo(ContainSubstring("toolu_01ABCxyz_never_emit_me"))
			Expect(out).NotTo(ContainSubstring("call_987_never_emit_me"))
		})

		It("passes user-supplied toolu_ ids in content through but keeps the directive", func() {
			// If a user message happens to contain a toolu_ string literal
			// (for example when debugging), the renderer does not strip it —
			// that is the downstream scrubber's job. What we assert here is
			// that the "do not emit ids" directive remains in the prompt so
			// the model is told not to repeat them.
			msgs := []provider.Message{
				{Role: "user", Content: "Why did toolu_01XYZ fail earlier?"},
			}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("toolu_01XYZ"))
			Expect(out).To(ContainSubstring("Do NOT include any tool_use_id or tool_call_id values"))
		})
	})

	Describe("message formatting", func() {
		It("labels messages with an index and role header", func() {
			msgs := []provider.Message{
				{Role: "user", Content: "first"},
				{Role: "assistant", Content: "second"},
			}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("[0] user:"))
			Expect(out).To(ContainSubstring("[1] assistant:"))
		})

		It("falls back to 'unknown' when a message role is empty", func() {
			msgs := []provider.Message{
				{Role: "", Content: "orphan"},
			}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("[0] unknown:"))
		})

		It("includes thinking blocks when present", func() {
			msgs := []provider.Message{
				{Role: "assistant", Thinking: "considering trade-offs", Content: "I'll proceed."},
			}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("(thinking) considering trade-offs"))
			Expect(out).To(ContainSubstring("I'll proceed."))
		})

		It("describes tool calls by name even when content is empty", func() {
			msgs := []provider.Message{
				{
					Role: "assistant",
					ToolCalls: []provider.ToolCall{
						{ID: "toolu_ignored", Name: "bash"},
					},
				},
			}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(out).To(ContainSubstring("(tool call) bash"))
			Expect(out).NotTo(ContainSubstring("toolu_ignored"))
		})
	})

	Describe("stability", func() {
		It("is deterministic across repeated calls with the same input", func() {
			msgs := []provider.Message{
				{Role: "user", Content: "question"},
				{Role: "assistant", Content: "answer"},
			}

			first, err := flowctx.RenderSummaryPrompt(msgs)
			Expect(err).NotTo(HaveOccurred())

			second, err := flowctx.RenderSummaryPrompt(msgs)
			Expect(err).NotTo(HaveOccurred())

			Expect(first).To(Equal(second))
		})

		It("ends with an instruction to produce only the JSON object", func() {
			msgs := []provider.Message{{Role: "user", Content: "x"}}

			out, err := flowctx.RenderSummaryPrompt(msgs)

			Expect(err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(out)).To(HaveSuffix("Produce only the JSON object."))
		})
	})
})

var _ = Describe("RenderSummaryPrompt template execution failure", func() {
	It("wraps template execution errors with a package-scoped prefix", func() {
		restore := flowctx.ExportedSwapSummaryPromptTemplate()
		defer restore()

		out, err := flowctx.RenderSummaryPrompt([]provider.Message{
			{Role: "user", Content: "forcing an execute failure"},
		})

		Expect(out).To(BeEmpty())
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("context: execute summary prompt template"))
	})
})

var _ = Describe("SummaryPromptSystem", func() {
	It("reinforces the JSON-only contract", func() {
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("JSON"))
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("CompactionSummary"))
	})

	It("restates the forbidding-ids directive", func() {
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("toolu_"))
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("call_"))
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("MUST NOT"))
	})

	It("forbids markdown fences and prose", func() {
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("markdown"))
		Expect(flowctx.SummaryPromptSystem).To(ContainSubstring("prose"))
	})
})
