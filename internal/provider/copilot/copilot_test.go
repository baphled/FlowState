package copilot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/oauth"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
)

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

var _ = Describe("NewFromConfig", func() {
	Context("when an OAuth token is supplied", func() {
		It("uses the OAuth token", func() {
			oauthToken := &oauth.TokenResponse{AccessToken: "gho_oauth_token"}
			p, err := copilot.NewFromConfig(oauthToken, "")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
			Expect(p.Name()).To(Equal("github-copilot"))
		})
	})

	Context("when only a fallback token is supplied", func() {
		It("uses the fallback token", func() {
			p, err := copilot.NewFromConfig(nil, "ghp_fallback")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when the OAuth token has an empty access token", func() {
		It("falls through to the fallback token", func() {
			p, err := copilot.NewFromConfig(&oauth.TokenResponse{AccessToken: ""}, "ghp_fb")
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
		})
	})

	Context("when no credential source provides a token", func() {
		It("returns an error", func() {
			p, err := copilot.NewFromConfig(nil, "")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
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
					"id":      "chatcmpl-test",
					"object":  "chat.completion",
					"model":   "gpt-4o",
					"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
					"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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
					"id":      "chatcmpl-test",
					"object":  "chat.completion",
					"model":   "gpt-4o",
					"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
					"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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
		})

		It("serialises tool role messages correctly in the request body", func() {
			var capturedBody []byte
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":      "chatcmpl-test",
					"object":  "chat.completion",
					"model":   "gpt-4o",
					"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "done"}, "finish_reason": "stop"}},
					"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
				})
			}))
			defer ts.Close()

			p, err := copilot.New("direct_token_tool_msg")
			Expect(err).NotTo(HaveOccurred())
			p.SetBaseURL(ts.URL)

			req := provider.ChatRequest{
				Model: "gpt-4o",
				Messages: []provider.Message{
					{Role: "user", Content: "call the tool"},
					{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{ID: "tc1", Name: "bash", Arguments: map[string]interface{}{"cmd": "ls"}}}},
					{Role: "tool", Content: "file1.txt\nfile2.txt", ToolCalls: []provider.ToolCall{{ID: "tc1"}}},
				},
			}
			_, err = p.Chat(context.Background(), req)
			Expect(err).NotTo(HaveOccurred())

			var body map[string]interface{}
			Expect(json.Unmarshal(capturedBody, &body)).To(Succeed())
			messages, ok := body["messages"].([]interface{})
			Expect(ok).To(BeTrue())
			Expect(messages).To(HaveLen(3))

			toolMsg := messages[2].(map[string]interface{})
			Expect(toolMsg["role"]).To(Equal("tool"))
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
				"id":      "chatcmpl-test",
				"object":  "chat.completion",
				"model":   "gpt-4o",
				"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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
				"id":      "chatcmpl-test",
				"object":  "chat.completion",
				"model":   "gpt-4o",
				"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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
				"id":      "chatcmpl-test",
				"object":  "chat.completion",
				"model":   "gpt-4o",
				"choices": []map[string]interface{}{{"index": 0, "message": map[string]string{"role": "assistant", "content": "hi"}, "finish_reason": "stop"}},
				"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
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

var _ = Describe("Tool Support", func() {
	var ctx context.Context

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("sends tools array in request body when tools are provided", func() {
		var capturedBody []byte
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			Expect(ok).To(BeTrue())
			fmt.Fprintf(w, "data: %s\n\n", `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`)
			flusher.Flush()
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}))
		defer ts.Close()

		p, err := copilot.New("gho_tools_test")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		req := provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "use the tool"}},
			Tools: []provider.Tool{
				{
					Name:        "bash",
					Description: "Run a bash command",
					Schema: provider.ToolSchema{
						Type:       "object",
						Properties: map[string]interface{}{"cmd": map[string]interface{}{"type": "string"}},
						Required:   []string{"cmd"},
					},
				},
			},
		}
		ch, err := p.Stream(ctx, req)
		Expect(err).NotTo(HaveOccurred())
		filterContent(collectChunks(ch))

		Expect(capturedBody).NotTo(BeEmpty())
		var body map[string]interface{}
		Expect(json.Unmarshal(capturedBody, &body)).To(Succeed())
		tools, ok := body["tools"].([]interface{})
		Expect(ok).To(BeTrue(), "expected tools array in request body")
		Expect(tools).To(HaveLen(1))
		tool := tools[0].(map[string]interface{})
		fn := tool["function"].(map[string]interface{})
		Expect(fn["name"]).To(Equal("bash"))
	})

	It("omits tools key when no tools are provided", func() {
		var capturedBody []byte
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedBody, _ = io.ReadAll(r.Body)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			Expect(ok).To(BeTrue())
			fmt.Fprintf(w, "data: %s\n\n", `{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"hi"},"finish_reason":null}]}`)
			flusher.Flush()
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}))
		defer ts.Close()

		p, err := copilot.New("gho_no_tools_test")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		ch, err := p.Stream(ctx, newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())
		collectChunks(ch)

		Expect(capturedBody).NotTo(BeEmpty())
		var body map[string]interface{}
		Expect(json.Unmarshal(capturedBody, &body)).To(Succeed())
		_, hasTools := body["tools"]
		Expect(hasTools).To(BeFalse(), "expected no tools key when tools are empty")
	})

	It("emits ToolCall chunk when stream contains tool_calls delta", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			Expect(ok).To(BeTrue())
			events := []string{
				`{"id":"chatcmpl-tc","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_abc","type":"function","function":{"name":"bash","arguments":""}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-tc","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-tc","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-tc","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, ev := range events {
				fmt.Fprintf(w, "data: %s\n\n", ev)
				flusher.Flush()
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}))
		defer ts.Close()

		p, err := copilot.New("gho_tool_call_stream_test")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		req := provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "run ls"}},
			Tools: []provider.Tool{
				{Name: "bash", Description: "Run bash", Schema: provider.ToolSchema{Type: "object"}},
			},
		}
		ch, err := p.Stream(ctx, req)
		Expect(err).NotTo(HaveOccurred())

		var toolChunks []provider.StreamChunk
		for chunk := range ch {
			if chunk.ToolCall != nil {
				toolChunks = append(toolChunks, chunk)
			}
		}
		Expect(toolChunks).To(HaveLen(1))
		Expect(toolChunks[0].ToolCall.Name).To(Equal("bash"))
		Expect(toolChunks[0].ToolCall.ID).To(Equal("call_abc"))
	})
})

var _ = Describe("SSE Streaming", func() {
	var (
		ctx context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
	})

	It("streams multiple content chunks from SSE events", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			flusher, ok := w.(http.Flusher)
			Expect(ok).To(BeTrue())
			chunks := []string{"Hello", " world", "!"}
			for _, c := range chunks {
				data := fmt.Sprintf(`{"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":%q},"finish_reason":null}]}`, c)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
			doneChunk := `{"id":"chatcmpl-sse","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
			fmt.Fprintf(w, "data: %s\n\n", doneChunk)
			flusher.Flush()
			fmt.Fprint(w, "data: [DONE]\n\n")
			flusher.Flush()
		}))
		defer ts.Close()

		p, err := copilot.New("sse_test_token")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		ch, err := p.Stream(ctx, newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())

		var contents []string
		for chunk := range ch {
			if chunk.Content != "" {
				contents = append(contents, chunk.Content)
			}
		}
		Expect(contents).To(Equal([]string{"Hello", " world", "!"}))
	})

	It("terminates stream on data: [DONE]", func() {
		ts := httptest.NewServer(http.HandlerFunc(sdkSSEHandler(
			`{"id":"chatcmpl-done","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-done","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)))
		defer ts.Close()

		p, err := copilot.New("sse_done_token")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		ch, err := p.Stream(ctx, newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())

		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).NotTo(BeEmpty())
		contentChunks := filterContent(chunks)
		Expect(contentChunks).To(HaveLen(1))
		Expect(contentChunks[0].Content).To(Equal("hi"))
		doneChunks := filterDone(chunks)
		Expect(doneChunks).NotTo(BeEmpty())
	})

	It("handles single chunk stream", func() {
		ts := httptest.NewServer(http.HandlerFunc(sdkSSEHandler(
			`{"id":"chatcmpl-single","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"only"},"finish_reason":null}]}`,
			`{"id":"chatcmpl-single","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		)))
		defer ts.Close()

		p, err := copilot.New("sse_single_token")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		ch, err := p.Stream(ctx, newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())

		var contents []string
		for chunk := range ch {
			if chunk.Content != "" {
				contents = append(contents, chunk.Content)
			}
		}
		Expect(contents).To(Equal([]string{"only"}))
	})

	It("returns error chunk for non-200 status", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
		}))
		defer ts.Close()

		p, err := copilot.New("sse_error_token")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		ch, err := p.Stream(ctx, newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())

		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).NotTo(BeEmpty())
		lastChunk := chunks[len(chunks)-1]
		Expect(lastChunk.Error).To(HaveOccurred())
	})

	It("handles context cancellation gracefully", func() {
		cancelCtx, cancel := context.WithCancel(ctx)
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			flusher, ok := w.(http.Flusher)
			Expect(ok).To(BeTrue())
			for i := range 1000 {
				data := fmt.Sprintf(`{"id":"chatcmpl-cancel","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"chunk%d"},"finish_reason":null}]}`, i)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}))
		defer ts.Close()

		p, err := copilot.New("sse_cancel_token")
		Expect(err).NotTo(HaveOccurred())
		p.SetBaseURL(ts.URL)

		ch, err := p.Stream(cancelCtx, newTestChatRequest())
		Expect(err).NotTo(HaveOccurred())

		cancel()

		done := make(chan struct{})
		go func() {
			defer close(done)
			for chunk := range ch {
				_ = chunk
			}
		}()
		Eventually(done, 5*time.Second).Should(BeClosed())
	})
})

func sdkSSEHandler(events ...string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		flusher, ok := w.(http.Flusher)
		if !ok {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for _, ev := range events {
			fmt.Fprintf(w, "data: %s\n\n", ev)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

func collectChunks(ch <-chan provider.StreamChunk) []provider.StreamChunk {
	var result []provider.StreamChunk
	for c := range ch {
		result = append(result, c)
	}
	return result
}

func filterContent(chunks []provider.StreamChunk) []provider.StreamChunk {
	var result []provider.StreamChunk
	for i := range chunks {
		if chunks[i].Content != "" {
			result = append(result, chunks[i])
		}
	}
	return result
}

func filterDone(chunks []provider.StreamChunk) []provider.StreamChunk {
	var result []provider.StreamChunk
	for i := range chunks {
		if chunks[i].Done {
			result = append(result, chunks[i])
		}
	}
	return result
}
