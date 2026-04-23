package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/cli"
	"github.com/baphled/flowstate/internal/coordination"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/spf13/cobra"
)

type recordingGateRunner struct {
	calls []swarm.GateSpec
}

func (r *recordingGateRunner) Run(_ context.Context, gate swarm.GateSpec, _ swarm.GateArgs) error {
	r.calls = append(r.calls, gate)
	return nil
}

func postSwarmGate() swarm.GateSpec {
	return swarm.GateSpec{
		Name: "post-swarm-aggregate",
		Kind: "builtin:result-schema",
		When: swarm.LifecyclePostSwarm,
	}
}

func wireSwarmDelegateTool(testApp *app.App, runner swarm.GateRunner, gates []swarm.GateSpec) {
	testApp.Registry = agent.NewRegistry()
	testApp.Registry.Register(&agent.Manifest{
		ID:           "worker",
		Name:         "Worker",
		Instructions: agent.Instructions{SystemPrompt: "work"},
	})
	swarmReg := swarm.NewRegistry()
	swarmReg.Register(&swarm.Manifest{
		ID:      "test-swarm",
		Lead:    "worker",
		Members: []string{"worker"},
		Harness: swarm.HarnessConfig{Gates: gates},
		Context: swarm.ContextConfig{ChainPrefix: "test"},
	})
	testApp.SwarmRegistry = swarmReg
	dt := engine.NewDelegateToolWithBackground(
		map[string]*engine.Engine{"worker": testApp.Engine},
		agent.Delegation{CanDelegate: true},
		"worker",
		nil,
		coordination.NewMemoryStore(),
	).WithGateRunner(runner)
	testApp.Engine.AddTool(dt)
}

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
	workerManifest := agent.Manifest{
		ID:                "worker",
		Name:              "Worker",
		Instructions:      agent.Instructions{SystemPrompt: "You are a helpful worker."},
		ContextManagement: agent.DefaultContextManagement(),
	}
	registerWorkerInTestRegistry(testApp, workerManifest)
	chatProvider := &runTestProvider{
		name:         "run-test-provider",
		streamChunks: streamChunks,
		streamErr:    streamErr,
	}
	eng := engine.New(engine.Config{
		ChatProvider: chatProvider,
		Manifest:     workerManifest,
	})
	testApp.Engine = eng
	testApp.Streamer = eng
	return testApp
}

// registerWorkerInTestRegistry mirrors the production wiring shape:
// the agent registry MUST hold an entry for whichever id the engine
// is configured against, otherwise resolveAgentOrSwarm rejects the
// name with a NotFoundError before runPrompt ever reaches the
// engine. Pre-existing tests relied on the agent/swarm resolver
// being a no-op when no swarm registry was configured; that
// invariant changed when app.NewForTest started seeding an empty
// swarm.Registry unconditionally.
func registerWorkerInTestRegistry(testApp *app.App, manifest agent.Manifest) {
	if testApp.Registry == nil {
		testApp.Registry = agent.NewRegistry()
	}
	clone := manifest
	testApp.Registry.Register(&clone)
}

// blockingRunProvider emits a preamble chunk so the engine appends
// the user message to the context store, signals that streaming
// started via started, then emits a chunk carrying ctx.Err on
// ctx.Done. Models the long-running planner case where the process
// sits inside a provider Stream call for minutes and a SIGTERM-driven
// signal.NotifyContext cancel propagates down to the provider — the
// provider surfaces the cancellation as a stream-level error, which
// in turn surfaces as a non-nil return from streamResponse. That is
// the exit path the fix must cover: previously, a non-graceful return
// skipped saveSession entirely.
type blockingRunProvider struct {
	name    string
	started chan struct{}
	// preamble is emitted before the provider blocks, so the consumer
	// has visible output and the engine has appended at least one
	// assistant chunk to its context store before the test cancels ctx.
	preamble provider.StreamChunk
}

func (p *blockingRunProvider) Name() string { return p.name }

