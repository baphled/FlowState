package api_test

import (
	"errors"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/api"
)

var _ = Describe("SSEConsumer", func() {
	var recorder *httptest.ResponseRecorder

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
	})

	Describe("NewSSEConsumer", func() {
		It("returns a consumer and true for a flushable writer", func() {
			consumer, ok := api.NewSSEConsumer(recorder)

			Expect(ok).To(BeTrue())
			Expect(consumer).NotTo(BeNil())
		})
	})

	Describe("WriteChunk", func() {
		It("writes JSON content as an SSE data line", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			err := consumer.WriteChunk("Hello")

			Expect(err).NotTo(HaveOccurred())
			Expect(recorder.Body.String()).To(ContainSubstring(`data: {"content":"Hello"}`))
		})

		It("flushes after writing", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			err := consumer.WriteChunk("world")

			Expect(err).NotTo(HaveOccurred())
			Expect(recorder.Flushed).To(BeTrue())
		})
	})

	Describe("WriteError", func() {
		It("writes JSON error as an SSE data line", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteError(errors.New("something broke"))

			Expect(recorder.Body.String()).To(ContainSubstring(`data: {"error":"something broke"}`))
		})
	})

	Describe("Done", func() {
		It("writes the DONE sentinel as an SSE data line", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.Done()

			Expect(recorder.Body.String()).To(ContainSubstring("data: [DONE]"))
		})
	})

	Describe("WriteToolCall", func() {
		It("writes JSON tool call as an SSE data line", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteToolCall("search_web")

			Expect(recorder.Body.String()).To(ContainSubstring(`data: {"type":"tool_call","name":"search_web","status":"running"}`))
		})

		It("writes JSON skill load as an SSE data line with skill type", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteToolCall("skill:pre-action")

			Expect(recorder.Body.String()).To(ContainSubstring(`data: {"type":"skill_load","name":"pre-action"}`))
		})

		It("flushes after writing", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteToolCall("search_web")

			Expect(recorder.Flushed).To(BeTrue())
		})

		It("flushes after writing skill calls", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteToolCall("skill:memory-keeper")

			Expect(recorder.Flushed).To(BeTrue())
		})
	})

	Describe("WriteToolResult", func() {
		It("writes JSON tool result as an SSE data line", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteToolResult("result data")

			Expect(recorder.Body.String()).To(ContainSubstring(`data: {"type":"tool_result","content":"result data"}`))
		})

		It("flushes after writing", func() {
			consumer, ok := api.NewSSEConsumer(recorder)
			Expect(ok).To(BeTrue())

			consumer.WriteToolResult("result data")

			Expect(recorder.Flushed).To(BeTrue())
		})
	})
})
