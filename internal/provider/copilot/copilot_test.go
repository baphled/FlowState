package copilot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/oauth"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
)

// ... (existing Describe blocks remain unchanged)

var _ = Describe("Token Exchange and Manager", func() {
	var (
		ctx context.Context
	)
	BeforeEach(func() {
		ctx = context.Background()
	})

	It("exchanges token successfully", func() {
		calls := int32(0)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "copilot_token_123",
				"expires_at": time.Now().Unix() + 3600,
			})
		}))
		defer ts.Close()
		ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
		tm := copilot.NewTokenManager("gho_test", ex)
		tok, err := tm.EnsureToken(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tok).To(Equal("copilot_token_123"))
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)))
	})

	It("returns error on HTTP failure", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()
		ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
		tm := copilot.NewTokenManager("gho_test", ex)
		_, err := tm.EnsureToken(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("status"))
	})

	It("returns error on decode failure", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not-json"))
		}))
		defer ts.Close()
		ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
		tm := copilot.NewTokenManager("gho_test", ex)
		_, err := tm.EnsureToken(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("decoding"))
	})

	It("returns error on empty token", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "",
				"expires_at": time.Now().Unix() + 3600,
			})
		}))
		defer ts.Close()
		ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
		tm := copilot.NewTokenManager("gho_test", ex)
		_, err := tm.EnsureToken(ctx)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("empty token"))
	})

	It("caches token until expiry, then refreshes", func() {
		var calls int32
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      fmt.Sprintf("copilot_token_%d", atomic.LoadInt32(&calls)),
				"expires_at": time.Now().Unix() + 3600,
			})
		}))
		defer ts.Close()
		ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
		tm := copilot.NewTokenManager("gho_test", ex)
		tok1, err := tm.EnsureToken(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tok1).To(Equal("copilot_token_1"))
		tok2, err := tm.EnsureToken(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tok2).To(Equal(tok1))
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)))
		tm.SetExpiresAt(time.Now().Unix() - 10)
		tok3, err := tm.EnsureToken(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(tok3).NotTo(Equal(tok1))
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(2)))
	})

	It("is concurrency safe: only one exchange on parallel calls", func() {
		var calls int32
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&calls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"token":      "copilot_token_conc",
				"expires_at": time.Now().Unix() + 3600,
			})
		}))
		defer ts.Close()
		ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
		tm := copilot.NewTokenManager("gho_test", ex)
		wg := sync.WaitGroup{}
		results := make([]string, 10)
		wg.Add(10)
		for idx := range results {
			go func(idx int) {
				defer wg.Done()
				tok, err := tm.EnsureToken(ctx)
				Expect(err).NotTo(HaveOccurred())
				results[idx] = tok
			}(idx)
		}
		wg.Wait()
		for _, tok := range results {
			Expect(tok).To(Equal("copilot_token_conc"))
		}
		Expect(atomic.LoadInt32(&calls)).To(Equal(int32(1)))
	})
})

