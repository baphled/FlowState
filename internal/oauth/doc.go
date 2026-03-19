// Package oauth provides OAuth 2.0 authentication implementations for FlowState providers.
package oauth

// This package implements the OAuth 2.0 Device Flow for GitHub and other OAuth providers.
// It includes:
//   - OAuth provider interfaces and implementations
//   - GitHub Device Flow implementation
//   - Encrypted token storage using age encryption
//
// Usage:
//
//	github := oauth.NewGitHub("your-client-id")
//	deviceResp, err := github.InitiateFlow(ctx)
//	// Display deviceResp.UserCode and deviceResp.VerificationURI to user
//	result, err := github.PollToken(ctx, deviceResp.DeviceCode, deviceResp.Interval)
//	if result.State == oauth.StateApproved {
//	    // Store the token securely
//	}
