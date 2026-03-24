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
})