var _ = Describe("NewFromOpenCodeOrFallback", func() {
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "copilot-test-*")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(os.RemoveAll(tmpDir)).To(Succeed())
	})

	Context("when opencode auth.json does not exist", func() {
		It("falls through to fallback token", func() {
			nonExistent := filepath.Join(tmpDir, "nonexistent", "auth.json")
			p, err := copilot.NewFromOpenCodeOrFallback(nonExistent, nil, "ghp_fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
			Expect(p.Name()).To(Equal("github-copilot"))
		})
	})

	Context("when opencode auth.json has no copilot credentials", func() {
		It("falls through to fallback token", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"anthropic": {"type": "oauth", "access": "sk-ant-test"}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := copilot.NewFromOpenCodeOrFallback(authPath, nil, "ghp_fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode auth.json has valid copilot credentials", func() {
		It("returns a provider using the opencode token", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"github-copilot": {"type": "oauth", "access": "gho_valid_token"}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := copilot.NewFromOpenCodeOrFallback(authPath, nil, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode auth.json contains invalid JSON", func() {
		It("returns an error", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			Expect(os.WriteFile(authPath, []byte("not json"), 0o600)).To(Succeed())
			p, err := copilot.NewFromOpenCodeOrFallback(authPath, nil, "ghp_fallback")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})
	})

	Context("when opencodePath is empty", func() {
		It("falls through to OAuth token", func() {
			oauthToken := &oauth.TokenResponse{AccessToken: "gho_oauth_token"}
			p, err := copilot.NewFromOpenCodeOrFallback("", oauthToken, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when opencode unavailable and OAuth token provided", func() {
		It("uses the OAuth token", func() {
			nonExistent := filepath.Join(tmpDir, "nonexistent", "auth.json")
			oauthToken := &oauth.TokenResponse{AccessToken: "gho_oauth_token"}
			p, err := copilot.NewFromOpenCodeOrFallback(nonExistent, oauthToken, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when no credential source provides a token", func() {
		It("returns an error", func() {
			nonExistent := filepath.Join(tmpDir, "nonexistent", "auth.json")
			p, err := copilot.NewFromOpenCodeOrFallback(nonExistent, nil, "")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
		})
	})

	Context("when opencode has empty access token", func() {
		It("falls through to fallback token", func() {
			authPath := filepath.Join(tmpDir, "auth.json")
			content := `{"github-copilot": {"type": "oauth", "access": ""}}`
			Expect(os.WriteFile(authPath, []byte(content), 0o600)).To(Succeed())
			p, err := copilot.NewFromOpenCodeOrFallback(authPath, nil, "ghp_fb")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})
})

var _ = Describe("Authorization and Headers", func() {
	Context("token exchange", func() {
		It("sends Bearer authorization, not token prefix", func() {
			var capturedAuthHeader string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedAuthHeader = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"token":      "copilot_token_bearer",
					"expires_at": time.Now().Unix() + 3600,
				})
			}))
			defer ts.Close()
			ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
			tm := copilot.NewTokenManager("gho_test_bearer", ex)
			_, err := tm.EnsureToken(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedAuthHeader).To(HavePrefix("Bearer "))
			Expect(capturedAuthHeader).NotTo(HavePrefix("token "))
		})

		It("includes all required Copilot headers", func() {
			var capturedHeaders http.Header
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedHeaders = r.Header.Clone()
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"token":      "copilot_token_hdrs",
					"expires_at": time.Now().Unix() + 3600,
				})
			}))
			defer ts.Close()
			ex := &copilot.TokenExchangerImpl{Client: ts.Client(), BaseURL: ts.URL}
			tm := copilot.NewTokenManager("gho_test_hdrs", ex)
			_, err := tm.EnsureToken(context.Background())
			Expect(err).NotTo(HaveOccurred())
			Expect(capturedHeaders.Get("User-Agent")).To(Equal("GitHubCopilotChat/0.35.0"))
			Expect(capturedHeaders.Get("Editor-Version")).To(Equal("vscode/1.107.0"))
			Expect(capturedHeaders.Get("Editor-Plugin-Version")).To(Equal("copilot-chat/0.35.0"))
			Expect(capturedHeaders.Get("Copilot-Integration-Id")).To(Equal("vscode-chat"))
		})
	})

	Context("API requests", func() {
		It("uses correct endpoint paths", func() {
			var capturedPaths []string
			var mu sync.Mutex
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				capturedPaths = append(capturedPaths, r.URL.Path)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/models" {
					_ = json.NewEncoder(w).Encode(map[string]interface{}{
						"data": []map[string]string{{"id": "gpt-4o"}},
					})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"choices": []map[string]interface{}{
						{"message": map[string]string{"role": "assistant", "content": "hi"}},
					},
				})
			}))
			defer ts.Close()
			p, err := copilot.New("direct_token_paths")
			Expect(err).NotTo(HaveOccurred())
			p.SetBaseURL(ts.URL)

			_, _ = p.Models()
			_, _ = p.Chat(context.Background(), newTestChatRequest())

			Expect(capturedPaths).To(ContainElement("/models"))
			Expect(capturedPaths).To(ContainElement("/chat/completions"))
			Expect(capturedPaths).NotTo(ContainElement("/copilot_next/v1/models"))
			Expect(capturedPaths).NotTo(ContainElement("/copilot_next/v1/chat/completions"))
		})

		It("includes all required Copilot headers", func() {
			var capturedHeaders http.Header
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/chat/completions" {
					capturedHeaders = r.Header.Clone()
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"choices": []map[string]interface{}{
						{"message": map[string]string{"role": "assistant", "content": "hi"}},
					},
				})
			}))
			defer ts.Close()
			p, err := copilot.New("direct_token_headers")
			Expect(err).NotTo(HaveOccurred())
			p.SetBaseURL(ts.URL)

			_, _ = p.Chat(context.Background(), newTestChatRequest())

			Expect(capturedHeaders).NotTo(BeNil())
			Expect(capturedHeaders.Get("Authorization")).To(HavePrefix("Bearer "))
			Expect(capturedHeaders.Get("User-Agent")).To(Equal("GitHubCopilotChat/0.35.0"))
			Expect(capturedHeaders.Get("Editor-Version")).To(Equal("vscode/1.107.0"))
			Expect(capturedHeaders.Get("Editor-Plugin-Version")).To(Equal("copilot-chat/0.35.0"))
			Expect(capturedHeaders.Get("Copilot-Integration-Id")).To(Equal("vscode-chat"))
			Expect(capturedHeaders.Get("Openai-Intent")).To(Equal("conversation-edits"))
			Expect(capturedHeaders.Get("Content-Type")).To(Equal("application/json"))
			Expect(capturedHeaders.Get("Accept")).To(Equal("application/json"))
		})
	})
})

var _ = Describe("Direct Token Management", func() {
	It("uses gho_ token directly as Bearer without token exchange", func() {
		var capturedAuth string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "hi"}},
				},
			})
		}))
		defer ts.Close()

		p, err := copilot.New("gho_direct_bearer_test")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		_, err = p.Chat(context.Background(), newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(capturedAuth).To(Equal("Bearer gho_direct_bearer_test"))
	})

	It("uses gho_ OAuth token directly as Bearer without token exchange", func() {
		var capturedAuth string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "hi"}},
				},
			})
		}))
		defer ts.Close()

		oauthToken := &oauth.TokenResponse{AccessToken: "gho_oauth_direct_test"}
		p, err := copilot.NewWithOAuth(oauthToken)
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		_, err = p.Chat(context.Background(), newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(capturedAuth).To(Equal("Bearer gho_oauth_direct_test"))
	})

	It("never contacts a token exchange endpoint for gho_ tokens", func() {
		var exchangeCalls int32
		exchangeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&exchangeCalls, 1)
			w.WriteHeader(http.StatusNotFound)
		}))
		defer exchangeServer.Close()

		chatServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"choices": []map[string]interface{}{
					{"message": map[string]string{"role": "assistant", "content": "hi"}},
				},
			})
		}))
		defer chatServer.Close()

		p, err := copilot.New("gho_no_exchange_test")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(chatServer.URL)

		_, err = p.Chat(context.Background(), newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())
		Expect(atomic.LoadInt32(&exchangeCalls)).To(Equal(int32(0)))
	})
})

func newTestChatRequest() provider.ChatRequest {
	return provider.ChatRequest{
		Model: "gpt-4o",
		Messages: []provider.Message{
			{Role: "user", Content: "hello"},
		},
	}
}
