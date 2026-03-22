// Package copilot provides a GitHub Copilot provider implementation.
package copilot

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/baphled/flowstate/internal/auth"
	"github.com/baphled/flowstate/internal/oauth"
	"github.com/baphled/flowstate/internal/provider"
)

var errTokenRequired = errors.New("GitHub token is required")

// ErrEmbedNotSupported is returned when embedding is not supported by Copilot.
var ErrEmbedNotSupported = errors.New("embedding is not supported by GitHub Copilot")

const (
	providerName         = "github-copilot"
	defaultBaseURL       = "https://api.githubcopilot.com"
	tokenExchangeBaseURL = "https://api.github.com"
	headerAccept         = "application/json"
	headerContentType    = "application/json"
	defaultContextLength = 128000
	copilotUserAgent     = "GitHubCopilotChat/0.35.0"
	copilotEditorVersion = "vscode/1.107.0"
	copilotPluginVersion = "copilot-chat/0.35.0"
	copilotIntegrationID = "vscode-chat"
)

// Provider implements the GitHub Copilot API provider.
type Provider struct {
	baseURL      string
	client       *http.Client
	tokenManager *TokenManager
}

// New creates a new GitHub Copilot provider with the given token.
//
// Expected:
//   - token is a non-empty GitHub authentication token.
//
// Returns:
//   - A configured Provider with default base URL and HTTP client, or an error if token is empty.
//
// Side effects:
//   - None.
func New(token string) (*Provider, error) {
	if token == "" {
		return nil, errTokenRequired
	}
	client := &http.Client{}
	p := &Provider{
		baseURL: defaultBaseURL,
		client:  client,
	}
	p.tokenManager = NewDirectTokenManager(token)
	return p, nil
}

// NewWithOAuth creates a new GitHub Copilot provider using an OAuth token response.
//
// Expected:
//   - tokenResp contains a valid OAuth access token.
//
// Returns:
//   - A configured Provider with the OAuth token, default base URL and HTTP client.
//
// Side effects:
//   - None.
func NewWithOAuth(tokenResp *oauth.TokenResponse) (*Provider, error) {
	if tokenResp == nil || tokenResp.AccessToken == "" {
		return nil, errTokenRequired
	}
	client := &http.Client{}
	p := &Provider{
		baseURL: defaultBaseURL,
		client:  client,
	}
	p.tokenManager = NewDirectTokenManager(tokenResp.AccessToken)
	return p, nil
}

// NewFromOpenCodeOrFallback attempts to load GitHub Copilot credentials from OpenCode auth.json,
// falling back to OAuth storage or API key if OpenCode not available.
//
// Expected:
//   - opencodePath is a file path to OpenCode's auth.json (or empty string to skip OpenCode).
//   - oauthToken is a stored OAuth token (may be nil or empty).
//   - fallbackToken is a direct API key from config (may be empty).
//
// Returns:
//   - A configured Provider using OpenCode token if found.
//   - A configured Provider using oauthToken if OpenCode not available.
//   - A configured Provider using fallbackToken if neither OpenCode nor OAuth available.
//   - An error if OpenCode exists but cannot be parsed.
//   - An error if none of the credential sources provide a token.
//
// Side effects:
//   - Reads from opencodePath if provided.
func NewFromOpenCodeOrFallback(opencodePath string, oauthToken *oauth.TokenResponse, fallbackToken string) (*Provider, error) {
	if opencodePath != "" {
		authData, err := auth.LoadOpenCodeAuthFrom(opencodePath)
		if err != nil && !errors.Is(err, auth.ErrAuthFileNotFound) && !errors.Is(err, auth.ErrNoCredentials) {
			return nil, fmt.Errorf("loading opencode auth: %w", err)
		}
		if authData != nil && authData.GitHubCopilot != nil && authData.GitHubCopilot.Access != "" {
			return New(authData.GitHubCopilot.Access)
		}
	}

	if oauthToken != nil && oauthToken.AccessToken != "" {
		return NewWithOAuth(oauthToken)
	}

	if fallbackToken != "" {
		return New(fallbackToken)
	}

	return nil, errTokenRequired
}

// SetBaseURL sets the base URL for the GitHub Copilot API endpoint.
//
// Expected:
//   - url is a valid HTTP or HTTPS URL string.
//
// Side effects:
//   - Mutates the baseURL field of the Provider.
func (p *Provider) SetBaseURL(url string) {
	p.baseURL = strings.TrimSuffix(url, "/")
	if p.tokenManager != nil {
		if exchanger, ok := p.tokenManager.exchanger.(*TokenExchangerImpl); ok {
			exchanger.BaseURL = p.baseURL
		}
	}
}

