package shared_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider/shared"
)

var _ = Describe("TranslateToolCallID", func() {
	Describe("target=anthropic", func() {
		It("passes a native anthropic id through unchanged", func() {
			out := shared.TranslateToolCallID("toolu_01ABC123def", shared.ToolIDTargetAnthropic)
			Expect(out).To(Equal("toolu_01ABC123def"))
		})

		It("rewrites an openai-style id to a toolu_-prefixed id", func() {
			out := shared.TranslateToolCallID("call_abc123", shared.ToolIDTargetAnthropic)
			Expect(out).To(HavePrefix("toolu_"))
			Expect(out).NotTo(Equal("call_abc123"))
		})

		It("rewrites an arbitrary non-prefixed id to a toolu_-prefixed id", func() {
			out := shared.TranslateToolCallID("some-opaque-id", shared.ToolIDTargetAnthropic)
			Expect(out).To(HavePrefix("toolu_"))
		})

		It("is deterministic for the same canonical id", func() {
			out1 := shared.TranslateToolCallID("call_xyz", shared.ToolIDTargetAnthropic)
			out2 := shared.TranslateToolCallID("call_xyz", shared.ToolIDTargetAnthropic)
			Expect(out1).To(Equal(out2))
		})

		It("produces distinct ids for distinct canonical inputs", func() {
			a := shared.TranslateToolCallID("call_xyz", shared.ToolIDTargetAnthropic)
			b := shared.TranslateToolCallID("call_abc", shared.ToolIDTargetAnthropic)
			Expect(a).NotTo(Equal(b))
		})

		It("returns empty string for empty input", func() {
			Expect(shared.TranslateToolCallID("", shared.ToolIDTargetAnthropic)).To(Equal(""))
		})
	})

	Describe("target=openai", func() {
		It("passes a native openai id through unchanged", func() {
			out := shared.TranslateToolCallID("call_abc123", shared.ToolIDTargetOpenAI)
			Expect(out).To(Equal("call_abc123"))
		})

		It("rewrites an anthropic-style id to a call_-prefixed id", func() {
			out := shared.TranslateToolCallID("toolu_01ABC123", shared.ToolIDTargetOpenAI)
			Expect(out).To(HavePrefix("call_"))
			Expect(out).NotTo(Equal("toolu_01ABC123"))
		})

		It("is deterministic for the same canonical id", func() {
			out1 := shared.TranslateToolCallID("toolu_01XYZ", shared.ToolIDTargetOpenAI)
			out2 := shared.TranslateToolCallID("toolu_01XYZ", shared.ToolIDTargetOpenAI)
			Expect(out1).To(Equal(out2))
		})

		It("returns empty string for empty input", func() {
			Expect(shared.TranslateToolCallID("", shared.ToolIDTargetOpenAI)).To(Equal(""))
		})
	})

	Describe("cross-provider-failover invariant", func() {
		It("produces identical wire ids for a tool_use and its matching tool_result when targeting anthropic", func() {
			canonical := "call_original_openai_id"
			useID := shared.TranslateToolCallID(canonical, shared.ToolIDTargetAnthropic)
			resultID := shared.TranslateToolCallID(canonical, shared.ToolIDTargetAnthropic)
			Expect(useID).To(Equal(resultID))
			Expect(useID).To(HavePrefix("toolu_"))
		})

		It("produces identical wire ids for a tool_use and its matching tool_result when targeting openai", func() {
			canonical := "toolu_01ORIGINAL"
			useID := shared.TranslateToolCallID(canonical, shared.ToolIDTargetOpenAI)
			resultID := shared.TranslateToolCallID(canonical, shared.ToolIDTargetOpenAI)
			Expect(useID).To(Equal(resultID))
			Expect(useID).To(HavePrefix("call_"))
		})
	})
})
