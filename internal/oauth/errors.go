package oauth

import "errors"

// OAuth error definitions.
var (
	ErrTokenExpired     = errors.New("OAuth token has expired")
	ErrTokenRevoked     = errors.New("OAuth token has been revoked")
	ErrFlowPending      = errors.New("OAuth flow is still pending user approval")
	ErrFlowTimeout      = errors.New("OAuth flow timed out waiting for approval")
	ErrFlowDenied       = errors.New("OAuth authorization was denied")
	ErrRateLimited      = errors.New("OAuth rate limit exceeded")
	ErrInvalidClientID  = errors.New("invalid OAuth client ID")
	ErrNetworkError     = errors.New("network error during OAuth flow")
	ErrEncryptionFailed = errors.New("failed to encrypt token")
)
