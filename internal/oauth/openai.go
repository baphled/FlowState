package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const (
	openaiAuthorizeURL = "https://auth.openai.com/api/accounts/authorize"
	openaiTokenURL     = "https://auth.openai.com/api/accounts/oauth/token"
)

// PKCEParams holds the PKCE code verifier and challenge.
type PKCEParams struct {
	Verifier  string
	Challenge string
}

// generatePKCE creates a PKCE code verifier and S256 challenge.
//
// Returns:
//   - PKCEParams with verifier and base64url-encoded SHA256 challenge.
//   - An error if random generation fails.
//
// Side effects:
//   - Reads from crypto/rand.
func generatePKCE() (*PKCEParams, error) {
	verifier := make([]byte, 32)
	if _, err := rand.Read(verifier); err != nil {
		return nil, fmt.Errorf("generating pkce verifier: %w", err)
	}
	verifierStr := base64.RawURLEncoding.EncodeToString(verifier)

	hash := sha256.Sum256([]byte(verifierStr))
	challenge := base64.RawURLEncoding.EncodeToString(hash[:])

	return &PKCEParams{
		Verifier:  verifierStr,
		Challenge: challenge,
	}, nil
}

// AuthorizationResponse carries the result of a successful PKCE authorization.
type AuthorizationResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

// StartPKCEFlow initiates the OAuth 2.0 PKCE authorization flow for OpenAI.
//
// Expected:
//   - ctx is a non-nil context for cancellation.
//   - clientID is the registered OAuth application client ID.
//   - redirectPort is the local port for the callback server (0 for random).
//
// Returns:
//   - The URL to open in the user's browser for authorization.
//   - A channel that receives the AuthorizationResponse on success or an error.
//   - A function to call after the flow completes for cleanup.
//
// Side effects:
//   - Starts a local HTTP server on the given port.
//   - Blocks the returned channel until the flow completes.
func StartPKCEFlow(ctx context.Context, clientID string, redirectPort int) (string, <-chan *AuthorizationResponse, <-chan error, func(), error) {
	pkce, err := generatePKCE()
	if err != nil {
		return "", nil, nil, nil, err
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", redirectPort))
	if err != nil {
		return "", nil, nil, nil, fmt.Errorf("starting callback server: %w", err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	redirectURI := fmt.Sprintf("http://localhost:%d/callback", actualPort)

	authURL := buildOpenAIAuthURL(clientID, redirectURI, pkce.Challenge)

	resultCh := make(chan *AuthorizationResponse, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		errParam := r.URL.Query().Get("error")
		if errParam != "" {
			errDesc := r.URL.Query().Get("error_description")
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, "<html><body><h1>Authorization Failed</h1><p>%s</p></body></html>", errDesc)
			errCh <- fmt.Errorf("authorization error: %s — %s", errParam, errDesc)
			return
		}
		if code == "" {
			w.Header().Set("Content-Type", "text/html")
			w.Write([]byte("<html><body><h1>Error</h1><p>No authorization code received.</p></body></html>"))
			errCh <- fmt.Errorf("no authorization code in callback")
			return
		}

		token, err := exchangeCodeForToken(ctx, clientID, code, pkce.Verifier, redirectURI)
		if err != nil {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, "<html><body><h1>Token Exchange Failed</h1><p>%s</p></body></html>", err)
			errCh <- err
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html><body><h1>✓ Authenticated</h1><p>You may close this window and return to the terminal.</p></body></html>"))
		resultCh <- token
	})

	srv := &http.Server{Handler: mux}
	go func() {
		_ = srv.Serve(listener)
	}()

	cleanup := func() {
		srv.Close()
	}

	return authURL, resultCh, errCh, cleanup, nil
}

// buildOpenAIAuthURL builds the full OpenAI authorization URL for the PKCE flow.
//
// Expected: parameters for buildOpenAIAuthURL.
// Returns: result of buildOpenAIAuthURL.
// Side effects: None.
func buildOpenAIAuthURL(clientID, redirectURI, codeChallenge string) string {
	params := url.Values{
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"response_type":         {"code"},
		"scope":                 {"openid profile email offline_access"},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return openaiAuthorizeURL + "?" + params.Encode()
}

// exchangeCodeForToken exchanges an authorization code for tokens.
//
// Expected: parameters for exchangeCodeForToken.
// Returns: result of exchangeCodeForToken.
// Side effects: None.
func exchangeCodeForToken(ctx context.Context, clientID, code, codeVerifier, redirectURI string) (*AuthorizationResponse, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {codeVerifier},
		"redirect_uri":  {redirectURI},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, openaiTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("creating token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting token: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token exchange: status %d: %s", resp.StatusCode, respBody)
	}

	var result AuthorizationResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}

	return &result, nil
}