func (p *blockingRunProvider) Stream(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	chunks := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(chunks)
		select {
		case chunks <- p.preamble:
		case <-ctx.Done():
			return
		}
		// Signal the spec that streaming is live so it can cancel
		// safely knowing the context store has been touched.
		close(p.started)
		<-ctx.Done()
		// Surface the cancellation as a stream-level error so the
		// streaming.Run loop returns a non-nil error. This mirrors
		// the realistic provider behaviour on SIGTERM-driven ctx
		// cancel — the HTTP client round-trip fails with ctx.Err and
		// the provider pipes that back as a chunk-level error. A
		// runPromptCtx control flow that only persists on the nil-err
		// return (the pre-fix shape) must skip saveSession here.
		chunks <- provider.StreamChunk{Error: ctx.Err(), Done: true}
	}()
	return chunks, nil
}

func (p *blockingRunProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *blockingRunProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *blockingRunProvider) Models() ([]provider.Model, error) { return nil, nil }

// createBlockingRunApp returns an app wired with a FileSessionStore at
// the given SessionsDir and an engine whose context store has already
// been installed. Mirrors the production wiring where App.New plumbs
// params.contextStore into engine.Config.Store; for a fresh session the
// store is empty until the first user message is appended during
// streaming.
func createBlockingRunApp(sessionsDir string, provBlocking *blockingRunProvider) *app.App {
	testApp, err := app.NewForTest(app.TestConfig{
		DataDir:     filepath.Dir(sessionsDir),
		SessionsDir: sessionsDir,
	})
	Expect(err).NotTo(HaveOccurred())
	eng := engine.New(engine.Config{
		ChatProvider: provBlocking,
		Manifest: agent.Manifest{
			ID:                "worker",
			Name:              "Worker",
			Instructions:      agent.Instructions{SystemPrompt: "You are a helpful worker."},
			ContextManagement: agent.DefaultContextManagement(),
		},
		Store: recall.NewEmptyContextStore(""),
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

	Describe("post-swarm gate dispatch (T-swarm-3)", func() {
		It("flushes swarm-level post gates after the lead's stream completes", func() {
			testApp := createRunTestApp([]provider.StreamChunk{{Content: "ok", Done: true}}, nil)
			runner := &recordingGateRunner{}
			wireSwarmDelegateTool(testApp, runner, []swarm.GateSpec{postSwarmGate()})

			err := runCmd(testApp, "run", "--prompt", "hello", "--agent", "test-swarm")

			Expect(err).NotTo(HaveOccurred())
			Expect(runner.calls).To(HaveLen(1),
				"post-swarm gate must fire exactly once when the lead's stream completes")
			Expect(runner.calls[0].Name).To(Equal("post-swarm-aggregate"))
			Expect(runner.calls[0].When).To(Equal(swarm.LifecyclePostSwarm))
		})

		It("does not invoke any runner when no swarm context is installed", func() {
			testApp := createRunTestApp([]provider.StreamChunk{{Content: "ok", Done: true}}, nil)
			runner := &recordingGateRunner{}
			dt := engine.NewDelegateToolWithBackground(
				map[string]*engine.Engine{"worker": testApp.Engine},
				agent.Delegation{CanDelegate: true},
				"worker",
				nil,
				coordination.NewMemoryStore(),
			).WithGateRunner(runner)
			testApp.Engine.AddTool(dt)

			err := runCmd(testApp, "run", "--prompt", "hello")

			Expect(err).NotTo(HaveOccurred())
			Expect(runner.calls).To(BeEmpty())
		})

		It("returns the gate failure when the post-swarm runner reports an error", func() {
			testApp := createRunTestApp([]provider.StreamChunk{{Content: "ok", Done: true}}, nil)
			runner := &failingGateRunner{
				err: &swarm.GateError{
					GateName: "post-swarm-aggregate",
					GateKind: "builtin:result-schema",
					When:     swarm.LifecyclePostSwarm,
					Reason:   "aggregate validation failed",
				},
			}
			wireSwarmDelegateTool(testApp, runner, []swarm.GateSpec{postSwarmGate()})

			err := runCmd(testApp, "run", "--prompt", "hello", "--agent", "test-swarm")

			Expect(err).To(HaveOccurred())
			var gateErr *swarm.GateError
			Expect(errors.As(err, &gateErr)).To(BeTrue())
			Expect(gateErr.Reason).To(ContainSubstring("aggregate validation failed"))
		})
	})

	// Regression guard for "Parent Session Lost on Non-Graceful Exit
	// (April 2026)". The live reproduction showed a 41-minute planner
	// run hit the outer `timeout 900` SIGTERM and left only a 136-byte
	// `.meta.json` on disk — the full accumulated conversation
	// (20+ turns, multiple completed delegations) evaporated because
	// the previous runPrompt control flow called saveSession only on
	// the graceful-return path. A SIGTERM that cancelled streaming
	// before streamResponse returned skipped persistence entirely.
	//
	// The fix installs signal.NotifyContext at the CLI entry point and
	// moves saveSession into a defer so every exit path — including a
	// ctx-cancel triggered by SIGTERM / SIGINT — flushes the parent
	// session to disk with whatever messages accumulated up to the
	// cancel point. Spec drives that path in-process by cancelling a
	// plain context.WithCancel while the provider is blocked mid-stream,
	// avoiding the need to send real signals to the Ginkgo runner.
	It("persists the parent session on context cancellation mid-stream", func() {
		sessionsDir := filepath.Join(GinkgoT().TempDir(), "sessions")
		Expect(os.MkdirAll(sessionsDir, 0o750)).To(Succeed())

		prov := &blockingRunProvider{
			name:     "blocking-run-provider",
			started:  make(chan struct{}),
			preamble: provider.StreamChunk{Content: "thinking"},
		}
		testApp := createBlockingRunApp(sessionsDir, prov)

		sessionID := "sigterm-regression-parent"
		opts := &cli.RunOptions{
			Prompt:  "draft a plan and wait",
			Agent:   "worker",
			Session: sessionID,
		}

		cmd := &cobra.Command{}
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- cli.RunPromptCtxForTest(ctx, cmd, testApp, opts)
		}()

		// Wait for the provider to be mid-stream — the user message is
		// now in the engine store, mirroring the production scenario
		// where planner turns have accumulated before SIGTERM arrives.
		select {
		case <-prov.started:
		case <-time.After(5 * time.Second):
			Fail("streaming did not reach the blocking point within 5s")
		}

		cancel()

		select {
		case err := <-done:
			// streamResponse surfaces the ctx-cancel as a stream error;
			// the defer-save must still have fired before that error
			// propagated. The assertion here is on disk state, not the
			// error shape, because the error path is the realistic one
			// after a signal and not the subject of the regression.
			_ = err
		case <-time.After(5 * time.Second):
			Fail("runPromptCtx did not return within 5s of cancel")
		}

		// The load-bearing assertion: the parent session's full JSON
		// file exists on disk. Before the fix, only the 136-byte
		// .meta.json sidecar was present after a non-graceful exit.
		sessionPath := filepath.Join(sessionsDir, sessionID+".json")
		info, statErr := os.Stat(sessionPath)
		Expect(statErr).NotTo(HaveOccurred(),
			"parent session .json must be written when the run is cancelled mid-stream; got stat error %v. "+
				"This is the exact regression captured in 'Parent Session Lost on Non-Graceful Exit'.",
			statErr)
		Expect(info.Size()).To(BeNumerically(">", int64(200)),
			"persisted session must contain the user prompt and metadata, not a bare shell")

		data, readErr := os.ReadFile(sessionPath)
		Expect(readErr).NotTo(HaveOccurred())
		Expect(string(data)).To(ContainSubstring("draft a plan and wait"),
			"persisted session must carry the user prompt that was in flight at the moment of cancellation")
	})
})

type failingGateRunner struct {
	err error
}

func (r *failingGateRunner) Run(_ context.Context, _ swarm.GateSpec, _ swarm.GateArgs) error {
	return r.err
}
