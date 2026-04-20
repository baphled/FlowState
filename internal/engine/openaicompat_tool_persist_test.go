package engine_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openaiAPI "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/openaicompat"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

// openaiCompatProvider is a thin Provider implementation that drives the real
// openaicompat.RunStream path against an httptest.Server. It lets the engine
// exercise the same accumulator and flush logic used by zai, github-copilot,
// and openai in production.
type openaiCompatProvider struct {
	name   string
	client openaiAPI.Client
}

func (p *openaiCompatProvider) Name() string { return p.name }

func (p *openaiCompatProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	params := openaicompat.BuildParams(req)
	return openaicompat.RunStream(ctx, p.client, params, p.name), nil
}

func (p *openaiCompatProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *openaiCompatProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.1, 0.2, 0.3}, nil
}

func (p *openaiCompatProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

var _ = Describe("Engine persists openaicompat tool_use intent", func() {
	// This spec pins the bug observed in session-1776623141279480382 where a
	// planner session left the persisted assistant message with a raw
	// function-call JSON string in Content and ToolCalls=nil. The contract
	// under test: when the real openaicompat accumulator surfaces a tool_call
	// event, the engine MUST persist a provider.Message whose ToolCalls slice
	// carries the tool-use intent — regardless of whether the tool is wired
	// into the engine. The alternative (silent loss of the tool-use record
	// when the tool cannot be executed) hides the model's intent from the
	// session transcript and the rehydration pipeline alike.
	//
	// The fake OpenAI-compat server streams a valid function_call-shaped SSE
	// sequence. The tool it asks for is intentionally NOT registered in the
	// engine, mirroring the repro where the planner session's tool_call was
	// never dispatched and the persisted message lost all trace of it.

	It("persists the tool_call on provider.Message.ToolCalls even when the tool is not registered", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			chunks := []string{
				`{"id":"chatcmpl-repro","object":"chat.completion.chunk","model":"glm-4.7","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_repro","type":"function","function":{"name":"plan_reviewer","arguments":"{\"all\":false}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-repro","object":"chat.completion.chunk","model":"glm-4.7","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		DeferCleanup(func() { server.Close() })

		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		prov := &openaiCompatProvider{name: "fake-openaicompat", client: client}

		tmpDir, err := os.MkdirTemp("", "engine-openaicompat-toolpersist")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })

		storePath := filepath.Join(tmpDir, "context.json")
		store, err := recall.NewFileContextStore(storePath, "")
		Expect(err).NotTo(HaveOccurred())

		manifest := agent.Manifest{
			ID:   "planner",
			Name: "Planner",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a planner.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		// Deliberately: no tool registered for "plan_reviewer". The engine
		// will treat execute as ErrToolNotFound, which is exactly the shape
		// that left the repro session with a bare assistant message.
		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetContextStore(store, "test-session")

		ctx := context.Background()
		chunks, streamErr := eng.Stream(ctx, "planner", "List plans")
		Expect(streamErr).NotTo(HaveOccurred())
		for chunk := range chunks {
			_ = chunk
		}

		msgs := store.AllMessages()

		var toolUseMsg *provider.Message
		for i := range msgs {
			m := msgs[i]
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				toolUseMsg = &msgs[i]
				break
			}
		}

		Expect(toolUseMsg).NotTo(BeNil(),
			"expected the engine to persist an assistant message with ToolCalls populated "+
				"when the openaicompat accumulator surfaced a tool_call event; saw messages: %+v",
			msgs)
		Expect(toolUseMsg.ToolCalls[0].Name).To(Equal("plan_reviewer"),
			"persisted tool_call must carry the function name from the stream")
		Expect(toolUseMsg.ToolCalls[0].ID).To(Equal("call_repro"),
			"persisted tool_call must carry the provider-issued id from the stream")

		// Guard against regression to the repro shape where the raw
		// function-call JSON leaked into Content.
		for _, m := range msgs {
			if m.Role == "assistant" {
				Expect(m.Content).NotTo(ContainSubstring(`"name":`),
					"assistant Content must not carry the raw function-call JSON; "+
						"the tool_use intent belongs on ToolCalls, not in free-text")
				Expect(m.Content).NotTo(ContainSubstring(`"plan_reviewer"`),
					"assistant Content must not leak the tool name as JSON text")
			}
		}
	})
})

// These specs pin the canonical artefact ordering at the engine's public
// Stream seam when the upstream provider is an openaicompat-shaped stream
// whose first chunk is a tool_call with no preceding content or thinking.
// The vault invariant (Chat TUI Message Rendering Order Fix, Session
// Rendering Consistency, ADR - Swarm Activity Event Model) requires that
// the consumer observe at least one content or thinking artefact for a
// turn before the first tool_use chunk is surfaced. A bare tool_use as
// the first consumer-observed artefact of a turn is the bug.
//
// We exercise the real openaicompat accumulator via an httptest server so
// this behaviour is pinned at the same seam that zai, github-copilot, and
// openai providers share in production. No TUI code is involved; the
// assertion is on the channel returned by engine.Stream.
var _ = Describe("Engine enforces text-before-tool_use ordering on openaicompat streams", func() {
	It("must not surface a tool_use before any content or thinking when the openaicompat stream opens with a bare function call", func() {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("Connection", "keep-alive")
			// The very first delta carries a tool_call with no prior text
			// or reasoning delta. This is the canonical repro shape for
			// the reported bug: agents emit tool calls as the first
			// artefact of a turn with nothing preceding them.
			chunks := []string{
				`{"id":"chatcmpl-order","object":"chat.completion.chunk","model":"glm-4.7","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_ordering","type":"function","function":{"name":"plan_reviewer","arguments":"{\"all\":false}"}}]},"finish_reason":null}]}`,
				`{"id":"chatcmpl-order","object":"chat.completion.chunk","model":"glm-4.7","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
			}
			for _, chunk := range chunks {
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
		}))
		DeferCleanup(func() { server.Close() })

		client := openaiAPI.NewClient(
			option.WithAPIKey("test-key"),
			option.WithBaseURL(server.URL),
		)
		prov := &openaiCompatProvider{name: "fake-openaicompat-ordering", client: client}

		tmpDir, err := os.MkdirTemp("", "engine-openaicompat-ordering")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(tmpDir) })

		storePath := filepath.Join(tmpDir, "context.json")
		store, err := recall.NewFileContextStore(storePath, "")
		Expect(err).NotTo(HaveOccurred())

		manifest := agent.Manifest{
			ID:   "planner",
			Name: "Planner",
			Instructions: agent.Instructions{
				SystemPrompt: "You are a planner.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		eng := engine.New(engine.Config{
			ChatProvider: prov,
			Manifest:     manifest,
			Tools:        []tool.Tool{},
		})
		eng.SetContextStore(store, "test-session")

		ctx := context.Background()
		streamChunks, streamErr := eng.Stream(ctx, "planner", "List plans")
		Expect(streamErr).NotTo(HaveOccurred())

		var received []provider.StreamChunk
		for chunk := range streamChunks {
			received = append(received, chunk)
		}
		Expect(received).NotTo(BeEmpty(),
			"the engine must produce at least one consumer-observable chunk per turn")

		firstToolUseIdx := -1
		firstTextOrThinkingIdx := -1
		for i, chunk := range received {
			if firstToolUseIdx == -1 && chunk.ToolCall != nil {
				firstToolUseIdx = i
			}
			if firstTextOrThinkingIdx == -1 && (chunk.Content != "" || chunk.Thinking != "") {
				firstTextOrThinkingIdx = i
			}
		}

		Expect(firstToolUseIdx).NotTo(Equal(-1),
			"the openaicompat stream carried a function call; the engine must eventually "+
				"surface it as a tool_use chunk once the ordering gate has released it")
		Expect(firstTextOrThinkingIdx).NotTo(Equal(-1),
			"the consumer must observe at least one text or thinking artefact for the "+
				"turn before the first tool_use; the engine must not let an openaicompat "+
				"stream violate the canonical thinking/text -> tool_use ordering")
		Expect(firstTextOrThinkingIdx).To(BeNumerically("<", firstToolUseIdx),
			"tool_use must not be the first consumer-observed artefact of a turn; "+
				"saw tool_use at index %d with no preceding content or thinking "+
				"(received=%+v)", firstToolUseIdx, received)
	})
})
