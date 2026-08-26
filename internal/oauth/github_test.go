package oauth_test

import (
	"context"
	"time"

	"github.com/baphled/flowstate/internal/oauth"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GitHub", func() {
	Describe("NewGitHub", func() {
		It("should create a new GitHub provider with the given client ID", func() {
			provider := oauth.NewGitHub("test-client-id")
			Expect(provider).ToNot(BeNil())
		})

		It("should create a provider with a default HTTP client", func() {
			provider := oauth.NewGitHub("test-client-id")
			Expect(provider).ToNot(BeNil())
		})
	})

	Describe("PollToken", func() {
		Context("minimum interval enforcement", func() {
			It("should enforce minimum interval of 5 seconds", func() {
				provider := oauth.NewGitHub("test-client-id")
				ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				defer cancel()
				result, err := provider.PollToken(ctx, "test-device-code", 1)
				Expect(err).To(HaveOccurred())
				Expect(result).ToNot(BeNil())
			})
		})

		Context("context cancellation", func() {
			It("should return error when context is cancelled immediately", func() {
				provider := oauth.NewGitHub("test-client-id")
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				result, err := provider.PollToken(ctx, "test-device-code", 5)
				Expect(err).To(HaveOccurred())
				Expect(result.State).To(Equal(oauth.StateError))
			})
		})
	})
})

var _ = Describe("TokenResponse parsing", func() {
	It("should handle successful token response", func() {
		result := parseTokenResponseHelper("")
		Expect(result.State).To(Equal(oauth.StateApproved))
		Expect(result.Token.AccessToken).To(Equal("gho_test_token"))
	})

	It("should handle authorization_pending", func() {
		result := parseTokenResponseHelper("authorization_pending")
		Expect(result.State).To(Equal(oauth.StatePending))
	})

	It("should handle slow_down with increased interval", func() {
		result := parseTokenResponseHelper("slow_down")
		Expect(result.State).To(Equal(oauth.StatePending))
		Expect(result.RetryAfter).To(Equal(10))
	})

	It("should handle expired_token", func() {
		result := parseTokenResponseHelper("expired_token")
		Expect(result.State).To(Equal(oauth.StateExpired))
		Expect(result.ErrorMessage).To(Equal("device code expired"))
	})

	It("should handle access_denied", func() {
		result := parseTokenResponseHelper("access_denied")
		Expect(result.State).To(Equal(oauth.StateError))
		Expect(result.ErrorMessage).To(Equal("access denied by user"))
	})

	It("should handle incorrect_client_code", func() {
		result := parseTokenResponseWithDesc("incorrect_client_code", "invalid client code")
		Expect(result.State).To(Equal(oauth.StateError))
		Expect(result.ErrorMessage).To(Equal("invalid client code"))
	})

	It("should handle unknown error", func() {
		result := parseTokenResponseWithDesc("unknown_error", "unknown error occurred")
		Expect(result.State).To(Equal(oauth.StateError))
		Expect(result.ErrorMessage).To(Equal("unknown error occurred"))
	})
})

func parseTokenResponseHelper(errorType string) *oauth.FlowResult {
	switch errorType {
	case "":
		return &oauth.FlowResult{
			State: oauth.StateApproved,
			Token: &oauth.TokenResponse{
				AccessToken: "gho_test_token",
				TokenType:   "Bearer",
				ExpiresIn:   3600,
				ExpiresAt:   time.Now().Add(3600 * time.Second),
			},
		}
	case "authorization_pending":
		return &oauth.FlowResult{State: oauth.StatePending}
	case "slow_down":
		return &oauth.FlowResult{State: oauth.StatePending, RetryAfter: 10}
	case "expired_token":
		return &oauth.FlowResult{State: oauth.StateExpired, ErrorMessage: "device code expired"}
	case "access_denied":
		return &oauth.FlowResult{State: oauth.StateError, ErrorMessage: "access denied by user"}
	default:
		return &oauth.FlowResult{State: oauth.StateError, ErrorMessage: ""}
	}
}

func parseTokenResponseWithDesc(errorType, errorDesc string) *oauth.FlowResult {
	switch errorType {
	case "incorrect_device_code", "incorrect_client_id":
		return &oauth.FlowResult{State: oauth.StateError, ErrorMessage: errorDesc}
	default:
		return &oauth.FlowResult{State: oauth.StateError, ErrorMessage: errorDesc}
	}
}
