package openaicompat_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"time"

	anthropicAPI "github.com/anthropics/anthropic-sdk-go"
	ollamaAPI "github.com/ollama/ollama/api"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
)

// GO: errors.As survives fmt.Errorf wrapping for all three SDKs.
// OpenAI exposes *openai.Error with StatusCode, Code, RawJSON(), DumpRequest(), and DumpResponse().
// Anthropic exposes *anthropic.Error with StatusCode, RequestID, RawJSON(), DumpRequest(), and DumpResponse(); the body must be parsed for error type/message.
// Ollama exposes *api.StatusError and *api.AuthorizationError with StatusCode plus Status/ErrorMessage or SigninURL; there is no raw body field.
var _ = Describe("OpenAI Compat", func() {
	Describe("BuildMessages", func() {
		Context("characterisation: role and content mapping", func() {
			It("maps user role and content to OpenAI UserMessage", func() {
				msgs := []provider.Message{{Role: "user", Content: "hello world"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfUser).NotTo(BeNil())
				Expect(result[0].OfUser.Content.OfString.Value).To(Equal("hello world"))
			})

			It("maps assistant role and content to OpenAI AssistantMessage", func() {
				msgs := []provider.Message{{Role: "assistant", Content: "hi there"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfAssistant).NotTo(BeNil())
				Expect(result[0].OfAssistant.Content.OfString.Value).To(Equal("hi there"))
			})

			It("maps system role and content to OpenAI SystemMessage", func() {
				msgs := []provider.Message{{Role: "system", Content: "you are helpful"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfSystem).NotTo(BeNil())
				Expect(result[0].OfSystem.Content.OfString.Value).To(Equal("you are helpful"))
			})

			It("maps tool role using ToolCalls[0].ID for the OpenAI ToolMessage", func() {
				msgs := []provider.Message{{
					Role:      "tool",
					Content:   "tool result",
					ToolCalls: []provider.ToolCall{{ID: "call_123"}},
				}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfTool).NotTo(BeNil())
				Expect(result[0].OfTool.Content.OfString.Value).To(Equal("tool result"))
				Expect(result[0].OfTool.ToolCallID).To(Equal("call_123"))
			})
		})

		It("converts user messages correctly", func() {
			msgs := []provider.Message{{Role: "user", Content: "hello"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("converts assistant messages correctly", func() {
			msgs := []provider.Message{{Role: "assistant", Content: "hi there"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("converts system messages correctly", func() {
			msgs := []provider.Message{{Role: "system", Content: "you are helpful"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("converts tool messages with ToolCalls ID", func() {
			msgs := []provider.Message{{
				Role:      "tool",
				Content:   "tool result",
				ToolCalls: []provider.ToolCall{{ID: "call_123"}},
			}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
		})

		It("returns empty slice for empty input", func() {
			result := openaicompat.BuildMessages([]provider.Message{})
			Expect(result).To(BeEmpty())
		})

		It("skips unknown roles", func() {
			msgs := []provider.Message{
				{Role: "user", Content: "hello"},
				{Role: "unknown", Content: "ignored"},
				{Role: "assistant", Content: "hi"},
			}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(2))
		})

		// M4-adjacent hardening (May 2026): the manager seam canonicalises
		// every provider.Message.Role to one of {user, assistant, system,
		// tool} before it reaches BuildMessages. The wire layer continues to
		// silently skip anything else (intentional, preserves existing
		// behaviour) but MUST log a Warn naming the role so any future
		// canonicalisation regression is visible at runtime instead of
		// vanishing into the void.
		It("logs a Warn naming the unknown role when one slips past the manager seam", func() {
			prev := slog.Default()
			DeferCleanup(func() { slog.SetDefault(prev) })

			var buf bytes.Buffer
			handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
			slog.SetDefault(slog.New(handler))

			msgs := []provider.Message{
				{Role: "user", Content: "hello"},
				{Role: "tool_error", Content: "uncanonicalised legacy"},
			}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1),
				"silent-drop behaviour must be preserved — the log is observability, not a behaviour change")

			out := buf.String()
			Expect(out).To(ContainSubstring("openaicompat"),
				"log must name the package so operators can grep by provider — log was: %s", out)
			Expect(out).To(ContainSubstring("unknown role"),
				"log must declare the condition with a single greppable phrase — log was: %s", out)
			Expect(out).To(ContainSubstring("role=tool_error"),
				"log must name the rogue role string so the regression site is identifiable — log was: %s", out)
		})

		It("skips tool messages without ToolCalls", func() {
			msgs := []provider.Message{{Role: "tool", Content: "orphan result"}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(BeEmpty())
		})

		It("preserves tool calls on assistant messages", func() {
			msgs := []provider.Message{{
				Role:    "assistant",
				Content: "Let me check the weather",
				ToolCalls: []provider.ToolCall{{
					ID:        "call_abc",
					Name:      "get_weather",
					Arguments: map[string]interface{}{"city": "London"},
				}},
			}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
			toolCalls := result[0].GetToolCalls()
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].ID).To(Equal("call_abc"))
			Expect(toolCalls[0].Function.Name).To(Equal("get_weather"))
		})

		It("preserves tool calls on assistant message with empty content", func() {
			msgs := []provider.Message{{
				Role:    "assistant",
				Content: "",
				ToolCalls: []provider.ToolCall{{
					ID:        "call_xyz",
					Name:      "search",
					Arguments: map[string]interface{}{"query": "golang"},
				}},
			}}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(1))
			toolCalls := result[0].GetToolCalls()
			Expect(toolCalls).To(HaveLen(1))
			Expect(toolCalls[0].ID).To(Equal("call_xyz"))
			Expect(toolCalls[0].Function.Name).To(Equal("search"))
		})

		It("converts multiple mixed messages", func() {
			msgs := []provider.Message{
				{Role: "system", Content: "be helpful"},
				{Role: "user", Content: "hello"},
				{Role: "assistant", Content: "hi"},
			}
			result := openaicompat.BuildMessages(msgs)
			Expect(result).To(HaveLen(3))
		})

		// Plan "Chat Attachments Backend (May 2026)" §6 task-11 / task-12 —
		// when a user message carries image Attachments, BuildMessages
		// lifts them into the OpenAI multimodal content-part shape ahead
		// of the text part. Anthropic-supported types (jpeg/png/gif/webp)
		// are passed through verbatim — model-level support is the
		// upstream model's responsibility, not the translator's.
		Context("image attachment threading (PR3 task-11 / task-12)", func() {
			pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
			jpgBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46}

			It("preserves the legacy string UserMessage shape when no attachments are present", func() {
				msgs := []provider.Message{{Role: "user", Content: "hello"}}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfUser).NotTo(BeNil())
				// Back-compat: with no attachments the union still
				// carries the simple string content, not an array.
				Expect(result[0].OfUser.Content.OfString.Value).To(Equal("hello"))
				Expect(result[0].OfUser.Content.OfArrayOfContentParts).To(BeNil())
			})

			It("converts a single PNG attachment to an image_url content part ahead of the text part", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "describe this", Attachments: []provider.Attachment{
						{ID: "a", MediaType: "image/png", Data: pngBytes, SizeBytes: int64(len(pngBytes))},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				Expect(result[0].OfUser).NotTo(BeNil())
				parts := result[0].OfUser.Content.OfArrayOfContentParts
				Expect(parts).To(HaveLen(2))
				// Image part first.
				Expect(parts[0].OfImageURL).NotTo(BeNil())
				Expect(parts[0].OfImageURL.ImageURL.URL).To(HavePrefix("data:image/png;base64,"))
				// Text part second.
				Expect(parts[1].OfText).NotTo(BeNil())
				Expect(parts[1].OfText.Text).To(Equal("describe this"))
			})

			It("preserves order across multiple image attachments", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "look at these", Attachments: []provider.Attachment{
						{ID: "a", MediaType: "image/png", Data: pngBytes},
						{ID: "b", MediaType: "image/jpeg", Data: jpgBytes},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				parts := result[0].OfUser.Content.OfArrayOfContentParts
				Expect(parts).To(HaveLen(3))
				Expect(parts[0].OfImageURL).NotTo(BeNil())
				Expect(parts[0].OfImageURL.ImageURL.URL).To(HavePrefix("data:image/png;base64,"))
				Expect(parts[1].OfImageURL).NotTo(BeNil())
				Expect(parts[1].OfImageURL.ImageURL.URL).To(HavePrefix("data:image/jpeg;base64,"))
				Expect(parts[2].OfText.Text).To(Equal("look at these"))
			})

			It("skips incomplete entries (empty MediaType or empty Data) and falls back to string when none remain", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "fallback", Attachments: []provider.Attachment{
						{ID: "empty-type", MediaType: "", Data: pngBytes},
						{ID: "empty-data", MediaType: "image/png", Data: nil},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				// With every attachment skipped the helper returns nil
				// parts, so the caller emits the legacy string union.
				Expect(result[0].OfUser.Content.OfString.Value).To(Equal("fallback"))
				Expect(result[0].OfUser.Content.OfArrayOfContentParts).To(BeNil())
			})

			It("preserves an empty text part when content is empty but attachments exist", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "", Attachments: []provider.Attachment{
						{ID: "a", MediaType: "image/png", Data: pngBytes},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				parts := result[0].OfUser.Content.OfArrayOfContentParts
				Expect(parts).To(HaveLen(2))
				Expect(parts[0].OfImageURL).NotTo(BeNil())
				Expect(parts[1].OfText).NotTo(BeNil())
				Expect(parts[1].OfText.Text).To(Equal(""))
			})

			It("encodes the image bytes via base64 in the data: URL", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "x", Attachments: []provider.Attachment{
						{ID: "a", MediaType: "image/png", Data: pngBytes},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				parts := result[0].OfUser.Content.OfArrayOfContentParts
				url := parts[0].OfImageURL.ImageURL.URL
				// data:image/png;base64,<encoded>
				Expect(url).To(HavePrefix("data:image/png;base64,"))
				encoded := strings.TrimPrefix(url, "data:image/png;base64,")
				decoded, decErr := base64.StdEncoding.DecodeString(encoded)
				Expect(decErr).NotTo(HaveOccurred())
				Expect(decoded).To(Equal(pngBytes))
			})
		})

		// Plan §6 task-15 — defence-in-depth document skip. PDFs that
		// reach the openaicompat translator (covers openai, copilot,
		// openzen, zai, ollamacloud — anything wrapping openaicompat)
		// are dropped with a structured slog.Warn. The upload-time
		// gate is the primary defence; this closes R13's
		// model-switch-mid-staging window.
		Context("defence-in-depth document-skip (PR4 task-15, AC-15-LogShape-Pinned)", func() {
			pdfBytes := []byte("%PDF-1.4\n%fake-pdf-body\n")
			pngBytes := []byte{0x89, 0x50, 0x4e, 0x47}

			It("drops a Kind=document attachment from the request body", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "discuss this", Attachments: []provider.Attachment{
						{ID: "doc-1", Kind: "document", MediaType: "application/pdf", Data: pdfBytes},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				Expect(result).To(HaveLen(1))
				// No documents got into the parts array — the message
				// falls back to the legacy string-shaped user content.
				Expect(result[0].OfUser).NotTo(BeNil())
				Expect(result[0].OfUser.Content.OfString.Value).To(Equal("discuss this"))
				Expect(result[0].OfUser.Content.OfArrayOfContentParts).To(BeNil())
			})

			It("mixed image+PDF: ships image, drops PDF, request still well-formed", func() {
				msgs := []provider.Message{
					{Role: "user", Content: "look", Attachments: []provider.Attachment{
						{ID: "img-1", Kind: "image", MediaType: "image/png", Data: pngBytes},
						{ID: "doc-1", Kind: "document", MediaType: "application/pdf", Data: pdfBytes},
					}},
				}
				result := openaicompat.BuildMessages(msgs)
				parts := result[0].OfUser.Content.OfArrayOfContentParts
				// Image + text only — PDF is silently dropped.
				Expect(parts).To(HaveLen(2))
				Expect(parts[0].OfImageURL).NotTo(BeNil())
				Expect(parts[1].OfText.Text).To(Equal("look"))
			})

			It("emits slog.Warn with the AC-15-LogShape-Pinned 4-field schema", func() {
				// Capture slog output to a buffer-backed handler so we
				// can assert the exact message + keys.
				var buf bytes.Buffer
				handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})
				prev := slog.Default()
				slog.SetDefault(slog.New(handler))
				defer slog.SetDefault(prev)

				msgs := []provider.Message{
					{Role: "user", Content: "x", Attachments: []provider.Attachment{
						{ID: "doc-xyz", Kind: "document", MediaType: "application/pdf", Data: pdfBytes},
					}},
				}
				_ = openaicompat.BuildMessages(msgs)

				var entry map[string]any
				Expect(json.Unmarshal(buf.Bytes(), &entry)).To(Succeed())
				Expect(entry).To(HaveKeyWithValue("msg",
					"attachment_dropped: provider does not support documents"))
				Expect(entry).To(HaveKeyWithValue("provider", "openaicompat"))
				Expect(entry).To(HaveKeyWithValue("attachment_id", "doc-xyz"))
				Expect(entry).To(HaveKeyWithValue("kind", "document"))
				Expect(entry).To(HaveKeyWithValue("media_type", "application/pdf"))
				Expect(entry).To(HaveKeyWithValue("level", "WARN"))
			})
		})
	})

	Describe("GateAttachmentRequestSize", func() {
		// Plan "Chat Attachments Backend (May 2026)" §6 task-11 / task-12
		// — pre-flight gate that mirrors the Anthropic provider's 25 MB
		// ceiling check (lifted to the shared seam in PR3 task-10).
		// Returns nil when within ceiling, wrapped sentinel when over.
		pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}

		It("returns nil for an empty request", func() {
			req := provider.ChatRequest{}
			Expect(openaicompat.GateAttachmentRequestSize(req)).To(BeNil())
		})

		It("returns nil for messages without attachments", func() {
			req := provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
					{Role: "assistant", Content: "hi"},
				},
			}
			Expect(openaicompat.GateAttachmentRequestSize(req)).To(BeNil())
		})

		It("returns nil when attachments are under the ceiling", func() {
			under := make([]byte, 1024*1024) // 1 MB
			copy(under, pngBytes)
			req := provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "ok", Attachments: []provider.Attachment{
						{ID: "a", MediaType: "image/png", Data: under},
					}},
				},
			}
			Expect(openaicompat.GateAttachmentRequestSize(req)).To(BeNil())
		})

		It("returns ErrAttachmentRequestTooLarge when attachments exceed the ceiling", func() {
			big := make([]byte, provider.MaxAttachmentRequestBytes())
			copy(big, pngBytes)
			extra := make([]byte, 1024*1024) // 1 MB over the 25 MB cap
			req := provider.ChatRequest{
				Messages: []provider.Message{
					{Role: "user", Content: "first", Attachments: []provider.Attachment{
						{ID: "a", MediaType: "image/png", Data: big},
					}},
					{Role: "user", Content: "second", Attachments: []provider.Attachment{
						{ID: "b", MediaType: "image/png", Data: extra},
					}},
				},
			}
			err := openaicompat.GateAttachmentRequestSize(req)
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, provider.ErrAttachmentRequestTooLarge)).To(BeTrue())
			Expect(err.Error()).To(ContainSubstring("openaicompat"))
		})
	})

	Describe("BuildTools", func() {
		Context("characterisation: multi-property schema mapping", func() {
			It("preserves all properties and required fields in the OpenAI parameters wrapper", func() {
				tools := []provider.Tool{{
					Name:        "search",
					Description: "Search for items",
					Schema: provider.ToolSchema{
						Type: "object",
						Properties: map[string]interface{}{
							"query": map[string]interface{}{"type": "string"},
							"limit": map[string]interface{}{"type": "integer"},
						},
						Required: []string{"query", "limit"},
					},
				}}
				result := openaicompat.BuildTools(tools)
				Expect(result).To(HaveLen(1))
				Expect(result[0].Function.Name).To(Equal("search"))
				Expect(result[0].Function.Description.Value).To(Equal("Search for items"))
				params := result[0].Function.Parameters
				Expect(params).To(HaveKey("properties"))
				Expect(params).To(HaveKey("required"))
				Expect(params["required"]).To(ConsistOf("query", "limit"))
			})
		})

		It("returns nil for empty tools slice", func() {
			result := openaicompat.BuildTools([]provider.Tool{})
			Expect(result).To(BeNil())
		})

		It("returns nil for nil tools slice", func() {
			result := openaicompat.BuildTools(nil)
			Expect(result).To(BeNil())
		})

		It("converts a single tool with all schema fields", func() {
			tools := []provider.Tool{{
				Name:        "get_weather",
				Description: "Get the weather",
				Schema: provider.ToolSchema{
					Type: "object",
					Properties: map[string]interface{}{
						"location": map[string]interface{}{"type": "string"},
					},
					Required: []string{"location"},
				},
			}}
			result := openaicompat.BuildTools(tools)
			Expect(result).To(HaveLen(1))
			Expect(result[0].Function.Name).To(Equal("get_weather"))
		})

		It("converts multiple tools", func() {
			tools := []provider.Tool{
				{
					Name:        "tool_a",
					Description: "First tool",
					Schema:      provider.ToolSchema{Type: "object"},
				},
				{
					Name:        "tool_b",
					Description: "Second tool",
					Schema:      provider.ToolSchema{Type: "object"},
				},
			}
			result := openaicompat.BuildTools(tools)
			Expect(result).To(HaveLen(2))
			Expect(result[0].Function.Name).To(Equal("tool_a"))
			Expect(result[1].Function.Name).To(Equal("tool_b"))
		})
	})

	Describe("BuildParams", func() {
		It("sets model and messages", func() {
			req := provider.ChatRequest{
				Model: "gpt-4o",
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
				},
			}
			params := openaicompat.BuildParams(req)
			Expect(params.Model).To(Equal("gpt-4o"))
			Expect(params.Messages).To(HaveLen(1))
		})

		It("includes tools when present", func() {
			req := provider.ChatRequest{
				Model: "gpt-4o",
				Messages: []provider.Message{
					{Role: "user", Content: "hello"},
				},
				Tools: []provider.Tool{{
					Name:        "my_tool",
					Description: "A tool",
					Schema:      provider.ToolSchema{Type: "object"},
				}},
			}
			params := openaicompat.BuildParams(req)
			Expect(params.Tools).To(HaveLen(1))
		})

		It("omits tools when empty", func() {
			req := provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "hi"}},
			}
			params := openaicompat.BuildParams(req)
			Expect(params.Tools).To(BeNil())
		})
	})

	Describe("ExtractToolCalls", func() {
		It("returns nil for empty slice", func() {
			result := openaicompat.ExtractToolCalls([]openaiAPI.ChatCompletionMessageToolCall{})
			Expect(result).To(BeNil())
		})

		It("returns nil for nil slice", func() {
			result := openaicompat.ExtractToolCalls(nil)
			Expect(result).To(BeNil())
		})

		It("converts a single tool call with ID, Name, and Arguments", func() {
			tc := unmarshalToolCall(`{"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}`)
			result := openaicompat.ExtractToolCalls([]openaiAPI.ChatCompletionMessageToolCall{tc})
			Expect(result).To(HaveLen(1))
			Expect(result[0].ID).To(Equal("call_1"))
			Expect(result[0].Name).To(Equal("get_weather"))
			Expect(result[0].Arguments).To(HaveKeyWithValue("city", "London"))
		})

		It("converts multiple tool calls", func() {
			tc1 := unmarshalToolCall(`{"id":"call_1","type":"function","function":{"name":"tool_a","arguments":"{}"}}`)
			tc2 := unmarshalToolCall(`{"id":"call_2","type":"function","function":{"name":"tool_b","arguments":"{\"x\":1}"}}`)
			result := openaicompat.ExtractToolCalls([]openaiAPI.ChatCompletionMessageToolCall{tc1, tc2})
			Expect(result).To(HaveLen(2))
			Expect(result[0].ID).To(Equal("call_1"))
			Expect(result[0].Name).To(Equal("tool_a"))
			Expect(result[1].ID).To(Equal("call_2"))
			Expect(result[1].Name).To(Equal("tool_b"))
		})
	})

	Describe("ParseChatResponse", func() {
		It("returns ErrNoChoices for nil response", func() {
			_, err := openaicompat.ParseChatResponse(nil)
			Expect(err).To(MatchError(provider.ErrNoChoices))
		})

		It("returns ErrNoChoices for empty choices", func() {
			resp := unmarshalCompletion(`{"id":"cmpl-1","model":"gpt-4o","choices":[],"object":"chat.completion","created":1}`)
			_, err := openaicompat.ParseChatResponse(resp)
			Expect(err).To(MatchError(provider.ErrNoChoices))
		})

		It("parses text response with role, content, and usage", func() {
			resp := unmarshalCompletion(`{
				"id":"cmpl-1","model":"gpt-4o","object":"chat.completion","created":1,
				"choices":[{"index":0,"message":{"role":"assistant","content":"Hello there"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}
			}`)
			result, err := openaicompat.ParseChatResponse(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Message.Role).To(Equal("assistant"))
			Expect(result.Message.Content).To(Equal("Hello there"))
			Expect(result.Usage.PromptTokens).To(Equal(10))
			Expect(result.Usage.CompletionTokens).To(Equal(5))
			Expect(result.Usage.TotalTokens).To(Equal(15))
		})

		It("parses response with tool calls", func() {
			resp := unmarshalCompletion(`{
				"id":"cmpl-1","model":"gpt-4o","object":"chat.completion","created":1,
				"choices":[{"index":0,"message":{
					"role":"assistant","content":"",
					"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}]
				},"finish_reason":"tool_calls"}],
				"usage":{"prompt_tokens":20,"completion_tokens":10,"total_tokens":30}
			}`)
			result, err := openaicompat.ParseChatResponse(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Message.Role).To(Equal("assistant"))
			Expect(result.Message.ToolCalls).To(HaveLen(1))
			Expect(result.Message.ToolCalls[0].ID).To(Equal("call_abc"))
			Expect(result.Message.ToolCalls[0].Name).To(Equal("get_weather"))
			Expect(result.Message.ToolCalls[0].Arguments).To(HaveKeyWithValue("city", "Paris"))
		})

		It("returns nil tool calls when response has no tool calls", func() {
			resp := unmarshalCompletion(`{
				"id":"cmpl-1","model":"gpt-4o","object":"chat.completion","created":1,
				"choices":[{"index":0,"message":{"role":"assistant","content":"plain text"},"finish_reason":"stop"}],
				"usage":{"prompt_tokens":5,"completion_tokens":3,"total_tokens":8}
			}`)
			result, err := openaicompat.ParseChatResponse(resp)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Message.ToolCalls).To(BeNil())
		})
	})
})

