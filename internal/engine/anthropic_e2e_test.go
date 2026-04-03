package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/provider/anthropic"
	"github.com/baphled/flowstate/internal/session"
)

const (
	anthropicE2EModel      = "claude-sonnet-4-20250514"
	anthropicE2ESystemText = "You are a helpful assistant. Respond directly to the user."
	goldenFileName         = "anthropic_hello_response.golden.json"
)

type goldenChunk struct {
	Content        string                   `json:"content,omitempty"`
	Done           bool                     `json:"done,omitempty"`
	ErrorMessage   string                   `json:"error,omitempty"`
	EventType      string                   `json:"event_type,omitempty"`
	ToolCall       *provider.ToolCall       `json:"tool_call,omitempty"`
	ToolResult     *provider.ToolResultInfo `json:"tool_result,omitempty"`
	DelegationInfo *provider.DelegationInfo `json:"delegation_info,omitempty"`
}

type goldenRecording struct {
	Chunks []goldenChunk `json:"chunks"`
}

var (
	anthropicE2EOnce       sync.Once
	anthropicE2ECached     []provider.StreamChunk
	anthropicE2ESkipped    bool
	anthropicE2EGoldenPath string
)

func convertGoldenChunks(chunks []goldenChunk) []provider.StreamChunk {
	result := make([]provider.StreamChunk, len(chunks))
	for i, gc := range chunks {
		var err error
		if gc.ErrorMessage != "" {
			err = errors.New(gc.ErrorMessage)
		}
		result[i] = provider.StreamChunk{
			Content:        gc.Content,
			Done:           gc.Done,
			Error:          err,
			EventType:      gc.EventType,
			ToolCall:       gc.ToolCall,
			ToolResult:     gc.ToolResult,
			DelegationInfo: gc.DelegationInfo,
		}
	}
	return result
}

func ensureAnthropicE2EData() {
	anthropicE2EOnce.Do(func() {
		anthropicE2EGoldenPath = filepath.Join("testdata", goldenFileName)

		if data, err := os.ReadFile(anthropicE2EGoldenPath); err == nil {
			var recording goldenRecording
			if jsonErr := json.Unmarshal(data, &recording); jsonErr == nil {
				anthropicE2ECached = convertGoldenChunks(recording.Chunks)
				return
			}
		}

		realProvider := createRealAnthropicProvider()
		if realProvider == nil {
			anthropicE2ESkipped = true
			return
		}

		req := provider.ChatRequest{
			Model: anthropicE2EModel,
			Messages: []provider.Message{
				{Role: "system", Content: anthropicE2ESystemText},
				{Role: "user", Content: "hello"},
			},
		}
		ch, err := realProvider.Stream(context.Background(), req)
		if err != nil {
			anthropicE2ESkipped = true
			return
		}

		var recorded []goldenChunk
		for chunk := range ch {
			anthropicE2ECached = append(anthropicE2ECached, chunk)
			recorded = append(recorded, goldenChunk{
				Content:        chunk.Content,
				Done:           chunk.Done,
				ErrorMessage:   errorString(chunk.Error),
				EventType:      chunk.EventType,
				ToolCall:       chunk.ToolCall,
				ToolResult:     chunk.ToolResult,
				DelegationInfo: chunk.DelegationInfo,
			})
		}

		saveGoldenFile(anthropicE2EGoldenPath, recorded)
	})
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func saveGoldenFile(goldenPath string, chunks []goldenChunk) {
	recording := goldenRecording{Chunks: chunks}
	data, err := json.MarshalIndent(recording, "", "  ")
	if err != nil {
		return
	}

	dir := filepath.Dir(goldenPath)
	if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
		return
	}
	_ = os.WriteFile(goldenPath, data, 0o600)
}

type replayProvider struct {
	chunks []provider.StreamChunk
}

func (r *replayProvider) Name() string { return "replay-anthropic" }

func (r *replayProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(r.chunks))
	for i := range r.chunks {
		ch <- r.chunks[i]
	}
	close(ch)
	return ch, nil
}

func (r *replayProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (r *replayProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (r *replayProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

func createRealAnthropicProvider() provider.Provider {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey != "" {
		p, err := anthropic.New(apiKey)
		if err != nil {
			return nil
		}
		return p
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	opencodePath := filepath.Join(homeDir, ".local", "share", "opencode", "auth.json")
	p, err := anthropic.NewFromOpenCodeOrConfig(opencodePath, "")
	if err != nil {
		return nil
	}
	return p
}

func newE2EManifest() agent.Manifest {
	return agent.Manifest{
		ID:         "test-executor",
		Name:       "Test Executor",
		Complexity: "standard",
		Capabilities: agent.Capabilities{
			Tools: []string{"bash", "file", "web"},
		},
		Instructions: agent.Instructions{
			SystemPrompt: anthropicE2ESystemText,
		},
		ContextManagement: agent.DefaultContextManagement(),
	}
}

var _ = Describe("Anthropic session end-to-end", Label("e2e"), func() {
	BeforeEach(func() {
		ensureAnthropicE2EData()
		if anthropicE2ESkipped {
			Skip("No Anthropic API key and no golden file — skipping e2e test")
		}
	})

	Describe("hello message flow", func() {
		Context("when the user sends 'hello'", func() {
			It("returns a helpful response without tool calls", func() {
				replay := &replayProvider{chunks: anthropicE2ECached}
				eng := engine.New(engine.Config{
					ChatProvider: replay,
					Manifest:     newE2EManifest(),
				})
				mgr := session.NewManager(eng)

				sess, err := mgr.CreateSession("test-executor")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.CoordinationStore).NotTo(BeNil())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())

				content, chunks := drainStreamContent(ch)

				Expect(len(content)).To(BeNumerically(">=", 5))

				for _, chunk := range chunks {
					Expect(chunk.EventType).NotTo(Equal("tool_call"))
					Expect(chunk.ToolCall).To(BeNil())
				}

				for _, chunk := range chunks {
					Expect(chunk.Error).ToNot(HaveOccurred())
				}

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Messages).NotTo(BeEmpty())
				Expect(sess.Messages[0].Role).To(Equal("user"))
				Expect(sess.Messages[0].Content).To(Equal("hello"))
			})

			It("coordination store is functional but does not interfere", func() {
				replay := &replayProvider{chunks: anthropicE2ECached}
				eng := engine.New(engine.Config{
					ChatProvider: replay,
					Manifest:     newE2EManifest(),
				})
				mgr := session.NewManager(eng)

				sess, err := mgr.CreateSession("test-executor")
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.CoordinationStore).NotTo(BeNil())

				Expect(sess.CoordinationStore.Set("test-key", []byte("test-value"))).To(Succeed())

				val, getErr := sess.CoordinationStore.Get("test-key")
				Expect(getErr).NotTo(HaveOccurred())
				Expect(string(val)).To(Equal("test-value"))

				keys, listErr := sess.CoordinationStore.List("")
				Expect(listErr).NotTo(HaveOccurred())
				Expect(keys).To(ContainElement("test-key"))
			})
		})
	})
})
