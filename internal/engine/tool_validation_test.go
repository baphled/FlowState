package engine_test

import (
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/tool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("ValidateToolArgs", func() {
	var schema tool.Schema

	BeforeEach(func() {
		schema = tool.Schema{
			Type: "object",
			Properties: map[string]tool.Property{
				"query": {Type: "string", Description: "search query"},
				"top_k": {Type: "integer", Description: "result count"},
			},
			Required: []string{"query"},
		}
	})

	Context("with valid arguments", func() {
		It("returns sanitised args without error", func() {
			args := map[string]interface{}{"query": "hello", "top_k": float64(5)}
			result, err := engine.ValidateToolArgs(schema, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveKey("query"))
			Expect(result).To(HaveKey("top_k"))
		})
	})

	Context("with unknown arguments", func() {
		It("strips unknown keys", func() {
			args := map[string]interface{}{
				"query":         "hello",
				"session_id":    "1234567890",
				"subagent_type": "query_vault",
			}
			result, err := engine.ValidateToolArgs(schema, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveKey("query"))
			Expect(result).NotTo(HaveKey("session_id"))
			Expect(result).NotTo(HaveKey("subagent_type"))
		})
	})

	Context("with missing required arguments", func() {
		It("returns an error", func() {
			args := map[string]interface{}{"top_k": float64(5)}
			_, err := engine.ValidateToolArgs(schema, args)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("query"))
		})
	})

	Context("with empty schema properties", func() {
		It("passes through all arguments", func() {
			emptySchema := tool.Schema{Type: "object"}
			args := map[string]interface{}{"anything": "goes"}
			result, err := engine.ValidateToolArgs(emptySchema, args)
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(HaveKey("anything"))
		})
	})
})
