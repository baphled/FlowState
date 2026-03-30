package cli

import (
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
