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

var _ = Describe("auth openai subcommand", func() {
	var (
		testApp    *app.App
		tmpDir     string
		originalDr string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-openai-auth-*")
		Expect(err).NotTo(HaveOccurred())

		originalDr = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())
		// app.New() bootstraps the default provider; pin to openai with a
		// dummy key so the suite does not require live anthropic creds.
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
		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if originalDr != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalDr)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("saves an env-supplied API key to the config", func() {
		Expect(os.Setenv("OPENAI_API_KEY", "sk-test-suite-openai-1234567890")).To(Succeed())

		cmd := cli.NewRootCmd(testApp)
		cmd.SetArgs([]string{"auth", "openai"})
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		Expect(cmd.Execute()).To(Succeed())
		Expect(out.String()).To(ContainSubstring("OpenAI API key saved"))
		Expect(testApp.Config.Providers.OpenAI.APIKey).To(Equal("sk-test-suite-openai-1234567890"))

		data, err := os.ReadFile(filepath.Join(tmpDir, "flowstate", "config.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("sk-test-suite-openai-1234567890"))
	})

	It("rejects a malformed API key", func() {
		Expect(os.Setenv("OPENAI_API_KEY", "not-an-openai-key")).To(Succeed())

		cmd := cli.NewRootCmd(testApp)
		cmd.SetArgs([]string{"auth", "openai"})
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("Invalid API key format"))
	})
})
