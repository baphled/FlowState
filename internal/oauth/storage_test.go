package oauth_test

import (
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/oauth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("EncryptedStore", func() {
	var (
		store   *oauth.EncryptedStore
		tempDir string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "oauth-test-*")
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		if tempDir != "" {
			os.RemoveAll(tempDir)
		}
	})

	Describe("NewEncryptedStore", func() {
		It("should create a new encrypted store", func() {
			store, err := oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(store).ToNot(BeNil())
		})

		It("should create tokens directory", func() {
			_, err := oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
			tokensDir := filepath.Join(tempDir, "tokens")
			info, err := os.Stat(tokensDir)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})

		It("should create key file", func() {
			_, err := oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
			keyPath := filepath.Join(tempDir, "tokens", ".oauth_key")
			info, err := os.Stat(keyPath)
			Expect(err).ToNot(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
		})

		It("should reuse existing key file", func() {
			store1, err := oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
			token1 := &oauth.TokenResponse{
				AccessToken: "test-token-1",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err = store1.Store("provider1", token1)
			Expect(err).ToNot(HaveOccurred())

			store2, err := oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
			retrieved, err := store2.Retrieve("provider1")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.AccessToken).To(Equal("test-token-1"))
		})
	})

	Describe("Store", func() {
		BeforeEach(func() {
			var err error
			store, err = oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should store a token", func() {
			token := &oauth.TokenResponse{
				AccessToken: "gho_test_token_12345",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("github", token)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error for empty provider", func() {
			token := &oauth.TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("", token)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("provider cannot be empty"))
		})

		It("should return error for nil token", func() {
			err := store.Store("github", nil)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid token"))
		})

		It("should return error for token with empty access token", func() {
			token := &oauth.TokenResponse{
				AccessToken: "",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("github", token)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("invalid token"))
		})
	})

	Describe("Retrieve", func() {
		BeforeEach(func() {
			var err error
			store, err = oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should retrieve a stored token", func() {
			token := &oauth.TokenResponse{
				AccessToken: "gho_test_token_12345",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("github", token)
			Expect(err).ToNot(HaveOccurred())

			retrieved, err := store.Retrieve("github")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved.AccessToken).To(Equal("gho_test_token_12345"))
			Expect(retrieved.TokenType).To(Equal("Bearer"))
			Expect(retrieved.ExpiresIn).To(Equal(3600))
		})

		It("should return error for non-existent provider", func() {
			_, err := store.Retrieve("nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("should retrieve different tokens for different providers", func() {
			token1 := &oauth.TokenResponse{
				AccessToken: "token-provider-1",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			token2 := &oauth.TokenResponse{
				AccessToken: "token-provider-2",
				TokenType:   "Bearer",
				ExpiresIn:   7200,
			}

			err := store.Store("provider1", token1)
			Expect(err).ToNot(HaveOccurred())
			err = store.Store("provider2", token2)
			Expect(err).ToNot(HaveOccurred())

			retrieved1, err := store.Retrieve("provider1")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved1.AccessToken).To(Equal("token-provider-1"))

			retrieved2, err := store.Retrieve("provider2")
			Expect(err).ToNot(HaveOccurred())
			Expect(retrieved2.AccessToken).To(Equal("token-provider-2"))
		})
	})

	Describe("Delete", func() {
		BeforeEach(func() {
			var err error
			store, err = oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should delete a stored token", func() {
			token := &oauth.TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("github", token)
			Expect(err).ToNot(HaveOccurred())

			err = store.Delete("github")
			Expect(err).ToNot(HaveOccurred())

			_, err = store.Retrieve("github")
			Expect(err).To(HaveOccurred())
		})

		It("should not return error for deleting non-existent token", func() {
			err := store.Delete("nonexistent")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Describe("HasToken", func() {
		BeforeEach(func() {
			var err error
			store, err = oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return true when token exists", func() {
			token := &oauth.TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("github", token)
			Expect(err).ToNot(HaveOccurred())

			hasToken := store.HasToken("github")
			Expect(hasToken).To(BeTrue())
		})

		It("should return false when token does not exist", func() {
			hasToken := store.HasToken("nonexistent")
			Expect(hasToken).To(BeFalse())
		})

		It("should return false after token is deleted", func() {
			token := &oauth.TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			err := store.Store("github", token)
			Expect(err).ToNot(HaveOccurred())

			err = store.Delete("github")
			Expect(err).ToNot(HaveOccurred())

			hasToken := store.HasToken("github")
			Expect(hasToken).To(BeFalse())
		})
	})

	Describe("concurrent access", func() {
		It("should handle concurrent store and retrieve operations", func() {
			store, err := oauth.NewEncryptedStore(tempDir)
			Expect(err).ToNot(HaveOccurred())

			token := &oauth.TokenResponse{
				AccessToken: "concurrent-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}

			done := make(chan bool)
			for range 10 {
				go func() {
					defer func() { done <- true }()
					_ = store.Store("github", token)
				}()
				go func() {
					defer func() { done <- true }()
					_ = store.HasToken("github")
				}()
			}

			for range 20 {
				<-done
			}
		})
	})
})
