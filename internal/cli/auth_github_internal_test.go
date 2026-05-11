package cli

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("resolveGitHubClientID", func() {
	It("returns config client ID when set", func() {
		cfg := config.DefaultConfig()
		cfg.Providers.GitHub.OAuth.ClientID = "custom-client-id"

		result := resolveGitHubClientID(cfg)

		Expect(result).To(Equal("custom-client-id"))
	})

	It("returns default client ID when config is empty", func() {
		cfg := config.DefaultConfig()

		result := resolveGitHubClientID(cfg)

		Expect(result).To(Equal(defaultGitHubClientID))
	})

	It("returns default client ID when config is nil", func() {
		result := resolveGitHubClientID(nil)

		Expect(result).To(Equal(defaultGitHubClientID))
	})
})

var _ = Describe("writeConfig (atomic persistence)", func() {
	var (
		tempDir       string
		savedXDG      string
		savedXDGFound bool
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "writeconfig-test-*")
		Expect(err).ToNot(HaveOccurred())
		// config.Dir() reads XDG_CONFIG_HOME and appends /flowstate.
		flowstateDir := filepath.Join(tempDir, "flowstate")
		Expect(os.MkdirAll(flowstateDir, 0o755)).To(Succeed())
		savedXDG, savedXDGFound = os.LookupEnv("XDG_CONFIG_HOME")
		Expect(os.Setenv("XDG_CONFIG_HOME", tempDir)).To(Succeed())
	})

	AfterEach(func() {
		if savedXDGFound {
			_ = os.Setenv("XDG_CONFIG_HOME", savedXDG)
		} else {
			_ = os.Unsetenv("XDG_CONFIG_HOME")
		}
		_ = os.RemoveAll(tempDir)
	})

	It("never leaves a zero-byte config.yaml when overwriting", func() {
		// Regression for non-atomic os.WriteFile: a crash between truncate
		// and write would expose an empty config and brick the CLI.
		cfgPath := filepath.Join(tempDir, "flowstate", "config.yaml")
		Expect(os.WriteFile(cfgPath, []byte("providers: {}\n"), 0o600)).To(Succeed())

		cfg := config.DefaultConfig()
		cfg.Providers.GitHub.OAuth.ClientID = "regression-id"

		Expect(writeConfig(cfg)).To(Succeed())

		info, err := os.Stat(cfgPath)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Size()).To(BeNumerically(">", int64(0)))
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("leaves no atomicwrite temp file behind", func() {
		cfg := config.DefaultConfig()
		Expect(writeConfig(cfg)).To(Succeed())

		entries, err := os.ReadDir(filepath.Join(tempDir, "flowstate"))
		Expect(err).ToNot(HaveOccurred())
		for _, entry := range entries {
			Expect(entry.Name()).ToNot(ContainSubstring(".atomicwrite-"))
		}
	})
})