// Name returns the provider name.
//
// Returns:
//   - The string "github-copilot".
//
// Side effects:
//   - None.
func (p *Provider) Name() string {
	return providerName
}

// Models returns the list of available models from the GitHub Copilot API.
//
// Returns:
//   - A slice of provider.Model fetched from the Copilot models endpoint.
//   - A hardcoded fallback list if the API call fails.
//
// Side effects:
//   - Makes an HTTP GET request to the Copilot models endpoint.
func (p *Provider) Models() ([]provider.Model, error) {
	models, err := p.fetchModels()
	if err == nil {
		return models, nil
	}
	return fallbackModels(), nil
}

// fetchModels queries the Copilot API for available models.
//
// Returns:
//   - A slice of provider.Model values from the API.
//   - An error if the API call fails.
//
// Side effects:
//   - Makes an HTTP GET request to the Copilot models endpoint.
func (p *Provider) fetchModels() ([]provider.Model, error) {
	endpoint := p.baseURL + "/models"

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("creating models request: %w", err)
	}
	token, err := p.tokenManager.EnsureToken(context.Background())
	if err != nil {
		return nil, fmt.Errorf("fetchModels: %w", err)
	}
	setHeaders(req, token)

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching copilot models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("copilot models: status %d", resp.StatusCode)
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}

	models := make([]provider.Model, 0, len(result.Data))
	for _, m := range result.Data {
		models = append(models, provider.Model{
			ID:            m.ID,
			Provider:      providerName,
			ContextLength: defaultContextLength,
		})
	}
	return models, nil
}

// fallbackModels returns a hardcoded list of known GitHub Copilot models.
//
// Returns:
//   - A static slice of well-known Copilot model definitions.
//
// Side effects:
//   - None.
func fallbackModels() []provider.Model {
	return []provider.Model{
		{ID: "gpt-4o", Provider: providerName, ContextLength: 128000},
		{ID: "gpt-4o-mini", Provider: providerName, ContextLength: 128000},
		{ID: "claude-3.5-sonnet", Provider: providerName, ContextLength: 200000},
		{ID: "o1-mini", Provider: providerName, ContextLength: 65536},
		{ID: "o1-preview", Provider: providerName, ContextLength: 32768},
	}
}

// Chat sends a chat request to GitHub Copilot and returns the response.
//
// Expected:
//   - ctx is a non-nil context for request cancellation.
//   - req contains a valid Model and at least one Message.
//
// Returns:
//   - A ChatResponse with the first choice from the Copilot API, or an error on failure.
//
// Side effects:
//   - Makes an HTTP POST request to the Copilot chat completions endpoint.
func (p *Provider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	endpoint := p.baseURL + "/chat/completions"

	payload := map[string]interface{}{
		"model":    req.Model,
		"messages": convertMessages(req.Messages),
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("marshalling request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("creating request: %w", err)
	}
	token, err := p.tokenManager.EnsureToken(ctx)
	if err != nil {
		return provider.ChatResponse{}, err
	}
	setHeaders(httpReq, token)

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return provider.ChatResponse{}, fmt.Errorf("copilot chat: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return provider.ChatResponse{}, fmt.Errorf("copilot chat: status %d", resp.StatusCode)
		}
		return provider.ChatResponse{}, fmt.Errorf("copilot chat: status %d: %s", resp.StatusCode, respBody)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return provider.ChatResponse{}, fmt.Errorf("decoding response: %w", err)
	}

	if len(result.Choices) == 0 {
		return provider.ChatResponse{}, errors.New("no choices in response")
	}

	return provider.ChatResponse{
		Message: provider.Message{
			Role:    result.Choices[0].Message.Role,
			Content: result.Choices[0].Message.Content,
		},
	}, nil
}

