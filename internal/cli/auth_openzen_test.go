package cli_test

import (
	"bytes"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("auth openzen subcommand", func() {
	var (
		testApp    *app.App
		tmpDir     string
		originalDr string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-openzen-auth-*")
		Expect(err).NotTo(HaveOccurred())

		originalDr = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())
		Expect(os.Setenv("OPENAI_API_KEY", "sk-test-bootstrap-key-1234567890")).To(Succeed())
		Expect(os.MkdirAll(filepath.Join(tmpDir, "flowstate"), 0o700)).To(Succeed())

		cfg := config.DefaultConfig()
		cfg.Providers.Default = "openai"
		cfg.DataDir = filepath.Join(tmpDir, "data")
		Expect(os.MkdirAll(cfg.DataDir, 0o700)).To(Succeed())

		testApp, err = app.New(cfg)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.Unsetenv("OPENZEN_API_KEY")).To(Succeed())
		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if originalDr != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalDr)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("saves an env-supplied API key to the config", func() {
		Expect(os.Setenv("OPENZEN_API_KEY", "openzen-test-suite-key-1234567890")).To(Succeed())

		cmd := cli.NewRootCmd(testApp)
		cmd.SetArgs([]string{"auth", "openzen"})
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		Expect(cmd.Execute()).To(Succeed())
		Expect(out.String()).To(ContainSubstring("OpenZen API key saved"))
		Expect(testApp.Config.Providers.OpenZen.APIKey).To(Equal("openzen-test-suite-key-1234567890"))
	})

	It("rejects a too-short API key", func() {
		Expect(os.Setenv("OPENZEN_API_KEY", "short")).To(Succeed())

		cmd := cli.NewRootCmd(testApp)
		cmd.SetArgs([]string{"auth", "openzen"})
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("Invalid API key format"))
	})
})