var _ = Describe("Spike: SDK error type introspection", func() {
	It("extracts OpenAI typed errors through wrapping", func() {
		err := newOpenAIError(`{"message":"invalid request","param":"model","type":"invalid_request_error","code":"invalid_model"}`, http.StatusBadRequest)
		var extracted *openaiAPI.Error
		Expect(errors.As(fmt.Errorf("openai provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected OpenAI error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusBadRequest))
		Expect(extracted.Code).To(Equal("invalid_model"))
		Expect(extracted.RawJSON()).To(ContainSubstring(`"code":"invalid_model"`))
	})

	It("extracts Anthropic typed errors through wrapping", func() {
		err := newAnthropicError(`{"message":"rate limited","type":"rate_limit_error"}`, http.StatusTooManyRequests, "req_123")
		var extracted *anthropicAPI.Error
		Expect(errors.As(fmt.Errorf("anthropic provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected Anthropic error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusTooManyRequests))
		Expect(extracted.RequestID).To(Equal("req_123"))
		Expect(extracted.RawJSON()).To(ContainSubstring(`"rate_limit_error"`))
	})

	It("extracts Ollama typed errors through wrapping", func() {
		err := &ollamaAPI.StatusError{StatusCode: http.StatusNotFound, Status: "404 Not Found", ErrorMessage: "model not found"}
		var extracted *ollamaAPI.StatusError
		Expect(errors.As(fmt.Errorf("ollama provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected Ollama status error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusNotFound))
		Expect(extracted.ErrorMessage).To(Equal("model not found"))
	})

	It("extracts Ollama authorisation errors through wrapping", func() {
		err := &ollamaAPI.AuthorizationError{StatusCode: http.StatusUnauthorized, Status: "401 Unauthorized", SigninURL: "https://ollama.com/signin"}
		var extracted *ollamaAPI.AuthorizationError
		Expect(errors.As(fmt.Errorf("ollama provider: %w", err), &extracted)).To(BeTrue())
		if extracted == nil {
			Fail("expected Ollama authorisation error to be extracted")
		}
		Expect(extracted.StatusCode).To(Equal(http.StatusUnauthorized))
		Expect(extracted.SigninURL).To(Equal("https://ollama.com/signin"))
	})
})

// ---
// RunStream streaming specs.
var _ = Describe("RunStream", func() {
	var server *httptest.Server

	AfterEach(func() {
		if server != nil {
			server.Close()
		}
	})

	It("streams text content chunks", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":" world!"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-1","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "Hello"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(3))
		Expect(chunks[0].Content).To(Equal("Hello"))
		Expect(chunks[1].Content).To(Equal(" world!"))
		Expect(chunks[2].Done).To(BeTrue())
	})

	It("streams tool call chunks", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_abc","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-2","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "Weather?"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(HaveLen(2))
		Expect(chunks[0].ToolCall).NotTo(BeNil())
		Expect(chunks[0].ToolCall.ID).To(Equal("call_abc"))
		Expect(chunks[0].ToolCall.Name).To(Equal("get_weather"))
		Expect(chunks[0].ToolCall.Arguments).To(HaveKeyWithValue("city", "London"))
		Expect(chunks[1].Done).To(BeTrue())
	})

	It("emits tool calls when the terminal chunk combines delta and finish_reason (github-copilot shape)", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-copilot","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"delegate","arguments":""}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-copilot","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent\":\"Explore\"}"}}]},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "delegate please"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}
		var toolCalls []provider.ToolCall
		for _, c := range collected {
			if c.ToolCall != nil {
				toolCalls = append(toolCalls, *c.ToolCall)
			}
		}
		Expect(toolCalls).To(HaveLen(1))
		Expect(toolCalls[0].Name).To(Equal("delegate"))
		Expect(toolCalls[0].ID).To(Equal("call_x"))
		Expect(toolCalls[0].Arguments).To(HaveKeyWithValue("agent", "Explore"))
	})

	It("emits tool calls when every chunk carries empty content alongside tool_calls (zai shape)", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-zai","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_y","type":"function","function":{"name":"read_file","arguments":""}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-zai","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"","tool_calls":[{"index":0,"function":{"arguments":"{\"path\":\"x.txt\"}"}}]},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "read file"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}
		var toolCalls []provider.ToolCall
		for _, c := range collected {
			if c.ToolCall != nil {
				toolCalls = append(toolCalls, *c.ToolCall)
			}
		}
		Expect(toolCalls).To(HaveLen(1))
		Expect(toolCalls[0].Name).To(Equal("read_file"))
		Expect(toolCalls[0].ID).To(Equal("call_y"))
		Expect(toolCalls[0].Arguments).To(HaveKeyWithValue("path", "x.txt"))
	})

	It("stamps EventType=\"tool_call\" on chunks emitted by the main streaming loop", func() {
		// Regression pin: without EventType set, the engine tool-loop gate at
		// internal/engine/engine.go:907 silently drops the tool call, producing
		// the 3-minute stall observed for github-copilot/zai/openzen/openai in
		// session-1775944430840782553. Anthropic and Ollama both stamp this
		// field; openaicompat must do the same for the JustFinishedToolCall
		// happy path (the main RunStream loop).
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-eventtype","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_et","type":"function","function":{"name":"get_weather","arguments":"{\"city\":\"London\"}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-eventtype","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "Weather?"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var toolCallChunks []provider.StreamChunk
		for chunk := range ch {
			if chunk.ToolCall != nil {
				toolCallChunks = append(toolCallChunks, chunk)
			}
		}
		Expect(toolCallChunks).To(HaveLen(1))
		Expect(toolCallChunks[0].EventType).To(Equal("tool_call"),
			"tool-call chunks from the main RunStream loop must carry EventType=\"tool_call\" "+
				"so the engine tool loop dispatches them; omitting the stamp caused the "+
				"non-anthropic silent-stall bug")
	})

	// Drop #1 — openaicompat reasoning_content emission.
	//
	// glm-4.6 (zai) and DeepSeek-R1 emit OpenAI-shaped chunks where the
	// reasoning tokens arrive as a non-standard `reasoning_content` field on
	// the delta — NOT the typed `content` field. The Go SDK's
	// ChatCompletionChunkChoiceDelta has no `Reasoning` member, so the data
	// only survives in `delta.RawJSON()` / `delta.JSON.ExtraFields`. Pre this
	// fix the dispatcher checked `delta.Content != ""` and silently dropped
	// every reasoning chunk; we measured 586 dropped deltas across a single
	// 92-second glm-4.6 call in the Phase 1d capture (live SSE wire idle for
	// the full 52-second reasoning phase).
	//
	// Contract: when a delta carries `reasoning_content`, RunStream MUST
	// emit a `provider.StreamChunk{Thinking: <text>}` whose Thinking field
	// holds the reasoning text. Existing content/tool-call paths are
	// unchanged. Providers that never emit reasoning_content (openai,
	// ollama, github-copilot text streams) are unaffected — the extraction
	// is a no-op when the field is absent.
	It("emits Thinking chunks when delta carries reasoning_content (glm-4.6 / DeepSeek-R1 shape)", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			// Real-shape glm-4.6 chunk: reasoning_content arrives in deltas
			// with empty content. After several reasoning chunks the model
			// switches to content. The dispatcher must emit thinking chunks
			// for the reasoning phase and content chunks for the reply.
			chunks := []string{
				`{"id":"chatcmpl-r1","object":"chat.completion.chunk","model":"glm-4.6","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think about this. "},"finish_reason":null}]}`,
				`{"id":"chatcmpl-r1","object":"chat.completion.chunk","model":"glm-4.6","choices":[{"index":0,"delta":{"reasoning_content":"The user is asking..."},"finish_reason":null}]}`,
				`{"id":"chatcmpl-r1","object":"chat.completion.chunk","model":"glm-4.6","choices":[{"index":0,"delta":{"content":"The answer is 42."},"finish_reason":null}]}`,
				`{"id":"chatcmpl-r1","object":"chat.completion.chunk","model":"glm-4.6","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "glm-4.6",
			Messages: []provider.Message{{Role: "user", Content: "What is the meaning of life?"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "zai")
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}

		var thinkingChunks []provider.StreamChunk
		var contentChunks []provider.StreamChunk
		for _, c := range collected {
			if c.Thinking != "" {
				thinkingChunks = append(thinkingChunks, c)
			}
			if c.Content != "" {
				contentChunks = append(contentChunks, c)
			}
		}

		Expect(thinkingChunks).To(HaveLen(2),
			"each reasoning_content delta MUST emit one thinking chunk; "+
				"got %d thinking chunks across collected=%v", len(thinkingChunks), collected)
		Expect(thinkingChunks[0].Thinking).To(Equal("Let me think about this. "))
		Expect(thinkingChunks[1].Thinking).To(Equal("The user is asking..."))

		// reasoning_content MUST NOT be conflated with Content — the chat
		// store renders Content as the visible reply and Thinking as
		// out-of-band reasoning. Conflating them would re-introduce the
		// JSON-leak class of bugs.
		for _, t := range thinkingChunks {
			Expect(t.Content).To(BeEmpty(),
				"thinking chunks MUST have empty Content; conflating reasoning with reply would leak private reasoning into the chat: %+v", t)
		}

		Expect(contentChunks).To(HaveLen(1),
			"the visible reply MUST be a single content chunk separate from reasoning")
		Expect(contentChunks[0].Content).To(Equal("The answer is 42."))
	})

	It("does not emit Thinking chunks for plain OpenAI providers (no reasoning_content field)", func() {
		// Providers that never emit reasoning_content (openai, github-copilot
		// text mode, ollama) MUST be unaffected by Drop #1 — the extraction
		// is a no-op when the field is absent. Without this guard, a regression
		// in the extraction logic could spuriously emit empty thinking chunks
		// and re-arm the watchdog from non-existent reasoning.
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-plain","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-plain","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "Hi"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "openai")
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}

		for _, c := range collected {
			Expect(c.Thinking).To(BeEmpty(),
				"plain OpenAI providers MUST NOT emit Thinking chunks: %+v", c)
		}
	})

	It("stamps EventType=\"tool_call\" on chunks emitted by flushAccumulatedToolCalls (github-copilot shape)", func() {
		// Regression pin for the flush path: github-copilot combines the final
		// tool_calls delta with finish_reason in one chunk, so JustFinishedToolCall
		// never fires and flushAccumulatedToolCalls is the only emitter. It must
		// also stamp EventType so the engine tool loop dispatches the call.
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-flush","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_flush","type":"function","function":{"name":"delegate","arguments":""}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-flush","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"agent\":\"Explore\"}"}}]},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "delegate please"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var toolCallChunks []provider.StreamChunk
		for chunk := range ch {
			if chunk.ToolCall != nil {
				toolCallChunks = append(toolCallChunks, chunk)
			}
		}
		Expect(toolCallChunks).To(HaveLen(1))
		Expect(toolCallChunks[0].EventType).To(Equal("tool_call"),
			"tool-call chunks from flushAccumulatedToolCalls must carry EventType=\"tool_call\" "+
				"so the engine tool loop dispatches them; this is the github-copilot code path")
	})

	// Inline-XML tool-call recovery from the reasoning_content stream.
	//
	// glm-4.5 / glm-4.6 (zai) sometimes emit tool calls as a literal
	// `<tool_call>...</tool_call>` block inside the reasoning_content channel
	// instead of populating the structured `tool_calls` array. The model then
	// stops, expecting a tool result; the runtime, having parsed nothing, sees
	// an empty assistant turn and shows the soft-error affordance "The model
	// worked through this turn but stopped before replying. Try sending the
	// prompt again." That affordance is treating a symptom — the underlying
	// defect is that we drop a tool call the model actually emitted.
	//
	// Reproducer: session 718b5d51-f01b-45f0-80bb-31329a9d44e7 message 9.
	// Persisted Thinking text was:
	//
	//   "\n<think>\n<tool_call>bash\n<arg_key>command</arg_key>\n"+
	//   "<arg_value>find /home/baphled/vaults -name \"*.md\" -type f | "+
	//   "grep -i \"baphled\" | head -20</arg_value>\n</tool_call>"
	//
	// Content empty, ToolCalls null, no Done. After the recovery RunStream
	// MUST extract the embedded tool call and emit it as a structured
	// StreamChunk with EventType="tool_call" so the engine tool-loop runs
	// the call as normal.
	Context("inline-XML tool-call recovery (glm-4.5/4.6 reasoning-stream variant)", func() {
		It("emits a structured ToolCall when reasoning_content carries a paired-arg <tool_call> block", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				// Real-shape: zai/glm-4.5 streams reasoning_content with an
				// inline tool_call block, then closes finish_reason="stop"
				// (NOT "tool_calls") because the structured tool_calls array
				// is empty.
				chunks := []string{
					`{"id":"chatcmpl-r1","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"\n<think>\n<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>find /home/baphled/vaults -name \"*.md\" -type f | grep -i \"baphled\" | head -20</arg_value>\n</tool_call>"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r1","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "find baphled notes"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var collected []provider.StreamChunk
			for chunk := range ch {
				collected = append(collected, chunk)
			}

			var toolCallChunks []provider.StreamChunk
			for _, c := range collected {
				if c.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, c)
				}
			}
			Expect(toolCallChunks).To(HaveLen(1),
				"the embedded <tool_call> block MUST be recovered as a structured ToolCall "+
					"chunk so the engine tool-loop runs the call; without recovery the model "+
					"appears to stop with no reply (session 718b5d51 message 9)")

			tc := toolCallChunks[0].ToolCall
			Expect(tc.Name).To(Equal("bash"),
				"the tool name is the first non-attribute token inside the <tool_call> body")
			Expect(tc.Arguments).To(HaveKeyWithValue("command",
				"find /home/baphled/vaults -name \"*.md\" -type f | grep -i \"baphled\" | head -20"),
				"the <arg_key>/<arg_value> pair MUST be parsed into the structured args map verbatim")
			Expect(tc.ID).NotTo(BeEmpty(),
				"a synthetic tool-call id MUST be generated so the engine tool-loop and "+
					"failover correlator can track the call across providers")
			Expect(toolCallChunks[0].EventType).To(Equal("tool_call"),
				"the recovered chunk MUST carry EventType=\"tool_call\" so the engine tool "+
					"loop dispatches it (matches every other tool_call emission path in this file)")
			Expect(toolCallChunks[0].ToolCallID).To(Equal(tc.ID),
				"the chunk-level ToolCallID MUST mirror the inner ToolCall.ID for downstream "+
					"correlation, matching the JustFinishedToolCall path")

			// The original markup MUST be stripped from the Thinking text so
			// the UI does not double-render: the structured ToolCall is the
			// canonical surface, and leaving the raw <tool_call>...</tool_call>
			// in the visible reasoning would replay the symptom in a new shape.
			var thinkingText strings.Builder
			for _, c := range collected {
				if c.Thinking != "" {
					thinkingText.WriteString(c.Thinking)
				}
			}
			Expect(thinkingText.String()).NotTo(ContainSubstring("<tool_call>"),
				"after recovery the markup MUST be stripped from downstream Thinking; "+
					"otherwise the UI double-renders (raw markup + executed tool result)")
			Expect(thinkingText.String()).NotTo(ContainSubstring("</tool_call>"),
				"closing tag must also be stripped")
			Expect(thinkingText.String()).NotTo(ContainSubstring("<arg_key>"),
				"arg markup must also be stripped")
		})

		It("emits multiple structured ToolCalls when reasoning_content carries two <tool_call> blocks", func() {
			// Multi-call sanity: a single reasoning_content payload containing
			// two closed tool_call blocks MUST yield two ToolCall chunks in
			// emission order. Without this, models that batch parallel tool
			// calls in one reasoning emission would still stall after the
			// first.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				chunks := []string{
					`{"id":"chatcmpl-r2","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>ls /tmp</arg_value>\n</tool_call>\nthen\n<tool_call>read\n<arg_key>path</arg_key>\n<arg_value>/etc/hosts</arg_value>\n</tool_call>"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r2","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "two calls"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var toolCallChunks []provider.StreamChunk
			for chunk := range ch {
				if chunk.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, chunk)
				}
			}
			Expect(toolCallChunks).To(HaveLen(2),
				"both inline tool_call blocks MUST be recovered, in order")
			Expect(toolCallChunks[0].ToolCall.Name).To(Equal("bash"))
			Expect(toolCallChunks[0].ToolCall.Arguments).To(HaveKeyWithValue("command", "ls /tmp"))
			Expect(toolCallChunks[1].ToolCall.Name).To(Equal("read"))
			Expect(toolCallChunks[1].ToolCall.Arguments).To(HaveKeyWithValue("path", "/etc/hosts"))
			Expect(toolCallChunks[0].ToolCall.ID).NotTo(Equal(toolCallChunks[1].ToolCall.ID),
				"each recovered call MUST get a distinct synthetic id so downstream "+
					"correlation and result-pairing keep them apart")
		})

		It("recovers a <tool_call> block with multiple <arg_key>/<arg_value> pairs", func() {
			// Multi-pair body: a single tool_call with two arg pairs (the
			// shape observed in session 45d39c27 message 19's would-be call,
			// where the upstream JSON-args mangling we still see today only
			// happens BECAUSE the attribute-form is malformed; the well-
			// formed paired body is the canonical multi-arg shape).
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				chunks := []string{
					`{"id":"chatcmpl-r3","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"<tool_call>delegate\n<arg_key>subagent_type</arg_key>\n<arg_value>explorer</arg_value>\n<arg_key>message</arg_key>\n<arg_value>Search the vault</arg_value>\n</tool_call>"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r3","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "delegate"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var toolCallChunks []provider.StreamChunk
			for chunk := range ch {
				if chunk.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, chunk)
				}
			}
			Expect(toolCallChunks).To(HaveLen(1))
			tc := toolCallChunks[0].ToolCall
			Expect(tc.Name).To(Equal("delegate"))
			Expect(tc.Arguments).To(HaveKeyWithValue("subagent_type", "explorer"))
			Expect(tc.Arguments).To(HaveKeyWithValue("message", "Search the vault"))
		})

		It("does not emit a ToolCall when reasoning_content has no inline-XML markup", func() {
			// Negative: a normal reasoning stream without any <tool_call>
			// markup MUST be unchanged byte-for-byte downstream — no
			// spurious ToolCalls, no Thinking-text mutation. Without this
			// guard a regression in the parser could spuriously fire on
			// substrings that resemble the markup.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				chunks := []string{
					`{"id":"chatcmpl-r4","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Let me think about how to answer this."},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r4","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"reasoning_content":" The user wants the weather."},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r4","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"content":"It is sunny."},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r4","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "weather?"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var collected []provider.StreamChunk
			for chunk := range ch {
				collected = append(collected, chunk)
			}
			var toolCallChunks []provider.StreamChunk
			var thinkingText strings.Builder
			for _, c := range collected {
				if c.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, c)
				}
				if c.Thinking != "" {
					thinkingText.WriteString(c.Thinking)
				}
			}
			Expect(toolCallChunks).To(BeEmpty(),
				"plain reasoning text MUST NOT produce spurious tool calls")
			Expect(thinkingText.String()).To(Equal("Let me think about how to answer this. The user wants the weather."),
				"plain reasoning text MUST flow downstream unchanged byte-for-byte")
		})

		It("preserves an unclosed <tool_call> block verbatim and emits no ToolCall (malformed-soft-error path)", func() {
			// Malformed/unclosed: the existing soft-error affordance still
			// has a job for genuinely broken markup. We MUST NOT half-parse
			// an unclosed tool_call — that would drop a ToolCall chunk the
			// engine will then dispatch with garbage args. Better to leave
			// the markup in Thinking and let the placeholder/affordance
			// surface the failure to the user.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				chunks := []string{
					`{"id":"chatcmpl-r5","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>echo hi"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r5","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "broken"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var collected []provider.StreamChunk
			for chunk := range ch {
				collected = append(collected, chunk)
			}
			var toolCallChunks []provider.StreamChunk
			var thinkingText strings.Builder
			for _, c := range collected {
				if c.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, c)
				}
				if c.Thinking != "" {
					thinkingText.WriteString(c.Thinking)
				}
			}
			Expect(toolCallChunks).To(BeEmpty(),
				"an unclosed <tool_call> MUST NOT yield a structured ToolCall; "+
					"genuinely broken markup stays on the soft-error path")
			Expect(thinkingText.String()).To(ContainSubstring("<tool_call>bash"),
				"unparsed markup MUST be preserved in Thinking so the user/affordance "+
					"can surface the failure")
			Expect(thinkingText.String()).To(ContainSubstring("echo hi"),
				"the partial body MUST also be preserved verbatim")
		})

		It("does not double-emit when a structured tool_calls delta look-alike string sits in reasoning_content", func() {
			// Cross-check: providers that emit BOTH a structured tool_calls
			// (the happy OpenAI shape) AND a reasoning_content with the same
			// substring must not produce two ToolCalls. This guards against
			// a race where the recovery path fires on text that the SDK has
			// already turned into a structured call.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				chunks := []string{
					`{"id":"chatcmpl-r6","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"I will call: <tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>ls</arg_value>\n</tool_call>","tool_calls":[{"id":"call_real","type":"function","function":{"name":"bash","arguments":"{\"command\":\"ls\"}"}}]},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r6","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "ls"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var toolCallChunks []provider.StreamChunk
			for chunk := range ch {
				if chunk.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, chunk)
				}
			}
			// When the SDK already parsed a structured tool call, recovery
			// MUST defer — emitting a second call would drive the engine to
			// double-execute. The structured call is canonical.
			Expect(toolCallChunks).To(HaveLen(1),
				"recovery MUST NOT fire when the structured tool_calls path "+
					"already produced a ToolCall — that would double-execute")
			Expect(toolCallChunks[0].ToolCall.ID).To(Equal("call_real"),
				"the structured call wins; recovery is the fallback only")
		})

		It("assembles a <tool_call> block split across multiple reasoning_content chunks", func() {
			// Stream-boundary safety: the buffer MUST hold a partial
			// tool_call across chunk boundaries and only emit the structured
			// ToolCall once the closing tag arrives. A naive per-chunk
			// scanner would miss the call if the opening and closing tags
			// land in different chunks.
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				chunks := []string{
					`{"id":"chatcmpl-r7","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"<tool_call>bash\n"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r7","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"reasoning_content":"<arg_key>command</arg_key>\n<arg_value>"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r7","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"reasoning_content":"echo hello</arg_value>\n"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r7","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{"reasoning_content":"</tool_call>"},"finish_reason":null}]}`,
					`{"id":"chatcmpl-r7","object":"chat.completion.chunk","model":"glm-4.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				}
				for _, chunk := range chunks {
					fmt.Fprintf(w, "data: %s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "glm-4.5",
				Messages: []provider.Message{{Role: "user", Content: "split"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "zai")
			var toolCallChunks []provider.StreamChunk
			var thinkingText strings.Builder
			for chunk := range ch {
				if chunk.ToolCall != nil {
					toolCallChunks = append(toolCallChunks, chunk)
				}
				if chunk.Thinking != "" {
					thinkingText.WriteString(chunk.Thinking)
				}
			}
			Expect(toolCallChunks).To(HaveLen(1),
				"a <tool_call> block split across chunks MUST be assembled in the buffer "+
					"and emitted exactly once on close-tag")
			Expect(toolCallChunks[0].ToolCall.Name).To(Equal("bash"))
			Expect(toolCallChunks[0].ToolCall.Arguments).To(HaveKeyWithValue("command", "echo hello"))
			Expect(thinkingText.String()).NotTo(ContainSubstring("<tool_call>"),
				"all markup MUST be stripped from Thinking once recovery completes, "+
					"even when the markup arrived across multiple chunks")
		})
	})

	It("propagates server errors", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			resp := map[string]interface{}{
				"error": map[string]interface{}{
					"message": "internal server error",
					"type":    "server_error",
				},
			}
			_ = json.NewEncoder(w).Encode(resp)
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "fail"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var lastChunk provider.StreamChunk
		for chunk := range ch {
			lastChunk = chunk
		}
		Expect(lastChunk.Error).To(HaveOccurred())
		Expect(lastChunk.Done).To(BeTrue())
	})

	It("respects context cancellation", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			for range 10 {
				fmt.Fprintf(w, "data: %s\n\n", `{"id":"chatcmpl-3","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"chunk"},"finish_reason":null}]}`)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "cancel"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var gotCancel bool
		for chunk := range ch {
			if chunk.Error != nil && ctx.Err() != nil {
				gotCancel = true
			}
		}
		Expect(gotCancel).To(BeTrue())
	})

	It("handles empty stream", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "empty"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var chunks []provider.StreamChunk
		for chunk := range ch {
			chunks = append(chunks, chunk)
		}
		Expect(chunks).To(BeEmpty())
	})

	// Streaming spend tracking: per OpenAI's stream_options.include_usage
	// contract, the upstream emits a terminal chunk (empty choices,
	// populated `usage` block) carrying the request-level token totals.
	// The openai-go ChatCompletionAccumulator sums these into acc.Usage,
	// and RunStream must surface them as a `StreamChunk{EventType:"usage"}`
	// carrying a `provider.UsageDelta` BEFORE the terminal `Done` chunk.
	// Without this, every provider that wraps openaicompat (openai,
	// openzen, zai, ollamacloud, github-copilot) has zero streaming
	// spend visibility — the non-stream `ParseChatResponse` path
	// already populates `provider.Usage` correctly, the stream path
	// silently dropped it.
	It("emits a UsageDelta chunk with cumulative tokens before Done", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-usage","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-usage","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				// Terminal usage chunk per OpenAI streaming spec —
				// empty choices, populated `usage` block. Only sent
				// when the request set `stream_options.include_usage`.
				`{"id":"chatcmpl-usage","object":"chat.completion.chunk","model":"gpt-4o","choices":[],"usage":{"prompt_tokens":42,"completion_tokens":17,"total_tokens":59}}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}

		// Locate the usage chunk and the Done chunk by inspection so
		// the assertion does not depend on the exact ordering of any
		// intervening content chunks.
		var usageIdx, doneIdx int
		usageIdx, doneIdx = -1, -1
		for i, c := range collected {
			if c.EventType == "usage" && usageIdx == -1 {
				usageIdx = i
			}
			if c.Done && doneIdx == -1 {
				doneIdx = i
			}
		}

		Expect(usageIdx).To(BeNumerically(">=", 0),
			"RunStream must emit a UsageDelta chunk when the upstream "+
				"includes a terminal usage block (stream_options.include_usage). "+
				"Without it every openaicompat-backed provider has zero "+
				"streaming spend visibility.")
		Expect(doneIdx).To(BeNumerically(">=", 0), "RunStream must emit a Done chunk")
		Expect(usageIdx).To(BeNumerically("<", doneIdx),
			"the usage chunk must precede Done so downstream consumers "+
				"that stop reading on Done still observe the token totals")

		usage := collected[usageIdx].Usage
		Expect(usage).NotTo(BeNil(),
			"the usage chunk must carry a populated *provider.UsageDelta")
		Expect(usage.InputTokens).To(Equal(int64(42)),
			"InputTokens must reflect the upstream's prompt_tokens")
		Expect(usage.OutputTokens).To(Equal(int64(17)),
			"OutputTokens must reflect the upstream's completion_tokens")
	})

	// Sister-spec: streams that finish without a trailing usage block
	// (older mocks, providers that do not honour the include_usage flag)
	// must NOT synthesise a zero-value usage chunk. The contract is
	// "carry tokens when known, stay quiet otherwise" so downstream
	// telemetry does not get poisoned with bogus zeros.
	It("does not emit a UsageDelta chunk when the upstream omits usage data", func() {
		server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-nousage","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi"},"finish_reason":null}]}`,
				`{"id":"chatcmpl-nousage","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		client := openaiAPI.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL))
		ctx := context.Background()
		params := openaicompat.BuildParams(provider.ChatRequest{
			Model:    "gpt-4o",
			Messages: []provider.Message{{Role: "user", Content: "hi"}},
		})
		ch := openaicompat.RunStream(ctx, client, params, "test-provider")
		var collected []provider.StreamChunk
		for chunk := range ch {
			collected = append(collected, chunk)
		}
		for _, c := range collected {
			Expect(c.EventType).NotTo(Equal("usage"),
				"no usage chunk must be emitted when the upstream "+
					"sent no usage data")
		}
	})

	// Error classification specs. These exercise the fix for the silent
	// retry-classification degrade on non-anthropic providers: `stream.Err()`
	// was previously emitted raw as the `Error` field on a `StreamChunk`,
	// so `errors.As(err, &providerErr)` in the engine retry path returned
	// false and the engine fell back to `ErrorTypeUnknown`. Post-fix,
	// RunStream must route the error through `WrapChatError` (plus a
	// fallback for unclassifiable stream-decoder errors) so the downstream
	// chunk carries a `*provider.Error` with a populated `ErrorType`,
	// `HTTPStatus`, and `Provider` field matching the name passed into
	// RunStream.
	//
	// RED NOTE: these specs currently call the pre-fix three-argument
	// RunStream signature and assert classification on the raw error
	// surfaced on chunk.Error. They fail because the current code emits
	// the raw SDK error and errors.As(*provider.Error) returns false.
	// When the fix lands (four-argument RunStream that takes a provider
	// name), the call sites and the Provider-field assertions will be
	// updated in the same commit as the implementation.
	Describe("error classification", func() {
		It("wraps a 429 pre-stream error as *provider.Error with ErrorTypeRateLimit", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"message": "rate limited",
						"type":    "rate_limit",
						"code":    "rate_limit_exceeded",
					},
				})
			}))
			client := openaiAPI.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL),
				option.WithMaxRetries(0),
			)
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "rate me"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "test-provider")

			var lastChunk provider.StreamChunk
			for chunk := range ch {
				lastChunk = chunk
			}
			Expect(lastChunk.Error).To(HaveOccurred())
			Expect(lastChunk.Done).To(BeTrue())

			var provErr *provider.Error
			Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue(),
				"stream error must unwrap to *provider.Error so the engine retry path can classify it")
			Expect(provErr.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
			Expect(provErr.HTTPStatus).To(Equal(http.StatusTooManyRequests))
			Expect(provErr.IsRetriable).To(BeTrue())
			Expect(provErr.Provider).To(Equal("test-provider"))
		})

		It("wraps a 500 pre-stream error as *provider.Error with ErrorTypeServerError", func() {
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"error": map[string]interface{}{
						"message": "internal server error",
						"type":    "server_error",
					},
				})
			}))
			client := openaiAPI.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL),
				option.WithMaxRetries(0),
			)
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "boom"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "test-provider")

			var lastChunk provider.StreamChunk
			for chunk := range ch {
				lastChunk = chunk
			}
			Expect(lastChunk.Error).To(HaveOccurred())

			var provErr *provider.Error
			Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue())
			Expect(provErr.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(provErr.HTTPStatus).To(Equal(http.StatusInternalServerError))
			Expect(provErr.IsRetriable).To(BeTrue())
			Expect(provErr.Provider).To(Equal("test-provider"))
		})

		It("wraps a mid-stream SSE error payload as *provider.Error with a populated ErrorType", func() {
			// openai-go's ssestream decoder turns mid-stream `error` payloads
			// into `fmt.Errorf("received error while streaming: %s", ...)`.
			// These are bare error values — neither *openaiAPI.Error nor
			// *url.Error — so the current RunStream code path surfaces them
			// raw, and engine `errors.As` classification fails. Post-fix,
			// RunStream must still produce a *provider.Error so the engine
			// gets a non-zero ErrorType (even if only ErrorTypeUnknown).
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Cache-Control", "no-cache")
				w.Header().Set("Connection", "keep-alive")
				// First emit a valid content chunk so the SDK enters the
				// streaming loop, then inject an error payload mid-stream.
				fmt.Fprintf(w, "data: %s\n\n",
					`{"id":"chatcmpl-err","object":"chat.completion.chunk","model":"gpt-4o","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				fmt.Fprintf(w, "data: %s\n\n",
					`{"error":{"message":"upstream exploded","type":"server_error"}}`)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}))
			client := openaiAPI.NewClient(
				option.WithAPIKey("test-key"),
				option.WithBaseURL(server.URL),
				option.WithMaxRetries(0),
			)
			ctx := context.Background()
			params := openaicompat.BuildParams(provider.ChatRequest{
				Model:    "gpt-4o",
				Messages: []provider.Message{{Role: "user", Content: "stream error"}},
			})
			ch := openaicompat.RunStream(ctx, client, params, "test-provider")

			var lastChunk provider.StreamChunk
			for chunk := range ch {
				if chunk.Error != nil {
					lastChunk = chunk
				}
			}
			Expect(lastChunk.Error).To(HaveOccurred(),
				"mid-stream SSE error payloads must surface as chunk.Error")

			var provErr *provider.Error
			Expect(errors.As(lastChunk.Error, &provErr)).To(BeTrue(),
				"even bare mid-stream errors must unwrap to *provider.Error so the engine retry path has structured metadata to key on")
			Expect(provErr.ErrorType).NotTo(BeEmpty(),
				"ErrorType must be populated — even ErrorTypeUnknown is better than the empty-string silent-degrade behaviour")
			Expect(provErr.Provider).To(Equal("test-provider"))
		})
	})
})

