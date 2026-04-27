package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("Auth Commands", func() {
	var (
		testApp    *app.App
		tmpDir     string
		originalDr string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-test-*")
		Expect(err).NotTo(HaveOccurred())

		originalDr = os.Getenv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tmpDir)).To(Succeed())

		// config.DefaultConfig pins Providers.Default to "anthropic"; the
		// Auth suite runs without any real anthropic registration so
		// app.New returns "provider anthropic not found" before the
		// test body ever executes. Switch to the openai provider with
		// a throwaway test key — mirroring the pattern already used by
		// internal/app/app_test.go — so app bootstrap succeeds and the
		// downstream credential-format assertions actually run.
		Expect(os.Setenv("OPENAI_API_KEY", "test-key-auth-suite")).To(Succeed())

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

	Describe("Anthropic credential validation", func() {
		It("accepts valid API key format sk-ant-api03-*", func() {
			key := "sk-ant-api03-test-key-valid-12345678901234567890"
			Expect(key).To(MatchRegexp(`^sk-ant-api03-.+`))
		})

		It("accepts valid OAuth token format sk-ant-oat01-*", func() {
			key := "sk-ant-oat01-test-token-valid-123456789012345"
			Expect(key).To(MatchRegexp(`^sk-ant-oat01-.+`))
		})

		It("rejects invalid format without correct prefix", func() {
			key := "invalid-key"
			Expect(key).NotTo(MatchRegexp(`^sk-ant-(api03|oat01)-.+`))
		})
	})

	Describe("Auth command group", func() {
		It("shows help for auth command", func() {
			cmd := cli.NewRootCmd(testApp)
			cmd.SetArgs([]string{"auth", "--help"})
			out := new(bytes.Buffer)
			cmd.SetOut(out)
			cmd.SetErr(out)
			err := cmd.Execute()
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("Authenticate with AI providers"))
		})

		It("lists github-copilot subcommand in help", func() {
			cmd := cli.NewRootCmd(testApp)
			cmd.SetArgs([]string{"auth", "--help"})
			out := new(bytes.Buffer)
			cmd.SetOut(out)
			cmd.SetErr(out)
			err := cmd.Execute()
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("github-copilot"))
		})

		It("lists anthropic subcommand in help", func() {
			cmd := cli.NewRootCmd(testApp)
			cmd.SetArgs([]string{"auth", "--help"})
			out := new(bytes.Buffer)
			cmd.SetOut(out)
			cmd.SetErr(out)
			err := cmd.Execute()
			Expect(err).NotTo(HaveOccurred())
			Expect(out.String()).To(ContainSubstring("anthropic"))
		})

		DescribeTable("lists every configured provider in help",
			func(name string) {
				cmd := cli.NewRootCmd(testApp)
				cmd.SetArgs([]string{"auth", "--help"})
				out := new(bytes.Buffer)
				cmd.SetOut(out)
				cmd.SetErr(out)
				Expect(cmd.Execute()).To(Succeed())
				Expect(out.String()).To(ContainSubstring(name))
			},
			Entry("openai", "openai"),
			Entry("openzen", "openzen"),
			Entry("zai", "zai"),
			Entry("ollama", "ollama"),
		)
	})

	Describe("GitHub Copilot OAuth", func() {
		BeforeEach(func() {
			if os.Getenv("FLOWSTATE_TEST_OAUTH_LIVE") == "" {
				Skip("live OAuth test skipped — opens a real browser and hits github.com/login/device. Set FLOWSTATE_TEST_OAUTH_LIVE=1 to run.")
			}
		})
		It("shows starting message for github-copilot command", func() {
			cmd := cli.NewRootCmd(testApp)
			cmd.SetArgs([]string{"auth", "github-copilot"})
			out := new(bytes.Buffer)
			cmd.SetOut(out)
			cmd.SetErr(out)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			go func() {
				cancel()
			}()

			_ = cmd.ExecuteContext(ctx)

			output := out.String()
			Expect(output).To(ContainSubstring("Starting GitHub OAuth authentication"))
		})
	})
})
