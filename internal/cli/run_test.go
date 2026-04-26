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
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func newRunSwarmRegistry() *swarm.Registry {
	reg := swarm.NewRegistry()
	reg.Register(&swarm.Manifest{
		ID:      "tech-team",
		Lead:    "tech-lead",
		Members: []string{"explorer"},
	})
	return reg
}

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
		for i := range p.streamChunks {
			chunks <- p.streamChunks[i]
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
	eng := engine.New(engine.Config{
		ChatProvider: chatProvider,
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You are a helpful worker."},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	testApp.Engine = eng
	testApp.Streamer = eng
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
		// Per the ADR - Multi-Agent Recall Context Sharing house rule and
		// Session Management architecture, an auto-generated session ID
		// MUST be a UUID v4 so filenames, ChildSessions equality checks,
		// and ctxstore IDKey lookups all agree across CLI and
		// session.Manager. The legacy "session-<UnixNano>" shape was the
		// CLI-only outlier and is superseded.
		parsed, parseErr := uuid.Parse(payload.Session)
		Expect(parseErr).NotTo(HaveOccurred(), "auto-generated session ID must parse as a UUID, got %q", payload.Session)
		Expect(parsed.Version()).To(Equal(uuid.Version(4)), "auto-generated session ID must be UUID v4, got version %d", parsed.Version())
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

	It("generates a UUID v4 session ID when none provided", func() {
		testApp := createRunTestApp([]provider.StreamChunk{
			{Content: "ok", Done: true},
		}, nil)
		err := runCmd(testApp, "run", "--prompt", "hello", "--json")
		Expect(err).NotTo(HaveOccurred())
		var payload struct {
			Session string `json:"session"`
		}
		Expect(json.Unmarshal(out.Bytes(), &payload)).To(Succeed())
		// Pin the canonical format: UUID v4. The old "session-<UnixNano>"
		// shape is the outlier and breaks ChildSessions equality and
		// filename consistency across the recorder, events store, and
		// session.Manager which already issue uuid.New().String().
		parsed, parseErr := uuid.Parse(payload.Session)
		Expect(parseErr).NotTo(HaveOccurred(), "auto-generated session ID must parse as a UUID, got %q", payload.Session)
		Expect(parsed.Version()).To(Equal(uuid.Version(4)), "auto-generated session ID must be UUID v4, got version %d", parsed.Version())
		Expect(payload.Session).NotTo(HavePrefix("session-"), "legacy UnixNano prefix must be gone")
	})

	It("rejects path-traversal session IDs at the CLI gate", func() {
		// H4: --session is user-controllable and flows into
		// filepath.Join calls at L1 and L3 storage write sites.
		// "../../tmp/evil" escapes the configured storage dir; the
		// validator rejects it at the earliest entry point so no
		// downstream code ever builds an unsafe path.
		testApp := createRunTestApp([]provider.StreamChunk{{Content: "ignored", Done: true}}, nil)
		err := runCmd(testApp, "run", "--prompt", "hello", "--session", "../../tmp/evil")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("session"))
	})

	It("rejects absolute-path session IDs at the CLI gate", func() {
		testApp := createRunTestApp([]provider.StreamChunk{{Content: "ignored", Done: true}}, nil)
		err := runCmd(testApp, "run", "--prompt", "hello", "--session", "/tmp/evil")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("session"))
	})

	It("rejects leading-dot session IDs at the CLI gate", func() {
		testApp := createRunTestApp([]provider.StreamChunk{{Content: "ignored", Done: true}}, nil)
		err := runCmd(testApp, "run", "--prompt", "hello", "--session", ".hidden")
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("session"))
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

	// The --session flag accepts arbitrary strings, but the CLI
	// validator rejects path separators and leading dots, and
	// auto-generates an ID when the flag is empty. Without that
	// information in the help text, the operator only discovers the
	// rules by triggering them. Pin the help text to the minimum
	// useful guidance so future wording changes stay operator-facing.
	It("documents auto-generation and validation rules in --session help", func() {
		testApp := createTestApp("", "")
		err := runCmd(testApp, "run", "--help")
		Expect(err).NotTo(HaveOccurred())
		help := out.String()
		Expect(help).To(ContainSubstring("Generated automatically"))
		Expect(help).To(ContainSubstring("path separators"))
		Expect(help).To(ContainSubstring("leading dot"))
	})

	// T-swarm-2 (spec §2): the `--agent` flag shares one resolver
	// path with the chat-input parser. Agent registry first, swarm
	// registry second, error on a both-miss naming the id.
	Describe("--agent T-swarm-2 resolution", func() {
		buildApp := func() *app.App {
			testApp := createRunTestApp([]provider.StreamChunk{{Content: "ok", Done: true}}, nil)
			testApp.Registry = agent.NewRegistry()
			testApp.Registry.Register(&agent.Manifest{
				ID:           "explorer",
				Name:         "Explorer",
				Instructions: agent.Instructions{SystemPrompt: "explore"},
			})
			testApp.SwarmRegistry = newRunSwarmRegistry()
			return testApp
		}

		It("routes --agent <known-agent> to the agent and leaves swarm context unset", func() {
			testApp := buildApp()

			err := runCmd(testApp, "run", "--prompt", "hello", "--agent", "explorer")

			Expect(err).NotTo(HaveOccurred())
			Expect(testApp.Engine.SwarmContext()).To(BeNil(),
				"agent-kind resolution must not install a swarm context")
		})

		It("routes --agent <known-swarm> to the swarm's lead and installs the swarm context", func() {
			testApp := buildApp()

			err := runCmd(testApp, "run", "--prompt", "hello", "--agent", "tech-team", "--json")

			Expect(err).NotTo(HaveOccurred())
			ctx := testApp.Engine.SwarmContext()
			Expect(ctx).NotTo(BeNil(), "swarm-kind resolution must install the SwarmContext")
			Expect(ctx.SwarmID).To(Equal("tech-team"))
			Expect(ctx.LeadAgent).To(Equal("tech-lead"))
			Expect(ctx.Members).To(Equal([]string{"explorer"}))
		})

		It("errors with the canonical \"no agent or swarm named\" message on a both-miss", func() {
			testApp := buildApp()

			err := runCmd(testApp, "run", "--prompt", "hello", "--agent", "ghost")

			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(`no agent or swarm named "ghost"`))
		})
	})
})