var _ = Describe("ParseProviderError", func() {
	const testProvider = "test-provider"

	Context("when error is nil", func() {
		It("returns nil", func() {
			Expect(openaicompat.ParseProviderError(testProvider, nil)).To(Succeed())
		})
	})

	Context("when error is an OpenAI SDK error", func() {
		It("classifies 429 as rate limit and retriable", func() {
			err := newOpenAIError(`{"message":"rate limited","type":"rate_limit","code":"rate_limit_exceeded"}`, http.StatusTooManyRequests)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.HTTPStatus).To(Equal(http.StatusTooManyRequests))
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
			Expect(result.IsRetriable).To(BeTrue())
			Expect(result.Provider).To(Equal(testProvider))
			Expect(result.ErrorCode).To(Equal("rate_limit_exceeded"))
			Expect(result.Message).To(Equal("rate limited"))
			Expect(result.RawError).To(Equal(err))
		})

		It("classifies 401 as auth failure and not retriable", func() {
			err := newOpenAIError(`{"message":"invalid key","type":"auth","code":"invalid_api_key"}`, http.StatusUnauthorized)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeAuthFailure))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("classifies 403 as auth failure and not retriable", func() {
			err := newOpenAIError(`{"message":"forbidden","type":"auth","code":"forbidden"}`, http.StatusForbidden)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeAuthFailure))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("classifies 404 as model not found and not retriable", func() {
			err := newOpenAIError(`{"message":"model not found","type":"not_found","code":"model_not_found"}`, http.StatusNotFound)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeModelNotFound))
			Expect(result.IsRetriable).To(BeFalse())
		})

		It("classifies 500 as server error and retriable", func() {
			err := newOpenAIError(`{"message":"internal error","type":"server_error","code":"server_error"}`, http.StatusInternalServerError)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(result.IsRetriable).To(BeTrue())
		})

		It("classifies 503 as server error and retriable", func() {
			err := newOpenAIError(`{"message":"unavailable","type":"server_error","code":"unavailable"}`, http.StatusServiceUnavailable)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(result.IsRetriable).To(BeTrue())
		})

		It("survives fmt.Errorf wrapping", func() {
			inner := newOpenAIError(`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`, http.StatusTooManyRequests)
			wrapped := fmt.Errorf("provider call: %w", inner)
			result := openaicompat.ParseProviderError(testProvider, wrapped)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
		})

		It("classifies unknown status as unknown and not retriable", func() {
			err := newOpenAIError(`{"message":"teapot","type":"unknown","code":"teapot"}`, http.StatusTeapot)
			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeUnknown))
			Expect(result.IsRetriable).To(BeFalse())
		})
	})

	Context("when error is a network error", func() {
		It("classifies url.Error as network error and retriable", func() {
			netErr := &url.Error{Op: "Post", URL: "https://api.openai.com/v1/chat", Err: errors.New("connection refused")}
			result := openaicompat.ParseProviderError(testProvider, netErr)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeNetworkError))
			Expect(result.IsRetriable).To(BeTrue())
			Expect(result.Provider).To(Equal(testProvider))
		})
	})

	Context("when error is unrecognised", func() {
		It("returns nil for a plain error", func() {
			Expect(openaicompat.ParseProviderError(testProvider, errors.New("something"))).To(Succeed())
		})
	})

	// Sibling follow-up to anthropic Phase 3 #3 — OpenAI exposes
	// `retry-after` and `x-ratelimit-*` on 429 errors; Z.AI also
	// returns `retry-after` on 429 / 1001. Capturing them here on
	// every openaicompat-routed provider lets the failover hook
	// honour the carrier-issued back-off instead of the per-error
	// cooldown table. Mirrors the anthropic pattern in
	// internal/provider/anthropic/anthropic_test.go.
	Context("when the error response carries rate-limit headers", func() {
		It("populates RateLimit.RetryAfter from a numeric retry-after", func() {
			headers := http.Header{}
			headers.Set("retry-after", "30")
			err := newOpenAIErrorWithHeaders(
				`{"message":"rate limited","type":"rate_limit","code":"rate_limit_exceeded"}`,
				http.StatusTooManyRequests, headers,
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.RateLimit).NotTo(BeNil(),
				"a 429 with retry-after must carry the parsed metadata so failover can back off precisely")
			Expect(result.RateLimit.RetryAfter).To(Equal(30 * time.Second))
		})

		It("populates RateLimit.RetryAfter from an HTTP-date retry-after", func() {
			// Some openaicompat backends emit retry-after as an
			// HTTP-date instead of an int — the spec permits both.
			future := time.Now().UTC().Add(45 * time.Second).Truncate(time.Second)
			headers := http.Header{}
			headers.Set("retry-after", future.Format(http.TimeFormat))
			err := newOpenAIErrorWithHeaders(
				`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`,
				http.StatusTooManyRequests, headers,
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result.RateLimit).NotTo(BeNil())
			// Allow a small drift; the exact difference depends on
			// when time.Until is evaluated relative to the test
			// clock.
			Expect(result.RateLimit.RetryAfter).To(BeNumerically("~", 45*time.Second, 5*time.Second))
		})

		It("captures the OpenAI x-ratelimit-* requests/tokens triples", func() {
			// OpenAI emits reset values as duration strings ("1s",
			// "10ms"); convert to wall-clock time so the failover
			// hook can compare against time.Now().
			headers := http.Header{}
			headers.Set("x-ratelimit-limit-requests", "1000")
			headers.Set("x-ratelimit-remaining-requests", "0")
			headers.Set("x-ratelimit-reset-requests", "1s")
			headers.Set("x-ratelimit-limit-tokens", "40000")
			headers.Set("x-ratelimit-remaining-tokens", "1500")
			headers.Set("x-ratelimit-reset-tokens", "10ms")
			headers.Set("x-request-id", "req_abc123")
			err := newOpenAIErrorWithHeaders(
				`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`,
				http.StatusTooManyRequests, headers,
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result.RateLimit).NotTo(BeNil())
			rl := result.RateLimit
			Expect(rl.RequestsLimit).To(Equal(1000))
			Expect(rl.RequestsRemaining).To(Equal(0))
			Expect(rl.RequestsReset.IsZero()).To(BeFalse())
			Expect(rl.TokensLimit).To(Equal(40000))
			Expect(rl.TokensRemaining).To(Equal(1500))
			Expect(rl.TokensReset.IsZero()).To(BeFalse())
			Expect(rl.RequestID).To(Equal("req_abc123"))
		})

		It("captures Z.AI-style integer-seconds reset values", func() {
			// Z.AI emits x-ratelimit-reset-* as seconds-until-reset
			// (an integer), not the OpenAI duration-string form.
			// The parser must accept both shapes since openaicompat
			// is shared by zai, openai, and other backends.
			headers := http.Header{}
			headers.Set("x-ratelimit-limit-requests", "60")
			headers.Set("x-ratelimit-remaining-requests", "0")
			headers.Set("x-ratelimit-reset-requests", "60")
			headers.Set("request-id", "zai_req_xyz")
			err := newOpenAIErrorWithHeaders(
				`{"message":"rate limited","type":"rate_limit","code":"1001"}`,
				http.StatusTooManyRequests, headers,
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result.RateLimit).NotTo(BeNil())
			Expect(result.RateLimit.RequestsLimit).To(Equal(60))
			Expect(result.RateLimit.RequestsRemaining).To(Equal(0))
			Expect(result.RateLimit.RequestsReset.IsZero()).To(BeFalse())
			Expect(result.RateLimit.RequestID).To(Equal("zai_req_xyz"))
		})

		It("uses -1 sentinels for windows that were not reported", func() {
			headers := http.Header{}
			headers.Set("retry-after", "5")
			err := newOpenAIErrorWithHeaders(
				`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`,
				http.StatusTooManyRequests, headers,
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result.RateLimit).NotTo(BeNil())
			Expect(result.RateLimit.RequestsRemaining).To(Equal(-1))
			Expect(result.RateLimit.TokensRemaining).To(Equal(-1))
			Expect(result.RateLimit.InputTokensRemaining).To(Equal(-1))
			Expect(result.RateLimit.OutputTokensRemaining).To(Equal(-1))
			Expect(result.RateLimit.RequestsReset.IsZero()).To(BeTrue())
			Expect(result.RateLimit.TokensReset.IsZero()).To(BeTrue())
		})
	})

	Context("when the error response carries no rate-limit headers", func() {
		It("leaves RateLimit nil for a 500 server error", func() {
			err := newOpenAIErrorWithHeaders(
				`{"message":"boom","type":"server_error","code":"server_error"}`,
				http.StatusInternalServerError, http.Header{},
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.ErrorType).To(Equal(provider.ErrorTypeServerError))
			Expect(result.RateLimit).To(BeNil(),
				"absent rate-limit headers must surface as nil so failover falls back to the default cooldown")
		})

		It("leaves RateLimit nil when retry-after is unparseable", func() {
			headers := http.Header{}
			headers.Set("retry-after", "not-a-number")
			err := newOpenAIErrorWithHeaders(
				`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`,
				http.StatusTooManyRequests, headers,
			)

			result := openaicompat.ParseProviderError(testProvider, err)
			Expect(result).To(HaveOccurred())
			Expect(result.RateLimit).To(BeNil(),
				"a malformed header must not crash and must not synthesise a phantom 0s back-off")
		})
	})
})

