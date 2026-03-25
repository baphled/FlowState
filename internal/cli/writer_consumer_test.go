package cli_test

import (
	"bytes"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
)

var _ = Describe("WriterConsumer", func() {
	Describe("WriteChunk", func() {
		It("writes content to the writer and accumulates the response", func() {
			var buf bytes.Buffer
			consumer := cli.NewWriterConsumer(&buf, false)

			err := consumer.WriteChunk("Hello ")
			Expect(err).NotTo(HaveOccurred())
			err = consumer.WriteChunk("world")
			Expect(err).NotTo(HaveOccurred())

			Expect(buf.String()).To(Equal("Hello world"))
			Expect(consumer.Response()).To(Equal("Hello world"))
		})

		Context("when silent is true", func() {
			It("accumulates content but does not write to the writer", func() {
				var buf bytes.Buffer
				consumer := cli.NewWriterConsumer(&buf, true)

				err := consumer.WriteChunk("secret")
				Expect(err).NotTo(HaveOccurred())

				Expect(buf.String()).To(BeEmpty())
				Expect(consumer.Response()).To(Equal("secret"))
			})
		})
	})

	Describe("WriteError", func() {
		It("stores the error for later retrieval", func() {
			consumer := cli.NewWriterConsumer(io.Discard, false)

			consumer.WriteError(errors.New("something broke"))

			Expect(consumer.Err()).To(MatchError("something broke"))
		})
	})

	Describe("Done", func() {
		It("is a no-op that does not panic", func() {
			consumer := cli.NewWriterConsumer(io.Discard, false)

			Expect(func() { consumer.Done() }).NotTo(Panic())
		})
	})

	Describe("Response", func() {
		It("returns the accumulated content from multiple chunks", func() {
			consumer := cli.NewWriterConsumer(io.Discard, true)

			Expect(consumer.WriteChunk("a")).To(Succeed())
			Expect(consumer.WriteChunk("b")).To(Succeed())
			Expect(consumer.WriteChunk("c")).To(Succeed())

			Expect(consumer.Response()).To(Equal("abc"))
		})
	})

	Describe("WriteToolCall", func() {
		It("writes tool call name with emoji to the writer", func() {
			var buf bytes.Buffer
			consumer := cli.NewWriterConsumer(&buf, false)

			consumer.WriteToolCall("bash")

			Expect(buf.String()).To(Equal("🔧 bash...\n"))
		})

		It("writes skill call with book emoji and no ellipsis", func() {
			var buf bytes.Buffer
			consumer := cli.NewWriterConsumer(&buf, false)

			consumer.WriteToolCall("skill:pre-action")

			Expect(buf.String()).To(Equal("📚 pre-action\n"))
		})

		Context("when silent is true", func() {
			It("does not write to the writer", func() {
				var buf bytes.Buffer
				consumer := cli.NewWriterConsumer(&buf, true)

				consumer.WriteToolCall("bash")

				Expect(buf.String()).To(BeEmpty())
			})

			It("does not write skill calls either", func() {
				var buf bytes.Buffer
				consumer := cli.NewWriterConsumer(&buf, true)

				consumer.WriteToolCall("skill:memory-keeper")

				Expect(buf.String()).To(BeEmpty())
			})
		})
	})

	Describe("WriteToolResult", func() {
		It("writes tool result content with emoji to the writer", func() {
			var buf bytes.Buffer
			consumer := cli.NewWriterConsumer(&buf, false)

			consumer.WriteToolResult("success")

			Expect(buf.String()).To(Equal("📤 success\n"))
		})

		Context("when silent is true", func() {
			It("does not write to the writer", func() {
				var buf bytes.Buffer
				consumer := cli.NewWriterConsumer(&buf, true)

				consumer.WriteToolResult("success")

				Expect(buf.String()).To(BeEmpty())
			})
		})
	})
})
