package oauth_test

import (
	"time"

	"github.com/baphled/flowstate/internal/oauth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OAuth", func() {
	Describe("DeviceCodeResponse", func() {
		It("should store device code response fields", func() {
			resp := &oauth.DeviceCodeResponse{
				DeviceCode:      "test-device-code",
				UserCode:        "USER-1234",
				VerificationURI: "https://github.com/login/device",
				ExpiresIn:       900,
				Interval:        5,
			}
			Expect(resp.DeviceCode).To(Equal("test-device-code"))
			Expect(resp.UserCode).To(Equal("USER-1234"))
			Expect(resp.VerificationURI).To(Equal("https://github.com/login/device"))
			Expect(resp.ExpiresIn).To(Equal(900))
			Expect(resp.Interval).To(Equal(5))
		})
	})

	Describe("TokenResponse", func() {
		It("should store token response fields", func() {
			expiresAt := time.Now().Add(1 * time.Hour)
			resp := &oauth.TokenResponse{
				AccessToken: "gho_test_token_12345",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
				ExpiresAt:   expiresAt,
			}
			Expect(resp.AccessToken).To(Equal("gho_test_token_12345"))
			Expect(resp.TokenType).To(Equal("Bearer"))
			Expect(resp.ExpiresIn).To(Equal(3600))
			Expect(resp.ExpiresAt).To(Equal(expiresAt))
		})
	})

	Describe("FlowState", func() {
		It("should have correct state values", func() {
			Expect(oauth.StatePending).To(BeEquivalentTo(0))
			Expect(oauth.StateApproved).To(BeEquivalentTo(1))
			Expect(oauth.StateExpired).To(BeEquivalentTo(2))
			Expect(oauth.StateRateLimited).To(BeEquivalentTo(3))
			Expect(oauth.StateError).To(BeEquivalentTo(4))
		})
	})

	Describe("FlowResult", func() {
		It("should store pending state result", func() {
			result := &oauth.FlowResult{
				State:      oauth.StatePending,
				RetryAfter: 5,
			}
			Expect(result.State).To(Equal(oauth.StatePending))
			Expect(result.RetryAfter).To(Equal(5))
			Expect(result.Token).To(BeNil())
		})

		It("should store approved state result with token", func() {
			token := &oauth.TokenResponse{
				AccessToken: "test-token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
			}
			result := &oauth.FlowResult{
				State: oauth.StateApproved,
				Token: token,
			}
			Expect(result.State).To(Equal(oauth.StateApproved))
			Expect(result.Token).To(Equal(token))
			Expect(result.ErrorMessage).To(BeEmpty())
		})

		It("should store expired state result", func() {
			result := &oauth.FlowResult{
				State:        oauth.StateExpired,
				ErrorMessage: "authorization request expired",
			}
			Expect(result.State).To(Equal(oauth.StateExpired))
			Expect(result.ErrorMessage).To(Equal("authorization request expired"))
		})

		It("should store error state result", func() {
			result := &oauth.FlowResult{
				State:        oauth.StateError,
				ErrorMessage: "access denied by user",
			}
			Expect(result.State).To(Equal(oauth.StateError))
			Expect(result.ErrorMessage).To(Equal("access denied by user"))
		})
	})

	Describe("CopilotScopes", func() {
		It("should return copilot scope", func() {
			scopes := oauth.CopilotScopes()
			Expect(scopes).To(HaveLen(1))
			Expect(scopes).To(ContainElement("copilot"))
		})
	})

	Describe("OAuth errors", func() {
		It("should have correct error messages", func() {
			Expect(oauth.ErrTokenExpired.Error()).To(Equal("OAuth token has expired"))
			Expect(oauth.ErrTokenRevoked.Error()).To(Equal("OAuth token has been revoked"))
			Expect(oauth.ErrFlowPending.Error()).To(Equal("OAuth flow is still pending user approval"))
			Expect(oauth.ErrFlowTimeout.Error()).To(Equal("OAuth flow timed out waiting for approval"))
			Expect(oauth.ErrFlowDenied.Error()).To(Equal("OAuth authorization was denied"))
			Expect(oauth.ErrRateLimited.Error()).To(Equal("OAuth rate limit exceeded"))
			Expect(oauth.ErrInvalidClientID.Error()).To(Equal("invalid OAuth client ID"))
			Expect(oauth.ErrNetworkError.Error()).To(Equal("network error during OAuth flow"))
			Expect(oauth.ErrEncryptionFailed.Error()).To(Equal("failed to encrypt token"))
		})
	})
})