var _ = Describe("WrapChatError", func() {
	const testProvider = "test-provider"

	It("returns nil for nil error", func() {
		Expect(openaicompat.WrapChatError(testProvider, nil)).To(Succeed())
	})

	It("wraps an OpenAI SDK error as *provider.Error", func() {
		inner := newOpenAIError(`{"message":"rate limited","type":"rate_limit","code":"rate_limit"}`, http.StatusTooManyRequests)
		result := openaicompat.WrapChatError(testProvider, inner)
		Expect(result).To(HaveOccurred())
		var provErr *provider.Error
		Expect(errors.As(result, &provErr)).To(BeTrue())
		Expect(provErr.ErrorType).To(Equal(provider.ErrorTypeRateLimit))
	})

	It("returns the original error when unrecognised", func() {
		plain := errors.New("something unexpected")
		result := openaicompat.WrapChatError(testProvider, plain)
		Expect(result).To(Equal(plain))
	})
})

// Cross-provider failover: when session history contains tool-call IDs
// emitted by Anthropic ("toolu_..."), the OpenAI-compat request builder
// must translate them to call_-prefixed IDs so OpenAI-style providers accept them.
// Bug #1: tool_use_id mismatch after failover.
var _ = Describe("cross-provider failover id translation (OpenAI-compat target)", func() {
	It("rewrites a foreign toolu_-style id to a call_-prefixed id in tool message", func() {
		msgs := []provider.Message{{
			Role:      "tool",
			Content:   "result from previously-anthropic tool call",
			ToolCalls: []provider.ToolCall{{ID: "toolu_01FOREIGN"}},
		}}
		result := openaicompat.BuildMessages(msgs)
		Expect(result).To(HaveLen(1))
		Expect(result[0].OfTool).NotTo(BeNil())
		Expect(result[0].OfTool.ToolCallID).To(HavePrefix("call_"))
		Expect(result[0].OfTool.ToolCallID).NotTo(Equal("toolu_01FOREIGN"))
	})

	It("rewrites a foreign toolu_-style id in assistant tool_calls", func() {
		msgs := []provider.Message{{
			Role:    "assistant",
			Content: "calling",
			ToolCalls: []provider.ToolCall{{
				ID:        "toolu_01FOREIGN",
				Name:      "get_weather",
				Arguments: map[string]any{"city": "London"},
			}},
		}}
		result := openaicompat.BuildMessages(msgs)
		Expect(result).To(HaveLen(1))
		toolCalls := result[0].GetToolCalls()
		Expect(toolCalls).To(HaveLen(1))
		Expect(toolCalls[0].ID).To(HavePrefix("call_"))
		Expect(toolCalls[0].ID).NotTo(Equal("toolu_01FOREIGN"))
	})

	It("preserves pairing: assistant tool_calls id matches subsequent tool message id after translation", func() {
		foreign := "toolu_01ORIGINAL_FROM_ANTHROPIC"
		msgs := []provider.Message{
			{Role: "user", Content: "what is the weather"},
			{Role: "assistant", Content: "", ToolCalls: []provider.ToolCall{{
				ID: foreign, Name: "get_weather", Arguments: map[string]any{"city": "London"},
			}}},
			{Role: "tool", Content: "15c", ToolCalls: []provider.ToolCall{{ID: foreign}}},
		}
		result := openaicompat.BuildMessages(msgs)
		Expect(result).To(HaveLen(3))
		assistantCalls := result[1].GetToolCalls()
		Expect(assistantCalls).To(HaveLen(1))
		Expect(assistantCalls[0].ID).To(HavePrefix("call_"))
		Expect(result[2].OfTool).NotTo(BeNil())
		// Load-bearing contract: ids must match intra-request.
		Expect(result[2].OfTool.ToolCallID).To(Equal(assistantCalls[0].ID))
	})

	It("leaves native call_-prefixed ids unchanged", func() {
		msgs := []provider.Message{{
			Role:      "tool",
			Content:   "ok",
			ToolCalls: []provider.ToolCall{{ID: "call_NATIVE_abc"}},
		}}
		result := openaicompat.BuildMessages(msgs)
		Expect(result).To(HaveLen(1))
		Expect(result[0].OfTool.ToolCallID).To(Equal("call_NATIVE_abc"))
	})
})

