package provider

import (
	"errors"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ToolResultInfo", func() {
	It("stores content and error state", func() {
		info := &ToolResultInfo{
			Content: "output",
			IsError: false,
		}

		Expect(info.Content).To(Equal("output"))
		Expect(info.IsError).To(BeFalse())
	})
})

var _ = Describe("StreamChunk with tool result", func() {
	It("stores the tool result details", func() {
		chunk := StreamChunk{
			Content:   "test",
			Done:      false,
			EventType: "tool_result",
			ToolResult: &ToolResultInfo{
				Content: "tool output",
				IsError: false,
			},
		}

		Expect(chunk.ToolResult).NotTo(BeNil())
		Expect(chunk.ToolResult.Content).To(Equal("tool output"))
	})
})

var _ = Describe("StreamChunk ToolCallID (P2 T1)", func() {
	It("stores the upstream provider's tool-use ID on the chunk", func() {
		chunk := StreamChunk{
			EventType:  "tool_call",
			ToolCallID: "toolu_01ABCDEF",
			ToolCall:   &ToolCall{ID: "toolu_01ABCDEF", Name: "bash"},
		}

		Expect(chunk.ToolCallID).To(Equal("toolu_01ABCDEF"))
	})

	It("stores the tool-call ID on a tool_result chunk so the intent layer can correlate", func() {
		chunk := StreamChunk{
			EventType:  "tool_result",
			ToolCallID: "toolu_01ABCDEF",
			ToolResult: &ToolResultInfo{Content: "output"},
		}

		Expect(chunk.ToolCallID).To(Equal("toolu_01ABCDEF"))
		Expect(chunk.ToolResult).NotTo(BeNil())
	})

	It("is the empty string on a zero-value chunk", func() {
		chunk := StreamChunk{}
		Expect(chunk.ToolCallID).To(Equal(""))
	})
})

var _ = Describe("DelegationInfo", func() {
	It("stores delegation metadata", func() {
		startedAt := time.Now().UTC().Truncate(time.Second)
		completedAt := startedAt.Add(2 * time.Second)

		info := &DelegationInfo{
			SourceAgent:  "planner",
			TargetAgent:  "explorer",
			ChainID:      "chain-1",
			ToolCalls:    3,
			LastTool:     "delegate",
			StartedAt:    &startedAt,
			CompletedAt:  &completedAt,
			Status:       "started",
			ModelName:    "claude-sonnet-4-6",
			ProviderName: "anthropic",
			Description:  "Exploring codebase for requirements",
		}

		Expect(info.SourceAgent).To(Equal("planner"))
		Expect(info.TargetAgent).To(Equal("explorer"))
		Expect(info.ChainID).To(Equal("chain-1"))
		Expect(info.ToolCalls).To(Equal(3))
		Expect(info.LastTool).To(Equal("delegate"))
		Expect(info.StartedAt).To(BeComparableTo(&startedAt))
		Expect(info.CompletedAt).To(BeComparableTo(&completedAt))
		Expect(info.Status).To(Equal("started"))
		Expect(info.ModelName).To(Equal("claude-sonnet-4-6"))
		Expect(info.ProviderName).To(Equal("anthropic"))
		Expect(info.Description).To(Equal("Exploring codebase for requirements"))
	})
})

var _ = Describe("StreamChunk with delegation info", func() {
	It("stores the delegation details", func() {
		chunk := StreamChunk{
			Content:   "",
			Done:      false,
			EventType: "delegation",
			DelegationInfo: &DelegationInfo{
				SourceAgent:  "planner",
				TargetAgent:  "plan-writer",
				ChainID:      "chain-2",
				ToolCalls:    1,
				LastTool:     "delegate",
				Status:       "started",
				ModelName:    "claude-sonnet-4-6",
				ProviderName: "anthropic",
				Description:  "Writing plan",
			},
		}

		Expect(chunk.DelegationInfo).NotTo(BeNil())
		Expect(chunk.DelegationInfo.SourceAgent).To(Equal("planner"))
		Expect(chunk.DelegationInfo.TargetAgent).To(Equal("plan-writer"))
		Expect(chunk.DelegationInfo.ChainID).To(Equal("chain-2"))
		Expect(chunk.DelegationInfo.ToolCalls).To(Equal(1))
		Expect(chunk.DelegationInfo.LastTool).To(Equal("delegate"))
		Expect(chunk.DelegationInfo.Status).To(Equal("started"))
	})
})

var _ = Describe("StreamChunk delegation info", func() {
	It("is nil by default", func() {
		chunk := StreamChunk{
			Content: "hello",
			Done:    false,
		}

		Expect(chunk.DelegationInfo).To(BeNil())
	})
})

var _ = Describe("Error", func() {
	It("satisfies the error interface", func() {
		var _ error = &Error{}
	})

	It("unwraps to the raw error", func() {
		rawErr := errors.New("root cause")
		provErr := &Error{RawError: rawErr}

		Expect(provErr.Unwrap()).To(BeIdenticalTo(rawErr))
		Expect(errors.Is(provErr, rawErr)).To(BeTrue())
	})

	It("supports errors.As through one level of wrapping", func() {
		rawErr := errors.New("root cause")
		provErr := &Error{Provider: "zai", ErrorType: ErrorTypeBilling, RawError: rawErr}

		wrapped := fmt.Errorf("outer: %w", provErr)

		var target *Error
		Expect(errors.As(wrapped, &target)).To(BeTrue())
		Expect(target).To(BeIdenticalTo(provErr))
	})

	It("supports errors.As through two levels of wrapping", func() {
		rawErr := errors.New("root cause")
		provErr := &Error{Provider: "anthropic", ErrorType: ErrorTypeRateLimit, RawError: rawErr}

		wrapped := fmt.Errorf("level 2: %w", fmt.Errorf("level 1: %w", provErr))

		var target *Error
		Expect(errors.As(wrapped, &target)).To(BeTrue())
		Expect(target).To(BeIdenticalTo(provErr))
	})

	It("supports errors.As through three levels of wrapping", func() {
		rawErr := errors.New("root cause")
		provErr := &Error{Provider: "openzen", ErrorType: ErrorTypeServerError, RawError: rawErr}

		wrapped := fmt.Errorf("level 3: %w", fmt.Errorf("level 2: %w", fmt.Errorf("level 1: %w", provErr)))

		var target *Error
		Expect(errors.As(wrapped, &target)).To(BeTrue())
		Expect(target).To(BeIdenticalTo(provErr))
	})

	It("allows errors.Is to reach the raw error through wrapping", func() {
		rawErr := errors.New("root cause")
		provErr := &Error{Provider: "zai", ErrorType: ErrorTypeBilling, RawError: rawErr}

		wrapped := fmt.Errorf("wrapped: %w", provErr)

		Expect(errors.Is(wrapped, rawErr)).To(BeTrue())
	})
})
