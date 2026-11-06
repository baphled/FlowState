package app

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// TestAgentSchemaConformance guards schema drift: each built-in
// schema must stay a JSON object whose required properties the tool
// actually decodes (principal review requirement).
var _ = Describe("agent tool schemas", func() {
	DescribeTable("schema is a valid object schema for known tools",
		func(name string) {
			schema := agentSchemas()[name]
			var v map[string]any
			Expect(json.Unmarshal([]byte(schema), &v)).To(Succeed())
			Expect(v["type"]).To(Equal("object"))
		},
		Entry("fs.read", "fs.read"),
		Entry("fs.write", "fs.write"),
		Entry("fs.edit", "fs.edit"),
		Entry("exec.run", "exec.run"),
	)

	It("keeps fs.read required fields aligned with the tool decoder", func() {
		var v struct {
			Required []string `json:"required"`
		}
		Expect(json.Unmarshal([]byte(agentSchemas()["fs.read"]), &v)).To(Succeed())
		Expect(v.Required).To(ConsistOf("path"))
	})

	It("keeps fs.edit required fields aligned with the tool decoder", func() {
		var v struct {
			Required []string `json:"required"`
		}
		Expect(json.Unmarshal([]byte(agentSchemas()["fs.edit"]), &v)).To(Succeed())
		Expect(v.Required).To(ConsistOf("path", "old_text", "new_text"))
	})
})