// Stream sends a chat request to GitHub Copilot and streams the response.
//
// Expected:
//   - ctx is a non-nil context for request cancellation.
//   - req contains a valid Model and at least one Message.
//
// Returns:
//   - A receive-only channel of StreamChunk values, or an error on setup failure.
//
// Side effects:
//   - Spawns a goroutine that makes an HTTP POST request and sends chunks to the channel.
func (p *Provider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, 16)

	go func() {
		defer close(ch)

		endpoint := p.baseURL + "/chat/completions"

		payload := map[string]interface{}{
			"model":    req.Model,
			"messages": convertMessages(req.Messages),
			"stream":   true,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
			return
		}
		token, err := p.tokenManager.EnsureToken(ctx)
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
			return
		}
		setHeaders(httpReq, token)

		resp, err := p.client.Do(httpReq)
		if err != nil {
			ch <- provider.StreamChunk{Error: err, Done: true}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, readErr := io.ReadAll(resp.Body)
			if readErr != nil {
				ch <- provider.StreamChunk{Error: fmt.Errorf("status %d", resp.StatusCode), Done: true}
				return
			}
			ch <- provider.StreamChunk{Error: fmt.Errorf("status %d: %s", resp.StatusCode, respBody), Done: true}
			return
		}

		streamSSE(ctx, ch, resp.Body)
	}()

	return ch, nil
}

// Embed is not implemented for GitHub Copilot.
//
// Expected:
//   - ctx is a context (unused).
//   - req is an EmbedRequest (unused).
//
// Returns:
//   - nil and nil; embedding is not supported by this provider.
//
// Side effects:
//   - None.
func (p *Provider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, ErrEmbedNotSupported
}

// streamSSE parses server-sent events from an SSE text/event-stream body and sends chunks to the channel.
//
// Expected:
//   - ctx is a non-nil context for cancellation.
//   - ch is an open, writable channel for stream chunks.
//   - body is an io.Reader positioned at the start of the SSE event stream.
//
// Side effects:
//   - Sends StreamChunk values to ch.
func streamSSE(ctx context.Context, ch chan<- provider.StreamChunk, body io.Reader) {
	scanner := bufio.NewScanner(body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			ch <- provider.StreamChunk{Done: true}
			return
		}

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		choice := extractChoice(event)
		if choice == nil {
			continue
		}

		if content := extractContent(choice); content != "" {
			select {
			case ch <- provider.StreamChunk{Content: content}:
			case <-ctx.Done():
				ch <- provider.StreamChunk{Error: ctx.Err(), Done: true}
				return
			}
		}

		if finish, ok := choice["finish_reason"].(string); ok && finish != "" {
			ch <- provider.StreamChunk{Done: true}
			return
		}
	}
}

// extractChoice extracts the first choice from an SSE event.
//
// Expected:
//   - event must be a valid SSE event map with a "choices" field.
//
// Returns:
//   - The first choice as a map, or nil if not found.
//
// Side effects:
//   - None.
func extractChoice(event map[string]interface{}) map[string]interface{} {
	choices, ok := event["choices"].([]interface{})
	if !ok || len(choices) == 0 {
		return nil
	}
	choice, ok := choices[0].(map[string]interface{})
	if !ok {
		return nil
	}
	return choice
}

// extractContent extracts text content from a choice delta.
//
// Expected:
//   - choice must be a valid choice map with a "delta" field.
//
// Returns:
//   - The content string, or empty string if not found.
//
// Side effects:
//   - None.
func extractContent(choice map[string]interface{}) string {
	delta, ok := choice["delta"].(map[string]interface{})
	if !ok {
		return ""
	}
	content, ok := delta["content"].(string)
	if !ok {
		return ""
	}
	return content
}

// setHeaders sets the required HTTP headers for the Copilot API request.
//
// Expected:
//   - req must be a non-nil HTTP request.
//   - token must be a valid GitHub Copilot token.
//
// Returns:
//   - None.
//
// Side effects:
//   - Mutates the Header map of req.
func setHeaders(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", headerContentType)
	req.Header.Set("Accept", headerAccept)
	req.Header.Set("Copilot-Integration-Id", copilotIntegrationID)
	req.Header.Set("User-Agent", copilotUserAgent)
	req.Header.Set("Editor-Version", copilotEditorVersion)
	req.Header.Set("Editor-Plugin-Version", copilotPluginVersion)
	req.Header.Set("Openai-Intent", "conversation-edits")
}

// convertMessages converts provider.Message slice to the Copilot API format.
//
// Expected:
//   - msgs must be a slice of provider.Message values.
//
// Returns:
//   - A slice of string maps suitable for the Copilot chat API.
//
// Side effects:
//   - Allocates a new result slice.
func convertMessages(msgs []provider.Message) []map[string]string {
	result := make([]map[string]string, 0, len(msgs))
	for _, m := range msgs {
		result = append(result, map[string]string{
			"role":    m.Role,
			"content": m.Content,
		})
	}
	return result
}
