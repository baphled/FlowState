package lsp_test

import (
	"context"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/lsp"
)

func TestLSPTool(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "LSP Tool Suite")
}

var _ = Describe("LSP Tool", func() {
	var lspTool tool.Tool

	BeforeEach(func() {
		lspTool = lsp.New()
	})

	Describe("Name", func() {
		It("returns lsp", func() {
			Expect(lspTool.Name()).To(Equal("lsp"))
		})
	})

	Describe("Description", func() {
		It("describes the supported LSP operations", func() {
			Expect(lspTool.Description()).To(ContainSubstring("diagnostics"))
			Expect(lspTool.Description()).To(ContainSubstring("symbols"))
			Expect(lspTool.Description()).To(ContainSubstring("goto definition"))
			Expect(lspTool.Description()).To(ContainSubstring("find references"))
		})
	})

	Describe("Schema", func() {
		It("requires operation and path", func() {
			schema := lspTool.Schema()
			Expect(schema.Type).To(Equal("object"))
			Expect(schema.Required).To(ConsistOf("operation", "path"))
		})

		It("exposes the LSP request fields", func() {
			schema := lspTool.Schema()
			Expect(schema.Properties).To(HaveKey("operation"))
			Expect(schema.Properties).To(HaveKey("path"))
			Expect(schema.Properties).To(HaveKey("line"))
			Expect(schema.Properties).To(HaveKey("column"))
			Expect(schema.Properties).To(HaveKey("query"))
			Expect(schema.Properties).To(HaveKey("context"))
		})

		It("limits operations to the supported LSP verbs", func() {
			schema := lspTool.Schema()
			Expect(schema.Properties["operation"].Enum).To(ConsistOf("diagnostics", "symbols", "goto", "find-references"))
		})
	})

	DescribeTable("Execute",
		func(args map[string]interface{}, wantOutput bool, wantToolError bool) {
			result, err := lspTool.Execute(context.Background(), tool.Input{Name: "lsp", Arguments: args})

			Expect(err).NotTo(HaveOccurred())
			if wantOutput {
				Expect(result.Output).NotTo(BeEmpty())
			}
			if wantToolError {
				Expect(result.Error).To(HaveOccurred())
			} else {
				Expect(result.Error).NotTo(HaveOccurred())
			}
		},
		Entry("returns diagnostics for a file", map[string]interface{}{
			"operation": "diagnostics",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
		}, true, false),
		Entry("rejects diagnostics without a path", map[string]interface{}{
			"operation": "diagnostics",
		}, false, true),
		Entry("rejects an unsupported operation", map[string]interface{}{
			"operation": "rename",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
		}, false, true),
		Entry("returns symbols for a file and query", map[string]interface{}{
			"operation": "symbols",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
			"query":     "Execute",
		}, true, false),
		Entry("rejects symbols with a malformed query", map[string]interface{}{
			"operation": "symbols",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
			"query":     42,
		}, false, true),
		Entry("returns goto definition results for a location", map[string]interface{}{
			"operation": "goto",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
			"line":      20,
			"column":    1,
		}, true, false),
		Entry("rejects goto without coordinates", map[string]interface{}{
			"operation": "goto",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
		}, false, true),
		Entry("returns references for a symbol location", map[string]interface{}{
			"operation": "find-references",
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
			"line":      20,
			"column":    1,
		}, true, false),
		Entry("rejects find-references with a missing file", map[string]interface{}{
			"operation": "find-references",
			"path":      filepath.Join("missing", "does-not-exist.go"),
			"line":      1,
			"column":    1,
		}, false, true),
		Entry("rejects a non-string operation", map[string]interface{}{
			"operation": 99,
			"path":      filepath.Join("internal", "tool", "lsp", "lsp.go"),
		}, false, true),
	)
})
