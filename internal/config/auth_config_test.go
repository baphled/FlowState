package config_test

// PR5 C10 — AuthConfig config-layer lift spec.
//
// Pins:
//   - DefaultAuthConfig returns Enabled=true (the PR5 flag-flip),
//     Mode="per-deployment-login" (plan §OD-E v1 deployable mode),
//     SecureCookies=true (production HTTPS default).
//   - YAML round-trip: marshal then unmarshal preserves every field
//     bytewise, including the slice AllowedOrigins (gopkg.in/yaml.v3
//     handles []string natively).
//   - LoadConfigFromPath with an `auth:` block populates the AuthConfig
//     struct fields. Omitting the block yields the DefaultConfig
//     value (i.e. the flag-on default propagates).
//
// Seam-level Ginkgo at the config package boundary per
// feedback_ginkgo_not_godog.

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/config"
)

var _ = Describe("PR5 C10 — AuthConfig", func() {
	Describe("DefaultAuthConfig", func() {
		It("ships Enabled=true (the PR5 flag-flip)", func() {
			cfg := config.DefaultAuthConfig()
			Expect(cfg.Enabled).To(BeTrue(),
				"PR5/C10 flips features.auth_v1 default-on; new deployments "+
					"come up authenticated unless `auth.enabled: false` is set")
		})

		It("defaults Mode to per-deployment-login", func() {
			cfg := config.DefaultAuthConfig()
			Expect(cfg.Mode).To(Equal("per-deployment-login"))
		})

		It("defaults SecureCookies to true", func() {
			cfg := config.DefaultAuthConfig()
			Expect(cfg.SecureCookies).To(BeTrue())
		})

		It("leaves credentials and CSRF key unset (operator provisions)", func() {
			cfg := config.DefaultAuthConfig()
			Expect(cfg.Secret).To(BeEmpty())
			Expect(cfg.CSRFKey).To(BeEmpty())
			Expect(cfg.PrincipalID).To(BeEmpty())
		})
	})

	Describe("DefaultConfig.Auth wiring", func() {
		It("propagates DefaultAuthConfig into the top-level AppConfig", func() {
			cfg := config.DefaultConfig()
			Expect(cfg.Auth).To(Equal(config.DefaultAuthConfig()))
		})
	})

	Describe("YAML round-trip", func() {
		It("preserves every field through marshal+unmarshal", func() {
			original := config.AuthConfig{
				Enabled:        true,
				Mode:           "multi-user",
				Secret:         "test-secret-do-not-ship",
				PrincipalID:    "operator-001",
				DisplayName:    "Operator One",
				AllowedOrigins: []string{"https://flowstate.example.com", "https://staging.flowstate.example.com"},
				SecureCookies:  true,
				CSRFKey:        "test-csrf-key-32-bytes-of-padding!",
			}

			data, err := yaml.Marshal(&original)
			Expect(err).ToNot(HaveOccurred())

			var roundtrip config.AuthConfig
			Expect(yaml.Unmarshal(data, &roundtrip)).To(Succeed())
			Expect(roundtrip).To(Equal(original))
		})

		It("emits omitempty fields only when populated", func() {
			minimal := config.AuthConfig{
				Enabled:       true,
				Mode:          "shared-secret",
				SecureCookies: true,
			}

			data, err := yaml.Marshal(&minimal)
			Expect(err).ToNot(HaveOccurred())

			// omitempty fields (Secret, PrincipalID, DisplayName,
			// AllowedOrigins, CSRFKey) must NOT appear when zero-valued —
			// they leak nothing into the emitted YAML.
			yamlStr := string(data)
			Expect(yamlStr).NotTo(ContainSubstring("secret:"))
			Expect(yamlStr).NotTo(ContainSubstring("principal_id:"))
			Expect(yamlStr).NotTo(ContainSubstring("display_name:"))
			Expect(yamlStr).NotTo(ContainSubstring("allowed_origins:"))
			Expect(yamlStr).NotTo(ContainSubstring("csrf_key:"))

			// Non-omitempty fields must always appear.
			Expect(yamlStr).To(ContainSubstring("enabled: true"))
			Expect(yamlStr).To(ContainSubstring("mode: shared-secret"))
			Expect(yamlStr).To(ContainSubstring("secure_cookies: true"))
		})
	})

	Describe("LoadConfigFromPath with auth: block", func() {
		It("populates the AuthConfig struct from YAML", func() {
			tempDir, err := os.MkdirTemp("", "auth-config-test")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			yamlContent := `
log_level: info
auth:
  enabled: true
  mode: shared-secret
  secret: my-deployment-secret
  allowed_origins:
    - https://flowstate.example.com
  secure_cookies: true
  csrf_key: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
`
			configPath := filepath.Join(tempDir, "config.yaml")
			Expect(os.WriteFile(configPath, []byte(yamlContent), 0644)).To(Succeed())

			cfg, err := config.LoadConfigFromPath(configPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(cfg.Auth.Enabled).To(BeTrue())
			Expect(cfg.Auth.Mode).To(Equal("shared-secret"))
			Expect(cfg.Auth.Secret).To(Equal("my-deployment-secret"))
			Expect(cfg.Auth.SecureCookies).To(BeTrue())
			Expect(cfg.Auth.CSRFKey).To(Equal("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
			Expect(cfg.Auth.AllowedOrigins).To(Equal([]string{"https://flowstate.example.com"}))
		})

		It("uses the DefaultAuthConfig when auth: block is absent", func() {
			tempDir, err := os.MkdirTemp("", "auth-config-test")
			Expect(err).ToNot(HaveOccurred())
			DeferCleanup(func() { os.RemoveAll(tempDir) })

			// No auth: block — the merge-with-defaults path should
			// surface DefaultAuthConfig values.
			yamlContent := `log_level: info`
			configPath := filepath.Join(tempDir, "config.yaml")
			Expect(os.WriteFile(configPath, []byte(yamlContent), 0644)).To(Succeed())

			cfg, err := config.LoadConfigFromPath(configPath)
			Expect(err).ToNot(HaveOccurred())

			// PR5/C10 flag-flip: absent auth: block defaults Enabled=true.
			// This is the load-bearing behaviour change — operators who
			// don't configure auth get authentication anyway.
			Expect(cfg.Auth.Enabled).To(BeTrue(),
				"missing auth: block must default Enabled=true per PR5/C10 flip")
			Expect(cfg.Auth.Mode).To(Equal("per-deployment-login"))
		})
	})
})