// Secondary fix: when a tool message bundles multiple tool calls, BuildMessages
// must emit a ToolMessage per tool-call ID, not silently drop indices >= 1.
var _ = Describe("BuildMessages multi-tool-call tool-role handling", func() {
	It("emits one tool message per tool call id, preserving ordering", func() {
		msgs := []provider.Message{{
			Role:    "tool",
			Content: "shared result payload",
			ToolCalls: []provider.ToolCall{
				{ID: "call_first"},
				{ID: "call_second"},
				{ID: "call_third"},
			},
		}}
		result := openaicompat.BuildMessages(msgs)
		Expect(result).To(HaveLen(3))
		Expect(result[0].OfTool).NotTo(BeNil())
		Expect(result[0].OfTool.ToolCallID).To(Equal("call_first"))
		Expect(result[1].OfTool).NotTo(BeNil())
		Expect(result[1].OfTool.ToolCallID).To(Equal("call_second"))
		Expect(result[2].OfTool).NotTo(BeNil())
		Expect(result[2].OfTool.ToolCallID).To(Equal("call_third"))
	})
})

func unmarshalToolCall(raw string) openaiAPI.ChatCompletionMessageToolCall {
	var tc openaiAPI.ChatCompletionMessageToolCall
	if err := json.Unmarshal([]byte(raw), &tc); err != nil {
		panic("failed to unmarshal tool call: " + err.Error())
	}
	return tc
}

