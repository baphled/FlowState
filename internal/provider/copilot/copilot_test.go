package copilot_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	providerPkg "github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/copilot"
)

var _ = Describe("GitHub Copilot Provider", func() {
	Describe("New", func() {
		Context("when token is empty", func() {
			It("returns an error", func() {
				p, err := copilot.New("")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("token"))
				Expect(p).To(BeNil())
			})
		})

		Context("when token is provided", func() {
			It("returns a provider instance", func() {
				p, err := copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				Expect(p).NotTo(BeNil())
			})
		})
	})

	Describe("Name", func() {
		It("returns github-copilot", func() {
			p, err := copilot.New("ghp_test_token")
			Expect(err).NotTo(HaveOccurred())
			Expect(p.Name()).To(Equal("github-copilot"))
		})
	})

	Describe("Models", func() {
		var (
			server *httptest.Server
			p      *copilot.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when the API returns models successfully", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/copilot_next/v1/models"))
					Expect(r.Method).To(Equal(http.MethodGet))
					Expect(r.Header.Get("Authorization")).To(Equal("Bearer ghp_test_token"))

					resp := map[string]interface{}{
						"data": []map[string]interface{}{
							{"id": "gpt-5", "object": "model"},
							{"id": "claude-sonnet-4", "object": "model"},
						},
					}
					w.Header().Set("Content-Type", "application/json")
					err := json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				p, err = copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				p.SetBaseURL(server.URL)
			})

			It("returns the models from the API", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())
				Expect(models).To(HaveLen(2))
			})

			It("sets provider to github-copilot for all models", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())

				for _, m := range models {
					Expect(m.Provider).To(Equal("github-copilot"))
				}
			})

			It("preserves model IDs from the API", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())

				var modelIDs []string
				for _, m := range models {
					modelIDs = append(modelIDs, m.ID)
				}
				Expect(modelIDs).To(ContainElement("gpt-5"))
				Expect(modelIDs).To(ContainElement("claude-sonnet-4"))
			})

			It("sets default context length for all models", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())

				for _, m := range models {
					Expect(m.ContextLength).To(Equal(128000))
				}
			})
		})

		Context("when the API returns an error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))

				var err error
				p, err = copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				p.SetBaseURL(server.URL)
			})

			It("falls back to hardcoded models", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())
				Expect(models).NotTo(BeEmpty())
			})

			It("sets provider to github-copilot for fallback models", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())

				for _, m := range models {
					Expect(m.Provider).To(Equal("github-copilot"))
				}
			})

			It("includes known models in fallback", func() {
				models, err := p.Models()
				Expect(err).NotTo(HaveOccurred())

				var modelIDs []string
				for _, m := range models {
					modelIDs = append(modelIDs, m.ID)
				}
				Expect(modelIDs).To(ContainElement("gpt-4o"))
				Expect(modelIDs).To(ContainElement("claude-3.5-sonnet"))
			})
		})
	})

	Describe("Chat", func() {
		var (
			server *httptest.Server
			p      *copilot.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/copilot_next/v1/chat/completions"))
					Expect(r.Method).To(Equal(http.MethodPost))
					Expect(r.Header.Get("Authorization")).To(Equal("Bearer ghp_test_token"))
					Expect(r.Header.Get("Content-Type")).To(Equal("application/json"))
					Expect(r.Header.Get("Accept")).To(Equal("application/vnd.github.copilot-integration+json"))

					body, err := io.ReadAll(r.Body)
					Expect(err).NotTo(HaveOccurred())

					var req map[string]interface{}
					err = json.Unmarshal(body, &req)
					Expect(err).NotTo(HaveOccurred())
					Expect(req["model"]).To(Equal("gpt-4o"))

					resp := map[string]interface{}{
						"choices": []map[string]interface{}{
							{
								"index": 0,
								"message": map[string]interface{}{
									"role":    "assistant",
									"content": "Hello from Copilot!",
								},
								"finish_reason": "stop",
							},
						},
						"usage": map[string]interface{}{
							"prompt_tokens":     10,
							"completion_tokens": 5,
							"total_tokens":      15,
						},
					}
					w.Header().Set("Content-Type", "application/json")
					err = json.NewEncoder(w).Encode(resp)
					Expect(err).NotTo(HaveOccurred())
				}))

				var err error
				p, err = copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				p.SetBaseURL(server.URL)
			})

			It("returns chat response with message content", func() {
				ctx := context.Background()
				resp, err := p.Chat(ctx, providerPkg.ChatRequest{
					Model: "gpt-4o",
					Messages: []providerPkg.Message{
						{Role: "user", Content: "Hello"},
					},
				})
				Expect(err).NotTo(HaveOccurred())
				Expect(resp.Message.Role).To(Equal("assistant"))
				Expect(resp.Message.Content).To(Equal("Hello from Copilot!"))
			})
		})

		Context("when server returns non-200 status", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
				}))

				var err error
				p, err = copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				p.SetBaseURL(server.URL)
			})

			It("returns an error", func() {
				ctx := context.Background()
				_, err := p.Chat(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).To(HaveOccurred())
			})
		})
	})

	Describe("Stream", func() {
		var (
			server *httptest.Server
			p      *copilot.Provider
		)

		AfterEach(func() {
			if server != nil {
				server.Close()
			}
		})

		Context("when server returns valid streaming response", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					Expect(r.URL.Path).To(Equal("/copilot_next/v1/chat/completions"))
					Expect(r.Header.Get("Authorization")).To(Equal("Bearer ghp_test_token"))
					Expect(r.Header.Get("Accept")).To(Equal("application/vnd.github.copilot-integration+json"))

					w.Header().Set("Content-Type", "application/json")

					chunks := []string{
						`{"choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}`,
						`{"choices":[{"delta":{"content":" world"},"finish_reason":null}]}`,
						`{"choices":[{"delta":{},"finish_reason":"stop"}]}`,
					}
					for _, chunk := range chunks {
						fmt.Fprintf(w, "%s\n", chunk)
					}
				}))

				var err error
				p, err = copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				p.SetBaseURL(server.URL)
			})

			It("returns chunks from streaming response", func() {
				ctx := context.Background()
				ch, err := p.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var chunks []providerPkg.StreamChunk
				for chunk := range ch {
					chunks = append(chunks, chunk)
				}

				Expect(chunks).To(HaveLen(3))
				Expect(chunks[0].Content).To(Equal("Hello"))
				Expect(chunks[1].Content).To(Equal(" world"))
				Expect(chunks[2].Done).To(BeTrue())
			})
		})

		Context("when server returns error", func() {
			BeforeEach(func() {
				server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))

				var err error
				p, err = copilot.New("ghp_test_token")
				Expect(err).NotTo(HaveOccurred())
				p.SetBaseURL(server.URL)
			})

			It("returns error chunk", func() {
				ctx := context.Background()
				ch, err := p.Stream(ctx, providerPkg.ChatRequest{
					Model:    "gpt-4o",
					Messages: []providerPkg.Message{{Role: "user", Content: "Hello"}},
				})
				Expect(err).NotTo(HaveOccurred())

				var lastChunk providerPkg.StreamChunk
				for chunk := range ch {
					lastChunk = chunk
				}
				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Done).To(BeTrue())
			})
		})
	})

	Describe("Embed", func() {
		It("returns nil, nil", func() {
			p, err := copilot.New("ghp_test_token")
			Expect(err).NotTo(HaveOccurred())
			emb, err := p.Embed(context.Background(), providerPkg.EmbedRequest{Input: "test"})
			Expect(err).ToNot(HaveOccurred())
			Expect(emb).To(BeNil())
		})
	})
})
