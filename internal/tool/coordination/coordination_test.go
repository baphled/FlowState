package coordination_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/coordination"
)

var _ = Describe("CoordinationTool", func() {
	var (
		toolInstance *coordination.Tool
		tempDir      string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "coordination-tool-test-*")
		Expect(err).NotTo(HaveOccurred())
		storePath := filepath.Join(tempDir, "coordination.json")
		toolInstance = coordination.New(storePath)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("Name", func() {
		It("returns the tool name", func() {
			Expect(toolInstance.Name()).To(Equal("coordination_store"))
		})
	})

	Describe("Description", func() {
		It("returns a human-readable description", func() {
			desc := toolInstance.Description()
			Expect(desc).To(ContainSubstring("coordination"))
			Expect(desc).To(ContainSubstring("store"))
		})
	})

	Describe("Schema", func() {
		It("returns the JSON schema", func() {
			schema := toolInstance.Schema()
			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Properties).To(HaveKey("operation"))
			Expect(schema.Properties).To(HaveKey("key"))
			Expect(schema.Required).To(ContainElement("operation"))
			Expect(schema.Required).To(ContainElement("key"))
		})
	})

	Describe("Execute", func() {
		Context("with get operation", func() {
			BeforeEach(func() {
				_, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "test-key",
						"value":     "test-value",
					},
				})
				Expect(err).NotTo(HaveOccurred())
			})

			It("retrieves a value by key", func() {
				result, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "get",
						"key":       "test-key",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("test-value"))
			})

			It("returns error for non-existent key", func() {
				_, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "get",
						"key":       "nonexistent",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("not found"))
			})
		})

		Context("with set operation", func() {
			It("stores a key-value pair", func() {
				result, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "new-key",
						"value":     "new-value",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("new-key"))
			})

			It("overwrites an existing key", func() {
				_, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "key",
						"value":     "value1",
					},
				})
				Expect(err).NotTo(HaveOccurred())

				result, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "set",
						"key":       "key",
						"value":     "value2",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("key"))
				Expect(result.Output).To(ContainSubstring("stored"))

				// Verify the value was overwritten by getting it
				getResult, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "get",
						"key":       "key",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(getResult.Output).To(ContainSubstring("value2"))
			})
		})

		Context("with list operation", func() {
			BeforeEach(func() {
				entries := map[string]string{
					"chain1/requirements": "reqs1",
					"chain1/interview":    "interview1",
					"chain2/requirements": "reqs2",
				}
				for k, v := range entries {
					_, err := toolInstance.Execute(context.Background(), tool.Input{
						Arguments: map[string]interface{}{
							"operation": "set",
							"key":       k,
							"value":     v,
						},
					})
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("lists all keys with empty prefix", func() {
				result, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "list",
						"key":       "",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("chain1/requirements"))
				Expect(result.Output).To(ContainSubstring("chain1/interview"))
				Expect(result.Output).To(ContainSubstring("chain2/requirements"))
			})

			It("lists keys with prefix", func() {
				result, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "list",
						"key":       "chain1/",
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("chain1/requirements"))
				Expect(result.Output).To(ContainSubstring("chain1/interview"))
				Expect(result.Output).NotTo(ContainSubstring("chain2/"))
			})
		})

		Context("with missing arguments", func() {
			It("returns error when operation is missing", func() {
				_, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"key": "test",
					},
				})
				Expect(err).To(HaveOccurred())
			})

			It("returns error when key is missing", func() {
				_, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "get",
					},
				})
				Expect(err).To(HaveOccurred())
			})

			It("returns error for unknown operation", func() {
				_, err := toolInstance.Execute(context.Background(), tool.Input{
					Arguments: map[string]interface{}{
						"operation": "unknown",
						"key":       "test",
					},
				})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("unknown operation"))
			})
		})
	})

	Describe("JSON output", func() {
		It("returns valid JSON for get operation", func() {
			_, err := toolInstance.Execute(context.Background(), tool.Input{
				Arguments: map[string]interface{}{
					"operation": "set",
					"key":       "json-test",
					"value":     `{"nested":"value"}`,
				},
			})
			Expect(err).NotTo(HaveOccurred())

			result, err := toolInstance.Execute(context.Background(), tool.Input{
				Arguments: map[string]interface{}{
					"operation": "get",
					"key":       "json-test",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			var output map[string]interface{}
			err = json.Unmarshal([]byte(result.Output), &output)
			Expect(err).NotTo(HaveOccurred()) // Output should be valid JSON
			Expect(output).To(HaveKey("key"))
			Expect(output).To(HaveKey("value"))
		})
	})
})
