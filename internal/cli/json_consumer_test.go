package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("CLIJSONConsumer", func() {
	Describe("WriteChunk", func() {
		It("writes a JSON line with type chunk and content", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			err := consumer.WriteChunk("Hello ")
			Expect(err).NotTo(HaveOccurred())
			err = consumer.WriteChunk("world")
			Expect(err).NotTo(HaveOccurred())

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(2))

			var firstEvent map[string]string
			err = json.Unmarshal([]byte(lines[0]), &firstEvent)
			Expect(err).NotTo(HaveOccurred())
			Expect(firstEvent["type"]).To(Equal("chunk"))
			Expect(firstEvent["content"]).To(Equal("Hello "))

			var secondEvent map[string]string
			err = json.Unmarshal([]byte(lines[1]), &secondEvent)
			Expect(err).NotTo(HaveOccurred())
			Expect(secondEvent["type"]).To(Equal("chunk"))
			Expect(secondEvent["content"]).To(Equal("world"))

			Expect(consumer.Response()).To(Equal("Hello world"))
		})
	})

	Describe("WriteError", func() {
		It("writes a JSON line with type error and stores the error", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			consumer.WriteError(errors.New("something broke"))

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var event map[string]string
			err := json.Unmarshal([]byte(lines[0]), &event)
			Expect(err).NotTo(HaveOccurred())
			Expect(event["type"]).To(Equal("error"))
			Expect(event["error"]).To(Equal("something broke"))

			Expect(consumer.Err()).To(MatchError("something broke"))
		})
	})

	Describe("Done", func() {
		It("writes a JSON line with type done", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			consumer.Done()

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var event map[string]string
			err := json.Unmarshal([]byte(lines[0]), &event)
			Expect(err).NotTo(HaveOccurred())
			Expect(event["type"]).To(Equal("done"))
		})
	})

	Describe("Response", func() {
		It("returns the accumulated content from multiple chunks", func() {
			consumer := cli.NewJSONConsumer(io.Discard)

			Expect(consumer.WriteChunk("a")).To(Succeed())
			Expect(consumer.WriteChunk("b")).To(Succeed())
			Expect(consumer.WriteChunk("c")).To(Succeed())

			Expect(consumer.Response()).To(Equal("abc"))
		})
	})

	Describe("WriteToolCall", func() {
		It("writes a JSON line with type tool_call and the tool name", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			consumer.WriteToolCall("search")

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var event map[string]string
			err := json.Unmarshal([]byte(lines[0]), &event)
			Expect(err).NotTo(HaveOccurred())
			Expect(event["type"]).To(Equal("tool_call"))
			Expect(event["name"]).To(Equal("search"))
		})

		It("preserves skill: prefix in tool name", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			consumer.WriteToolCall("skill:golang")

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var event map[string]string
			err := json.Unmarshal([]byte(lines[0]), &event)
			Expect(err).NotTo(HaveOccurred())
			Expect(event["name"]).To(Equal("skill:golang"))
		})
	})

	Describe("WriteToolResult", func() {
		It("writes a JSON line with type tool_result and content", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			consumer.WriteToolResult("search results here")

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var event map[string]string
			err := json.Unmarshal([]byte(lines[0]), &event)
			Expect(err).NotTo(HaveOccurred())
			Expect(event["type"]).To(Equal("tool_result"))
			Expect(event["content"]).To(Equal("search results here"))
		})
	})

	Describe("WriteDelegation", func() {
		It("writes a JSON line with type delegation and all fields", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			event := streaming.DelegationEvent{
				SourceAgent:  "planner",
				TargetAgent:  "worker",
				Status:       "started",
				ModelName:    "claude-3-5-sonnet",
				ProviderName: "anthropic",
			}
			err := consumer.WriteDelegation(event)
			Expect(err).NotTo(HaveOccurred())

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var delegation map[string]string
			err = json.Unmarshal([]byte(lines[0]), &delegation)
			Expect(err).NotTo(HaveOccurred())
			Expect(delegation["type"]).To(Equal("delegation"))
			Expect(delegation["source"]).To(Equal("planner"))
			Expect(delegation["target"]).To(Equal("worker"))
			Expect(delegation["status"]).To(Equal("started"))
			Expect(delegation["model"]).To(Equal("claude-3-5-sonnet"))
			Expect(delegation["provider"]).To(Equal("anthropic"))
		})

		It("omits provider when empty", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			event := streaming.DelegationEvent{
				SourceAgent: "planner",
				TargetAgent: "worker",
				Status:      "completed",
				ModelName:   "claude-3-5-sonnet",
			}
			err := consumer.WriteDelegation(event)
			Expect(err).NotTo(HaveOccurred())

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(1))

			var delegation map[string]string
			err = json.Unmarshal([]byte(lines[0]), &delegation)
			Expect(err).NotTo(HaveOccurred())
			Expect(delegation["provider"]).To(BeEmpty())
		})
	})

	Describe("complete NDJSON stream", func() {
		It("produces valid JSON on each line", func() {
			var buf bytes.Buffer
			consumer := cli.NewJSONConsumer(&buf)

			Expect(consumer.WriteChunk("Hello ")).To(Succeed())
			consumer.WriteToolCall("skill:search")
			Expect(consumer.WriteChunk("results")).To(Succeed())
			consumer.WriteToolResult("found 5 items")
			consumer.WriteError(errors.New("warning: rate limited"))
			consumer.Done()

			lines := linesFromBuffer(buf)
			Expect(lines).To(HaveLen(6))

			for i, line := range lines {
				var parsed map[string]interface{}
				err := json.Unmarshal([]byte(line), &parsed)
				Expect(err).NotTo(HaveOccurred(), "line %d should be valid JSON", i)
			}
		})
	})
})

func linesFromBuffer(buf bytes.Buffer) []string {
	content := buf.String()
	if content == "" {
		return nil
	}
	var lines []string
	for _, line := range splitLines(content) {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func splitLines(s string) []string {
	var lines []string
	var current []rune
	for _, r := range s {
		if r == '\n' {
			lines = append(lines, string(current))
			current = nil
		} else {
			current = append(current, r)
		}
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines
}
