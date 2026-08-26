package cli_test

import (
	"bytes"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/cli"
)

var _ = Describe("config command", func() {
	var out *bytes.Buffer

	BeforeEach(func() {
		out = new(bytes.Buffer)
	})

	Describe("config harness", func() {
		It("prints YAML containing enabled: true", func() {
			testApp := createTestApp("", "")
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs([]string{"config", "harness"})

			err := root.Execute()

			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("enabled: true"))
		})

		It("prints YAML containing critic_enabled: false", func() {
			testApp := createTestApp("", "")
			root := cli.NewRootCmd(testApp)
			root.SetOut(out)
			root.SetErr(out)
			root.SetArgs([]string{"config", "harness"})

			err := root.Execute()

			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("critic_enabled: false"))
		})
	})
})
