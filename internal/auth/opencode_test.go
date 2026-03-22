package auth_test

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/auth"
)

func TestOpenCodeAuth(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "OpenCode Auth Suite")
}

var _ = Describe("LoadOpenCodeAuth", func() {
	var (
		tmpDir string
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "opencode-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	Context("when auth.json does not exist", func() {
		It("returns ErrAuthFileNotFound", func() {
			nonExistentPath := filepath.Join(tmpDir, "nonexistent", "auth.json")
			authData, err := auth.LoadOpenCodeAuthFrom(nonExistentPath)
			Expect(err).To(MatchError(auth.ErrAuthFileNotFound))
			Expect(authData).To(BeNil())
		})
	})

	Context("when auth.json contains valid GitHub Copilot OAuth credentials", func() {
		It("loads GitHub Copilot credentials correctly", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			jsonContent := `{
  "github-copilot": {
    "type": "oauth",
    "refresh": "gho_refresh_token",
    "access": "gho_access_token",
    "expires": 0
  }
}`
			Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

			authData, err := auth.LoadOpenCodeAuthFrom(authPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(authData).NotTo(BeNil())
			Expect(authData.GitHubCopilot).NotTo(BeNil())
			Expect(authData.GitHubCopilot.Type).To(Equal("oauth"))
			Expect(authData.GitHubCopilot.Access).To(Equal("gho_access_token"))
			Expect(authData.GitHubCopilot.Refresh).To(Equal("gho_refresh_token"))
		})
	})

	Context("when auth.json contains valid Anthropic OAuth credentials", func() {
		It("loads Anthropic credentials correctly", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			jsonContent := `{
  "anthropic": {
    "type": "oauth",
    "access": "sk-ant-oat01-token123",
    "refresh": "sk-ant-ort01-refresh456",
    "expires": 1773994591282
  }
}`
			Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

			authData, err := auth.LoadOpenCodeAuthFrom(authPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(authData).NotTo(BeNil())
			Expect(authData.Anthropic).NotTo(BeNil())
			Expect(authData.Anthropic.Type).To(Equal("oauth"))
			Expect(authData.Anthropic.Access).To(Equal("sk-ant-oat01-token123"))
			Expect(authData.Anthropic.Refresh).To(Equal("sk-ant-ort01-refresh456"))
			Expect(authData.Anthropic.Expires).To(Equal(int64(1773994591282)))
		})
	})

	Context("when auth.json contains both GitHub Copilot and Anthropic credentials", func() {
		It("loads both provider credentials", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			jsonContent := `{
  "github-copilot": {
    "type": "oauth",
    "refresh": "gho_refresh",
    "access": "gho_access",
    "expires": 0
  },
  "anthropic": {
    "type": "oauth",
    "access": "sk-ant-oat01-xyz",
    "refresh": "sk-ant-ort01-abc",
    "expires": 1773994591282
  }
}`
			Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

			authData, err := auth.LoadOpenCodeAuthFrom(authPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(authData).NotTo(BeNil())
			Expect(authData.GitHubCopilot).NotTo(BeNil())
			Expect(authData.Anthropic).NotTo(BeNil())
		})
	})

	Context("when auth.json contains invalid JSON", func() {
		It("returns an error", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			jsonContent := `{ invalid json`
			Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

			authData, err := auth.LoadOpenCodeAuthFrom(authPath)
			Expect(err).To(HaveOccurred())
			Expect(authData).To(BeNil())
		})
	})

	Context("when using default OpenCode path", func() {
		It("attempts to load from ~/.local/share/opencode/auth.json", func() {
			authPath := filepath.Join(tmpDir, ".local", "share", "opencode", "auth.json")
			Expect(os.MkdirAll(filepath.Dir(authPath), 0o755)).To(Succeed())
			jsonContent := `{"anthropic": {"type": "oauth", "access": "token"}}`
			Expect(os.WriteFile(authPath, []byte(jsonContent), 0o600)).To(Succeed())

			authData, err := auth.LoadOpenCodeAuthFromHome(func(home string) string {
				return filepath.Join(tmpDir, ".local", "share", "opencode", "auth.json")
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(authData).NotTo(BeNil())
		})
	})
})