func unmarshalCompletion(raw string) *openaiAPI.ChatCompletion {
	var resp openaiAPI.ChatCompletion
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		panic("failed to unmarshal completion: " + err.Error())
	}
	return &resp
}

func newOpenAIError(body string, statusCode int) *openaiAPI.Error {
	return newOpenAIErrorWithHeaders(body, statusCode, nil)
}

// newOpenAIErrorWithHeaders builds an openai-go SDK error whose
// underlying http.Response carries the given headers. Used by the
// rate-limit capture tests to drive ParseProviderError without standing
// up an httptest server for each case.
func newOpenAIErrorWithHeaders(body string, statusCode int, headers http.Header) *openaiAPI.Error {
	var err openaiAPI.Error
	if uErr := json.Unmarshal([]byte(body), &err); uErr != nil {
		panic("failed to unmarshal openai error: " + uErr.Error())
	}
	err.StatusCode = statusCode
	err.Request = httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/chat/completions", http.NoBody)
	err.Response = &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     headers,
	}
	return &err
}

func newAnthropicError(body string, statusCode int, requestID string) *anthropicAPI.Error {
	var err anthropicAPI.Error
	if uErr := json.Unmarshal([]byte(body), &err); uErr != nil {
		panic("failed to unmarshal anthropic error: " + uErr.Error())
	}
	err.StatusCode = statusCode
	err.RequestID = requestID
	err.Request = httptest.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", http.NoBody)
	err.Response = &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
	return &err
}
