package shared_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/shared"
)

var _ = Describe("BuildBaseToolSchema", func() {
	Context("when given a tool with all fields populated", func() {
		It("extracts name, description, properties, and required", func() {
			t := provider.Tool{
				Name:        "get_weather",
				Description: "Returns current weather",
				Schema: provider.ToolSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"location": map[string]interface{}{"type": "string"},
					},
					Required: []string{"location"},
				},
			}
			schema := shared.BuildBaseToolSchema(t)
			Expect(schema.Name).To(Equal("get_weather"))
			Expect(schema.Description).To(Equal("Returns current weather"))
			Expect(schema.Properties).To(HaveKey("location"))
			Expect(schema.Required).To(ConsistOf("location"))
		})
	})

	Context("when given a tool with no properties or required fields", func() {
		It("returns a schema with nil properties and empty required", func() {
			t := provider.Tool{
				Name:        "ping",
				Description: "Check connectivity",
				Schema:      provider.ToolSchema{Type: "object"},
			}
			schema := shared.BuildBaseToolSchema(t)
			Expect(schema.Name).To(Equal("ping"))
			Expect(schema.Description).To(Equal("Check connectivity"))
			Expect(schema.Properties).To(BeNil())
			Expect(schema.Required).To(BeNil())
		})
	})

	Context("when given a tool with multiple required fields", func() {
		It("preserves all required fields", func() {
			t := provider.Tool{
				Name:        "search",
				Description: "Search for things",
				Schema: provider.ToolSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"query": map[string]interface{}{"type": "string"},
						"limit": map[string]interface{}{"type": "integer"},
					},
					Required: []string{"query", "limit"},
				},
			}
			schema := shared.BuildBaseToolSchema(t)
			Expect(schema.Required).To(ConsistOf("query", "limit"))
		})
	})
})

var _ = Describe("ParseToolArguments", func() {
	It("parses a valid JSON string into a map", func() {
		args := `{"foo": "bar", "num": 42}`
		result := shared.ParseToolArguments(args)
		Expect(result).To(HaveKeyWithValue("foo", "bar"))
		Expect(result).To(HaveKeyWithValue("num", BeNumerically("==", 42)))
	})

	It("returns an empty map for an empty string", func() {
		result := shared.ParseToolArguments("")
		Expect(result).To(BeEmpty())
	})

	It("returns an empty map for invalid JSON", func() {
		result := shared.ParseToolArguments("not a json")
		Expect(result).To(BeEmpty())
	})
})
