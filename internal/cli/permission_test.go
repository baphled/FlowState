package cli_test

import (
	"bytes"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("NewCLIPermissionHandler", func() {
	var (
		output  *bytes.Buffer
		handler tool.PermissionHandler
	)

	Context("when user types y", func() {
		BeforeEach(func() {
			output = &bytes.Buffer{}
			input := strings.NewReader("y\n")
			handler = cli.NewCLIPermissionHandler(input, output)
		})

		It("returns true", func() {
			approved, err := handler(tool.PermissionRequest{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(approved).To(BeTrue())
		})

		It("prints tool details to output", func() {
			_, _ = handler(tool.PermissionRequest{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "ls"},
			})
			Expect(output.String()).To(ContainSubstring("bash"))
			Expect(output.String()).To(ContainSubstring("y/n"))
		})
	})

	Context("when user types n", func() {
		BeforeEach(func() {
			output = &bytes.Buffer{}
			input := strings.NewReader("n\n")
			handler = cli.NewCLIPermissionHandler(input, output)
		})

		It("returns false", func() {
			approved, err := handler(tool.PermissionRequest{
				ToolName:  "bash",
				Arguments: map[string]interface{}{"command": "rm -rf /"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(approved).To(BeFalse())
		})
	})

	Context("when input is empty (EOF)", func() {
		BeforeEach(func() {
			output = &bytes.Buffer{}
			input := strings.NewReader("")
			handler = cli.NewCLIPermissionHandler(input, output)
		})

		It("returns false", func() {
			approved, err := handler(tool.PermissionRequest{
				ToolName:  "bash",
				Arguments: map[string]interface{}{},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(approved).To(BeFalse())
		})
	})
})
