package cli_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("auth ollama subcommand", func() {
	var (
		testApp    *app.App
		tmpDir     string
		originalDr string
		restore    func()
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-ollama-auth-*")
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
		if restore != nil {
			restore()
			restore = nil
		}
		Expect(os.Unsetenv("OPENAI_API_KEY")).To(Succeed())
		if originalDr != "" {
			Expect(os.Setenv("XDG_CONFIG_HOME", originalDr)).To(Succeed())
		} else {
			Expect(os.Unsetenv("XDG_CONFIG_HOME")).To(Succeed())
		}
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	It("reports success when the host is reachable", func() {
		seen := ""
		restore = cli.SetOllamaProbeForTest(func(host string) error {
			seen = host
			return nil
		})

		cmd := cli.NewRootCmd(testApp)
		cmd.SetArgs([]string{"auth", "ollama"})
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		Expect(cmd.Execute()).To(Succeed())
		Expect(out.String()).To(ContainSubstring("Ollama doesn't require authentication"))
		Expect(out.String()).To(ContainSubstring("Confirmed reachable at"))
		Expect(seen).To(Equal(testApp.Config.Providers.Ollama.Host))
	})

	It("returns a non-zero error when the host is unreachable", func() {
		restore = cli.SetOllamaProbeForTest(func(string) error {
			return errors.New("connection refused")
		})

		cmd := cli.NewRootCmd(testApp)
		cmd.SetArgs([]string{"auth", "ollama"})
		out := new(bytes.Buffer)
		cmd.SetOut(out)
		cmd.SetErr(out)

		err := cmd.Execute()
		Expect(err).To(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("not reachable"))
	})
})
