package anthropic

import (
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("IsOAuthToken", func() {
	It("returns true for sk-ant-oat01- prefixed tokens", func() {
		Expect(IsOAuthToken("sk-ant-oat01-abc123")).To(BeTrue())
	})

	It("returns true for a full-length OAuth token", func() {
		Expect(IsOAuthToken("sk-ant-oat01-abcdef1234567890abcdef1234567890")).To(BeTrue())
	})

	It("returns false for standard API keys", func() {
		Expect(IsOAuthToken("sk-ant-api03-abc123")).To(BeFalse())
	})

	It("returns false for empty string", func() {
		Expect(IsOAuthToken("")).To(BeFalse())
	})

	It("returns false for arbitrary strings", func() {
		Expect(IsOAuthToken("not-a-token")).To(BeFalse())
	})

	It("returns false for partial prefix match", func() {
		Expect(IsOAuthToken("sk-ant-oat01")).To(BeFalse())
	})

	It("returns false for different oat version prefix", func() {
		Expect(IsOAuthToken("sk-ant-oat02-abc123")).To(BeFalse())
	})
})

var _ = Describe("NewOAuth", func() {
	It("returns a provider for a valid OAuth token", func() {
		p, err := NewOAuth("sk-ant-oat01-valid-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())
	})

	It("returns errOAuthTokenRequired for empty token", func() {
		p, err := NewOAuth("")
		Expect(p).To(BeNil())
		Expect(err).To(MatchError(errOAuthTokenRequired))
	})

	It("returns a provider even for non-OAuth formatted tokens", func() {
		p, err := NewOAuth("any-non-empty-string")
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())
	})

	It("sets isOAuth to true on the returned provider", func() {
		p, err := NewOAuth("sk-ant-oat01-valid-token")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.isOAuth).To(BeTrue())
	})
})

var _ = Describe("New", func() {
	It("sets isOAuth to false on the returned provider", func() {
		p, err := New("sk-ant-api03-test-key")
		Expect(err).NotTo(HaveOccurred())
		Expect(p.isOAuth).To(BeFalse())
	})

	It("returns errAPIKeyRequired for empty key", func() {
		p, err := New("")
		Expect(p).To(BeNil())
		Expect(err).To(MatchError(errAPIKeyRequired))
	})
})

var _ = Describe("extractSystemPrompt", func() {
	var msgs []provider.Message

	BeforeEach(func() {
		msgs = []provider.Message{
			{Role: "system", Content: "You are a helpful assistant."},
			{Role: "user", Content: "Hello"},
		}
	})

	Context("when provider uses API key authentication", func() {
		It("includes CacheControl on system blocks", func() {
			p := &Provider{isOAuth: false}
			blocks := p.extractSystemPrompt(msgs)
			Expect(blocks).To(HaveLen(1))
			Expect(blocks[0].Text).To(Equal("You are a helpful assistant."))
			Expect(blocks[0].CacheControl).NotTo(BeZero())
		})
	})

	Context("when provider uses OAuth authentication", func() {
		It("omits CacheControl on system blocks", func() {
			p := &Provider{isOAuth: true}
			blocks := p.extractSystemPrompt(msgs)
			Expect(blocks).To(HaveLen(1))
			Expect(blocks[0].Text).To(Equal("You are a helpful assistant."))
			Expect(blocks[0].CacheControl).To(BeZero())
		})
	})

	Context("when there are no system messages", func() {
		It("returns an empty slice", func() {
			p := &Provider{isOAuth: false}
			userOnly := []provider.Message{{Role: "user", Content: "Hello"}}
			blocks := p.extractSystemPrompt(userOnly)
			Expect(blocks).To(BeEmpty())
		})
	})

	Context("when system message has empty content", func() {
		It("skips the empty system message", func() {
			p := &Provider{isOAuth: false}
			withEmpty := []provider.Message{
				{Role: "system", Content: ""},
				{Role: "system", Content: "Valid prompt"},
			}
			blocks := p.extractSystemPrompt(withEmpty)
			Expect(blocks).To(HaveLen(1))
			Expect(blocks[0].Text).To(Equal("Valid prompt"))
		})
	})
})

