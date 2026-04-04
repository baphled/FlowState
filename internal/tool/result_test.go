package tool_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("Result", func() {
	Describe("Title field", func() {
		It("defaults to empty string when not set", func() {
			result := tool.Result{Output: "some output"}
			Expect(result.Title).To(BeEmpty())
		})

		It("stores a human-readable title", func() {
			result := tool.Result{
				Output: "delegate response",
				Title:  "Delegating to QA agent",
			}
			Expect(result.Title).To(Equal("Delegating to QA agent"))
		})
	})

	Describe("Metadata field", func() {
		It("defaults to nil when not set", func() {
			result := tool.Result{Output: "some output"}
			Expect(result.Metadata).To(BeNil())
		})

		It("stores arbitrary metadata", func() {
			result := tool.Result{
				Output: "delegate response",
				Metadata: map[string]interface{}{
					"sessionId": "ses_abc123",
					"model":     "claude-opus-4-5",
					"provider":  "anthropic",
				},
			}
			Expect(result.Metadata).To(HaveKey("sessionId"))
			Expect(result.Metadata["sessionId"]).To(Equal("ses_abc123"))
			Expect(result.Metadata["model"]).To(Equal("claude-opus-4-5"))
			Expect(result.Metadata["provider"]).To(Equal("anthropic"))
		})
	})

	Describe("backward compatibility", func() {
		It("existing Output and Error fields remain accessible", func() {
			result := tool.Result{
				Output: "hello",
				Error:  nil,
			}
			Expect(result.Output).To(Equal("hello"))
			Expect(result.Error).ToNot(HaveOccurred())
		})
	})
})
