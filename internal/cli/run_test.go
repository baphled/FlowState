package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type runTestProvider struct {
	name         string
	streamChunks []provider.StreamChunk
	streamErr    error
}

func (p *runTestProvider) Name() string {
	return p.name
}
func (p *runTestProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.streamErr != nil {
		return nil, p.streamErr
	}
	chunks := make(chan provider.StreamChunk, len(p.streamChunks))
	go func() {
		defer close(chunks)
		for _, chunk := range p.streamChunks {
			chunks <- chunk
		}
	}()
	return chunks, nil
}
func (p *runTestProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}
func (p *runTestProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}
func (p *runTestProvider) Models() ([]provider.Model, error) {
	return nil, nil
}
func createRunTestApp(streamChunks []provider.StreamChunk, streamErr error) *app.App {
	testApp := createTestApp("", "")
	chatProvider := &runTestProvider{
		name:         "run-test-provider",
		streamChunks: streamChunks,
		streamErr:    streamErr,
	}
	testApp.Engine = engine.New(engine.Config{
		ChatProvider: chatProvider,
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You are a helpful worker."},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	return testApp
}

var _ = Describe("run command", func() {
	var out *bytes.Buffer
	runCmd := func(testApp *app.App, args ...string) error {
		root := cli.NewRootCmd(testApp)
		root.SetOut(out)
		root.SetErr(out)
		root.SetArgs(args)
		return root.Execute()
	}
	BeforeEach(func() {
		out = new(bytes.Buffer)
	})
	It("prints the full response in plain text", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "hello"},
			{Content: " world", Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "say hello")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.String()).To(Equal("hello world\n"))
	})
	It("prints JSON output with agent, prompt, response and session", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "hello"},
			{Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "say hello", "--agent", "builder", "--json")
		Expect(err).NotTo(HaveOccurred())
		var payload struct {
			Agent    string `json:"agent"`
			Prompt   string `json:"prompt"`
			Response string `json:"response"`
			Session  string `json:"session"`
		}
		Expect(json.Unmarshal(out.Bytes(), &payload)).To(Succeed())
		Expect(payload.Response).To(Equal("hello"))
		Expect(payload.Agent).To(Equal("builder"))
		Expect(payload.Prompt).To(Equal("say hello"))
		Expect(payload.Session).To(HavePrefix("session-"))
	})
	It("defaults the agent to worker", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "done", Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "do work", "--json")
		Expect(err).NotTo(HaveOccurred())
		var payload struct {
			Agent string `json:"agent"`
		}
		Expect(json.Unmarshal(out.Bytes(), &payload)).To(Succeed())
		Expect(payload.Agent).To(Equal("worker"))
	})
	It("returns an error when the prompt is missing", func() {
		testApp := createRunTestApp([]provider.StreamChunk{{Content: "ignored", Done: true}}, nil)
		err := runCmd(testApp, "run")
		Expect(err).To(MatchError("prompt is required"))
	})
	It("returns an error when streaming fails", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "partial"},
			{Error: errors.New("boom"), Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "explode")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("stream error: boom"))
	})
	It("returns an error when the engine is not configured", func() {
		testApp := createTestApp("", "")
		err := runCmd(testApp, "run", "--prompt", "hello")
		Expect(err).To(MatchError("engine not configured"))
	})
	It("returns an error when the provider fails before streaming starts", func() {
		testApp := createRunTestApp(nil, errors.New("provider unavailable"))
		err := runCmd(testApp, "run", "--prompt", "hello")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("streaming response: provider unavailable"))
	})

	It("includes session and prompt in JSON output", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "response", Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "my prompt", "--session", "test-session", "--json")
		Expect(err).NotTo(HaveOccurred())
		var payload struct {
			Session  string `json:"session"`
			Prompt   string `json:"prompt"`
			Response string `json:"response"`
		}
		Expect(json.Unmarshal(out.Bytes(), &payload)).To(Succeed())
		Expect(payload.Session).To(Equal("test-session"))
		Expect(payload.Prompt).To(Equal("my prompt"))
		Expect(payload.Response).To(Equal("response"))
	})

	It("generates a session ID when none provided", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "ok", Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "hello", "--json")
		Expect(err).NotTo(HaveOccurred())
		var payload struct {
			Session string `json:"session"`
		}
		Expect(json.Unmarshal(out.Bytes(), &payload)).To(Succeed())
		Expect(payload.Session).To(HavePrefix("session-"))
	})

	It("shows session flag in help", func() {
		testApp := createTestApp("", "")
		err := runCmd(testApp, "run", "--help")
		Expect(err).NotTo(HaveOccurred())
		Expect(out.String()).To(ContainSubstring("--session"))
		Expect(out.String()).To(ContainSubstring("--prompt"))
		Expect(out.String()).To(ContainSubstring("--agent"))
		Expect(out.String()).To(ContainSubstring("--json"))
	})
})
