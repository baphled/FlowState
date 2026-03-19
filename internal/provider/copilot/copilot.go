package copilot

import (
	"context"
	"fmt"

	"github.com/baphled/flowstate/internal/oauth"
	"github.com/baphled/flowstate/internal/provider"
)

type Provider struct {
	token        string
	oauthProvider *oauth.GitHubOAuthProvider
	tokenStorage oauth.TokenStorage
}

func NewWithAPIKey(token string) *Provider {
	return &Provider{
		token: token,
	}
}

func NewWithOAuth(clientID string, storage oauth.TokenStorage) (*Provider, error) {
	oauthProvider := oauth.NewGitHubOAuthProvider(clientID)
	
	token, err := storage.Load("github-copilot")
	if err != nil {
		return nil, fmt.Errorf("failed to load OAuth token: %w", err)
	}

	return &Provider{
		token:        token.AccessToken,
		oauthProvider: oauthProvider,
		tokenStorage: storage,
	}, nil
}

func (p *Provider) Chat(ctx context.Context, req *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{
		Content: "OAuth implementation pending",
	}, nil
}

func (p *Provider) Name() string {
	return "GitHub Copilot"
}

func (p *Provider) Models() []string {
	return []string{"gpt-4", "gpt-3.5-turbo"}
}
