package bash_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/tool"
	"github.com/baphled/flowstate/internal/tool/bash"
)

var _ = Describe("Bash Tool", func() {
	var bashTool *bash.Tool

	BeforeEach(func() {
		bashTool = bash.New()
	})

	Describe("Name", func() {
		It("returns bash", func() {
			Expect(bashTool.Name()).To(Equal("bash"))
		})
	})

	Describe("Description", func() {
		It("returns a non-empty description", func() {
			Expect(bashTool.Description()).NotTo(BeEmpty())
		})
	})

	Describe("Schema", func() {
		It("has command in Required", func() {
			schema := bashTool.Schema()
			Expect(schema.Required).To(ContainElement("command"))
		})
	})

	Describe("Execute", func() {
		Context("with a valid command", func() {
			It("returns the command output", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": "echo hello"},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(Equal("hello"))
				Expect(result.Error).ToNot(HaveOccurred())
			})
		})

		Context("with an invalid command", func() {
			It("returns non-nil Error in result", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": "nonexistent_command_xyz_123"},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("with missing command argument", func() {
			It("returns a Go error", func() {
				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{},
				}
				_, err := bashTool.Execute(context.Background(), input)
				Expect(err).To(HaveOccurred())
			})
		})

		Context("with cancelled context", func() {
			It("respects context cancellation", func() {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()

				input := tool.Input{
					Name:      "bash",
					Arguments: map[string]interface{}{"command": "sleep 10"},
				}
				result, err := bashTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).To(HaveOccurred())
			})
		})

		Context("with output exceeding the truncation cap", func() {
			It("returns a truncated payload with the recovery hint embedded", func() {
				// 60KB of output via printf + repeat.
				input := tool.Input{
					Name: "bash",
					Arguments: map[string]interface{}{
						"command": `awk 'BEGIN{for(i=0;i<3500;i++) print "line-" i}'`,
					},
				}
				result, err := bashTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Error).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("truncated"))
				Expect(result.Output).To(ContainSubstring("grep"))
				Expect(result.Output).To(ContainSubstring("offset"))
			})
		})
	})
})