var _ = Describe("tryOpenCodeAuth (direct)", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "anthropic-tryauth-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("returns errNoOpenCodeCredentials if no anthropic section present", func() {
		authJSON := `{"github-copilot": {"type": "oauth", "access": "ghu_test"}}`
		authPath := filepath.Join(tmpDir, "auth.json")
		err := os.WriteFile(authPath, []byte(authJSON), 0o600)
		Expect(err).NotTo(HaveOccurred())

		p, err := tryOpenCodeAuth(authPath)
		Expect(p).To(BeNil())
		Expect(err).To(MatchError(errNoOpenCodeCredentials))
	})

	It("returns errNoOpenCodeCredentials if anthropic section has empty access", func() {
		authJSON := `{"anthropic": {"type": "api_key", "access": ""}}`
		authPath := filepath.Join(tmpDir, "auth.json")
		err := os.WriteFile(authPath, []byte(authJSON), 0o600)
		Expect(err).NotTo(HaveOccurred())

		p, err := tryOpenCodeAuth(authPath)
		Expect(p).To(BeNil())
		Expect(err).To(MatchError(errNoOpenCodeCredentials))
	})

	It("returns provider if anthropic section has valid access", func() {
		authJSON := `{"anthropic": {"type": "api_key", "access": "sk-ant-test"}}`
		authPath := filepath.Join(tmpDir, "auth.json")
		err := os.WriteFile(authPath, []byte(authJSON), 0o600)
		Expect(err).NotTo(HaveOccurred())

		p, err := tryOpenCodeAuth(authPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())
	})

	It("returns provider via NewOAuth when token has OAuth prefix", func() {
		authJSON := `{"anthropic": {"type": "oauth", "access": "sk-ant-oat01-abc"}}`
		authPath := filepath.Join(tmpDir, "auth.json")
		err := os.WriteFile(authPath, []byte(authJSON), 0o600)
		Expect(err).NotTo(HaveOccurred())

		p, err := tryOpenCodeAuth(authPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())
	})

	It("returns provider via New when token has API key prefix", func() {
		authJSON := `{"anthropic": {"type": "api_key", "access": "sk-ant-api03-xyz"}}`
		authPath := filepath.Join(tmpDir, "auth.json")
		err := os.WriteFile(authPath, []byte(authJSON), 0o600)
		Expect(err).NotTo(HaveOccurred())

		p, err := tryOpenCodeAuth(authPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(p).NotTo(BeNil())
	})
})

var _ = Describe("NewFromOpenCodeOrConfig", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "anthropic-config-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	Context("when opencode auth.json does not exist", func() {
		It("falls through to fallback API key", func() {
			nonExistent := filepath.Join(tmpDir, "missing", "auth.json")
			p, err := NewFromOpenCodeOrConfig(nonExistent, "sk-ant-fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode auth.json has no anthropic credentials", func() {
		It("falls through to fallback API key", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"github-copilot": {"type": "oauth", "access": "gho_test"}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "sk-ant-fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode auth.json has valid anthropic credentials", func() {
		It("returns a provider using the opencode token", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"anthropic": {"type": "oauth", "access": "sk-ant-oat01-valid"}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode has OAuth token and fallback key exists", func() {
		It("prefers the OAuth token from opencode", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"anthropic": {"type": "oauth", "access": "sk-ant-oat01-pref"}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "sk-ant-api03-fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode has API key token", func() {
		It("returns a provider using the API key constructor", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"anthropic": {"type": "api_key", "access": "sk-ant-api03-xyz"}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode auth.json contains invalid JSON", func() {
		It("returns an error", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			Expect(os.WriteFile(authPath, []byte("{broken"), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "sk-ant-fallback")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})
	})

	Context("when opencodePath is empty", func() {
		It("uses the fallback API key", func() {
			p, err := NewFromOpenCodeOrConfig("", "sk-ant-key123")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when no credential source provides a key", func() {
		It("returns errAPIKeyRequired", func() {
			nonExistent := filepath.Join(tmpDir, "missing", "auth.json")
			p, err := NewFromOpenCodeOrConfig(nonExistent, "")
			Expect(err).To(MatchError(errAPIKeyRequired))
			Expect(p).To(BeNil())
		})
	})

	Context("when opencode auth.json has empty access token", func() {
		It("falls through to fallback API key", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"anthropic": {"type": "oauth", "access": ""}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "sk-ant-fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode has ErrNoCredentials (empty auth.json)", func() {
		It("falls through to fallback API key", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			Expect(os.WriteFile(authPath, []byte(`{}`), 0o600)).To(Succeed())
			p, err := NewFromOpenCodeOrConfig(authPath, "sk-ant-fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})
})
