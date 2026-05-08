package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/plugin/eventbus"
	"github.com/baphled/flowstate/internal/plugin/events"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	todo "github.com/baphled/flowstate/internal/tool/todo"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type mockStreamer struct {
	chunks          []provider.StreamChunk
	err             error
	mu              sync.Mutex
	capturedAgentID string
	capturedMessage string
}

func (m *mockStreamer) Stream(_ context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	// Capture under mu so the sibling-race specs (which fan out
	// concurrent POSTs through this same fixture) do not race on the
	// bookkeeping fields. The non-concurrent specs continue to read
	// CapturedAgentID/CapturedMessage via the accessors below.
	m.mu.Lock()
	m.capturedAgentID = agentID
	m.capturedMessage = message
	m.mu.Unlock()
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan provider.StreamChunk, len(m.chunks))
	for i := range m.chunks {
		ch <- m.chunks[i]
	}
	close(ch)
	return ch, nil
}

// fakeDispatchEngine satisfies swarm.DispatchEngine for the API parity
// tests. Records every SetSwarmContext + FlushSwarmLifecycle call so
// the test can assert the swarm dispatch path actually went through
// the engine — not just the streamer. Also captures the snapshot/
// restore round-trip introduced for CLI/TUI symmetric manifest
// reverts: snapshotCalls and restoreCalls let the test assert the
// engine's persistent state is reverted after each dispatch.
type fakeDispatchEngine struct {
	installedContext *swarm.Context
	flushCalls       int
	snapshotCalls    int
	restoreCalls     int
	lastRestored     any
	// Lifecycle recorders — the API agent / model PATCH handlers fan
	// out via Orchestrator.SwitchAgent / SwitchModel post-lift, which
	// expect the wider Engine surface (SetManifest +
	// SetModelPreference + SetContextStore). Tests assert on these
	// counters to pin the parity-fan-out contract.
	manifestSet         []agent.Manifest
	modelPrefProviders  []string
	modelPrefModels     []string
	contextStoreCalls   int
}

func (f *fakeDispatchEngine) SetSwarmContext(ctx *swarm.Context) {
	f.installedContext = ctx
}

func (f *fakeDispatchEngine) FlushSwarmLifecycle(_ context.Context) error {
	f.flushCalls++
	return nil
}

// fakeManifestToken is the opaque value the fake hands back through
// ManifestSnapshot / RestoreManifest so the test can pin the
// round-trip without depending on the agent.Manifest type.
type fakeManifestToken struct{ id string }

func (f *fakeDispatchEngine) ManifestSnapshot() any {
	f.snapshotCalls++
	return fakeManifestToken{id: "pre-dispatch"}
}

func (f *fakeDispatchEngine) RestoreManifest(snapshot any) {
	f.restoreCalls++
	f.lastRestored = snapshot
}

func (f *fakeDispatchEngine) SkipAgentFiles() bool     { return false }
func (f *fakeDispatchEngine) SetSkipAgentFiles(_ bool) {}

// SetManifest, SetModelPreference, SetContextStore satisfy the wider
// orchestrator.Engine interface. Without them the fake only satisfies
// swarm.DispatchEngine and orchestrator.New's auto-narrow leaves the
// lifecycle-half engine field nil — agent / model PATCH parity tests
// would silently no-op the engine half.
func (f *fakeDispatchEngine) SetManifest(m agent.Manifest) {
	f.manifestSet = append(f.manifestSet, m)
}

func (f *fakeDispatchEngine) SetModelPreference(providerName, modelName string) {
	f.modelPrefProviders = append(f.modelPrefProviders, providerName)
	f.modelPrefModels = append(f.modelPrefModels, modelName)
}

func (f *fakeDispatchEngine) SetContextStore(_ *recall.FileContextStore, _ string) {
	f.contextStoreCalls++
}

var _ = Describe("Server", func() {
	var (
		server          *api.Server
		recorder        *httptest.ResponseRecorder
		registry        *agent.Registry
		streamer        *mockStreamer
		disc            *discovery.AgentDiscovery
		skills          []skill.Skill
		testManifest    agent.Manifest
		anotherManifest agent.Manifest
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()

		testManifest = agent.Manifest{
			ID:   "test-agent",
			Name: "Test Agent",
			Metadata: agent.Metadata{
				Role:      "Testing agent",
				Goal:      "Help with testing",
				WhenToUse: "When you need to test something",
			},
			Instructions: agent.Instructions{
				SystemPrompt: "You are a test agent.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		anotherManifest = agent.Manifest{
			ID:   "another-agent",
			Name: "Another Agent",
			Metadata: agent.Metadata{
				Role:      "Another role",
				Goal:      "Another goal",
				WhenToUse: "When you need another agent",
			},
			Instructions: agent.Instructions{
				SystemPrompt: "You are another agent.",
			},
			ContextManagement: agent.DefaultContextManagement(),
		}

		registry = agent.NewRegistry()

		streamer = &mockStreamer{
			chunks: []provider.StreamChunk{
				{Content: "Hello"},
				{Content: " there!"},
				{Content: "", Done: true},
			},
		}

		disc = discovery.NewAgentDiscovery([]agent.Manifest{testManifest, anotherManifest})

		skills = []skill.Skill{
			{Name: "skill-one", Description: "First skill"},
			{Name: "skill-two", Description: "Second skill"},
		}

		server = api.NewServer(streamer, registry, disc, skills)
	})

	Describe("GET /api/agents", func() {
		Context("when registry is empty", func() {
			It("returns an empty JSON array", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/agents", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

				var agents []agent.Manifest
				err := json.Unmarshal(recorder.Body.Bytes(), &agents)
				Expect(err).NotTo(HaveOccurred())
				Expect(agents).To(BeEmpty())
			})
		})

		Context("when registry has agents", func() {
			BeforeEach(func() {
				registry.Register(&testManifest)
				registry.Register(&anotherManifest)
			})

			It("returns JSON array of agent manifests", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/agents", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

				var agents []agent.Manifest
				err := json.Unmarshal(recorder.Body.Bytes(), &agents)
				Expect(err).NotTo(HaveOccurred())
				Expect(agents).To(HaveLen(2))
			})
		})
	})

	Describe("GET /api/agents/{id}", func() {
		Context("when agent exists", func() {
			BeforeEach(func() {
				registry.Register(&testManifest)
			})

			It("returns the specific agent manifest", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

				var manifest agent.Manifest
				err := json.Unmarshal(recorder.Body.Bytes(), &manifest)
				Expect(err).NotTo(HaveOccurred())
				Expect(manifest.ID).To(Equal("test-agent"))
				Expect(manifest.Name).To(Equal("Test Agent"))
			})
		})

		Context("when agent does not exist", func() {
			It("returns 404 Not Found", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/agents/unknown-agent", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusNotFound))
			})
		})

		Context("when agent declares model preferences", func() {
			BeforeEach(func() {
				strictManifest := agent.Manifest{
					ID:          "junior-strict",
					Name:        "Junior Strict",
					ModelPolicy: agent.ModelPolicyStrict,
					PreferredModels: []agent.ModelPreference{
						{Provider: "anthropic", Model: "claude-haiku-4"},
						{Provider: "anthropic", Model: "claude-sonnet-4"},
					},
					ContextManagement: agent.DefaultContextManagement(),
				}
				registry.Register(&strictManifest)
			})

			It("serialises preferred_models and model_policy on the wire", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/agents/junior-strict", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))

				// Decode into a generic map to pin the precise JSON shape
				// the web client consumes — field names, casing, order
				// inside each entry.
				var payload map[string]any
				Expect(json.Unmarshal(recorder.Body.Bytes(), &payload)).To(Succeed())

				Expect(payload).To(HaveKey("model_policy"))
				Expect(payload["model_policy"]).To(Equal("strict"))

				Expect(payload).To(HaveKey("preferred_models"))
				prefs, ok := payload["preferred_models"].([]any)
				Expect(ok).To(BeTrue(), "preferred_models must be a JSON array")
				Expect(prefs).To(HaveLen(2))

				first, ok := prefs[0].(map[string]any)
				Expect(ok).To(BeTrue())
				Expect(first).To(HaveKeyWithValue("provider", "anthropic"))
				Expect(first).To(HaveKeyWithValue("model", "claude-haiku-4"))
			})
		})

		Context("when agent omits model preferences", func() {
			BeforeEach(func() {
				registry.Register(&testManifest)
			})

			It("omits the optional fields from JSON output", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/agents/test-agent", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))

				var payload map[string]any
				Expect(json.Unmarshal(recorder.Body.Bytes(), &payload)).To(Succeed())

				// omitempty contract — when no preferences are declared
				// the keys must not appear, so the web client's
				// `agent.preferred_models ?? []` fallback is exercised.
				Expect(payload).NotTo(HaveKey("preferred_models"))
				Expect(payload).NotTo(HaveKey("model_policy"))
			})
		})
	})

	Describe("GET /api/discover", func() {
		It("returns agent suggestions as JSON array", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/discover?message=test", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

			var suggestions []discovery.AgentSuggestion
			err := json.Unmarshal(recorder.Body.Bytes(), &suggestions)
			Expect(err).NotTo(HaveOccurred())
		})

		Context("when message matches agent", func() {
			It("returns matching suggestions", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/discover?message=testing", http.NoBody)
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))

				var suggestions []discovery.AgentSuggestion
				err := json.Unmarshal(recorder.Body.Bytes(), &suggestions)
				Expect(err).NotTo(HaveOccurred())
				Expect(suggestions).NotTo(BeEmpty())
			})
		})
	})

	Describe("GET /api/skills", func() {
		It("returns skills as JSON array", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/skills", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

			var returnedSkills []skill.Skill
			err := json.Unmarshal(recorder.Body.Bytes(), &returnedSkills)
			Expect(err).NotTo(HaveOccurred())
			Expect(returnedSkills).To(HaveLen(2))
			Expect(returnedSkills[0].Name).To(Equal("skill-one"))
			Expect(returnedSkills[1].Name).To(Equal("skill-two"))
		})
	})

	Describe("GET /api/sessions", func() {
		It("returns placeholder JSON array", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/sessions", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

			var sessions []interface{}
			err := json.Unmarshal(recorder.Body.Bytes(), &sessions)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("POST /api/chat", func() {
		It("returns SSE stream with content chunks", func() {
			body := `{"agent_id":"test-agent","message":"Hello"}`
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("text/event-stream"))
			Expect(recorder.Header().Get("Cache-Control")).To(Equal("no-cache"))
			Expect(recorder.Header().Get("Connection")).To(Equal("keep-alive"))

			events := parseSSEEvents(recorder.Body)
			Expect(events).NotTo(BeEmpty())

			hasContent := false
			hasDone := false
			for _, event := range events {
				if strings.Contains(event, `"content"`) {
					hasContent = true
				}
				if event == "[DONE]" {
					hasDone = true
				}
			}
			Expect(hasContent).To(BeTrue())
			Expect(hasDone).To(BeTrue())
		})

		Context("when request body is invalid", func() {
			It("returns 400 Bad Request", func() {
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader("invalid json"))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusBadRequest))
			})
		})

		Context("when streamer returns an error", func() {
			BeforeEach(func() {
				streamer.err = errors.New("stream failed")
			})

			It("writes SSE error and DONE", func() {
				body := `{"agent_id":"test-agent","message":"Hello"}`
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				events := parseSSEEvents(recorder.Body)
				// Raw error text must not reach the client; only the canonical
				// category message and a correlation ID are forwarded.
				Expect(events).To(ContainElement(ContainSubstring(`"error":"stream error"`)))
				Expect(events).To(ContainElement(ContainSubstring(`"correlation_id"`)))
				Expect(events).NotTo(ContainElement(ContainSubstring("stream failed")))
				Expect(events).To(ContainElement("[DONE]"))
			})
		})

		// API/CLI/TUI parity per ADR - Swarm Dispatch Across Access Methods.
		// The web surface used to call streaming.Run directly with whatever
		// the client sent as agent_id, so a swarm id arrived as a phantom
		// agent and the engine streamed nothing useful. With WithSwarmRegistry
		// + WithDispatchEngine wired, the handler resolves the id through
		// swarm.ResolveTarget and dispatches via swarm.DispatchSwarm — same
		// shape as cli/run.go's flow.
		Context("when agent_id resolves to a swarm", func() {
			var (
				swarmReg *swarm.Registry
				engStub  *fakeDispatchEngine
			)

			BeforeEach(func() {
				registry.Register(&agent.Manifest{ID: "test-lead", Name: "Test Lead"})
				swarmReg = swarm.NewRegistry()
				swarmReg.Register(&swarm.Manifest{
					SchemaVersion: "1.0.0",
					ID:            "test-swarm",
					Lead:          "test-lead",
					Members:       []string{},
				})
				engStub = &fakeDispatchEngine{}
				server = api.NewServer(streamer, registry, disc, skills,
					api.WithSwarmRegistry(swarmReg),
					api.WithDispatchEngine(engStub),
				)
			})

			It("streams from the swarm's lead, not the swarm id verbatim", func() {
				body := `{"agent_id":"test-swarm","message":"trace please"}`
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(streamer.capturedAgentID).To(Equal("test-lead"))
				Expect(streamer.capturedMessage).To(Equal("trace please"))
			})

			It("installs a swarm context on the engine and flushes the lifecycle", func() {
				body := `{"agent_id":"test-swarm","message":"hi"}`
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				Expect(engStub.installedContext).NotTo(BeNil())
				Expect(engStub.installedContext.SwarmID).To(Equal("test-swarm"))
				Expect(engStub.installedContext.LeadAgent).To(Equal("test-lead"))
				Expect(engStub.flushCalls).To(Equal(1))
			})

			It("passes a plain agent id through unchanged (no swarm context)", func() {
				body := `{"agent_id":"test-lead","message":"hi"}`
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				Expect(streamer.capturedAgentID).To(Equal("test-lead"))
				// SetSwarmContext is still called — with nil — so the engine
				// reverts to single-agent shape if a previous swarm dispatch
				// left context behind. Flush still runs to keep the wind-down
				// idempotent.
				Expect(engStub.installedContext).To(BeNil())
				Expect(engStub.flushCalls).To(Equal(1))
			})

			It("returns 400 when agent_id matches neither registry", func() {
				body := `{"agent_id":"ghost","message":"hi"}`
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				server.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusBadRequest))
				Expect(streamer.capturedAgentID).To(Equal(""))
				Expect(engStub.installedContext).To(BeNil())
				Expect(engStub.flushCalls).To(Equal(0))
			})
		})
	})

	Describe("GET /", func() {
		It("redirects to the Vue SPA /chat route", func() {
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusFound))
			Expect(recorder.Header().Get("Location")).To(Equal("/chat"))
		})
	})
})

var _ = Describe("GET /api/v1/sessions/{id}/todos", func() {
	var (
		recorder *httptest.ResponseRecorder
		srv      *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			[]skill.Skill{},
		)
	})

	Context("when no todo store is configured", func() {
		It("returns an empty JSON array", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-123/todos", http.NoBody)
			srv.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

			var items []interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &items)).To(Succeed())
			Expect(items).To(BeEmpty())
		})
	})

	Context("when todo store is configured", func() {
		var store *todo.MemoryStore

		BeforeEach(func() {
			store = todo.NewMemoryStore()
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			srv = api.NewServer(
				&mockStreamer{chunks: []provider.StreamChunk{}},
				registry,
				disc,
				[]skill.Skill{},
				api.WithTodoStore(store),
			)
		})

		Context("when session has no todos", func() {
			It("returns an empty JSON array", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/unknown-session/todos", http.NoBody)
				srv.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))

				var items []interface{}
				Expect(json.Unmarshal(recorder.Body.Bytes(), &items)).To(Succeed())
				Expect(items).To(BeEmpty())
			})
		})

		Context("when session has stored todos", func() {
			BeforeEach(func() {
				Expect(store.Set("sess-abc", []todo.Item{
					{Content: "Write tests", Status: "in_progress", Priority: "high"},
					{Content: "Fix bug", Status: "pending", Priority: "medium"},
				})).To(Succeed())
			})

			It("returns the stored todo items", func() {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/sess-abc/todos", http.NoBody)
				srv.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))

				var items []todo.Item
				Expect(json.Unmarshal(recorder.Body.Bytes(), &items)).To(Succeed())
				Expect(items).To(HaveLen(2))
				Expect(items[0].Content).To(Equal("Write tests"))
				Expect(items[0].Status).To(Equal("in_progress"))
				Expect(items[1].Content).To(Equal("Fix bug"))
			})
		})
	})
})

var _ = Describe("Session stream live events", func() {
	var (
		broker     *api.SessionBroker
		mgr        *session.Manager
		srv        *api.Server
		httpServer *httptest.Server
	)

	BeforeEach(func() {
		broker = api.NewSessionBroker()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Content: "hi", Done: true}}})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
			api.WithSessionBroker(broker),
		)
		httpServer = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		httpServer.Close()
	})

	It("forwards live chunks from broker to SSE subscriber", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		source := make(chan provider.StreamChunk, 3)
		source <- provider.StreamChunk{Content: "live-chunk"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var events []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						events = append(events, strings.TrimPrefix(line, "data: "))
					}
					if len(events) > 0 && events[len(events)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- events
		}()

		var events []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&events))

		Expect(events).To(ContainElement(ContainSubstring("live-chunk")))
		Expect(events).To(ContainElement("[DONE]"))
	})

	It("emits named delegation SSE events when chunk carries DelegationInfo", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		source := make(chan provider.StreamChunk, 3)
		source <- provider.StreamChunk{DelegationInfo: &provider.DelegationInfo{
			SourceAgent: "orchestrator",
			TargetAgent: "hephaestus",
			ToolCalls:   3,
			LastTool:    "write",
			Status:      "running",
		}}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		bodyCh := make(chan string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var sb strings.Builder
			for {
				line, readErr := reader.ReadString('\n')
				sb.WriteString(line)
				if strings.Contains(sb.String(), "[DONE]") {
					break
				}
				if readErr != nil {
					break
				}
			}
			bodyCh <- sb.String()
		}()

		var body string
		Eventually(bodyCh, 4*time.Second).Should(Receive(&body))

		Expect(body).To(ContainSubstring(`"type":"delegation"`))
		Expect(body).To(ContainSubstring(`"target_agent":"hephaestus"`))
		Expect(body).To(ContainSubstring(`"tool_calls":3`))
		Expect(body).To(ContainSubstring(`"last_tool":"write"`))
	})

	// Regression for the harness-JSON-leak bug. Before the fix,
	// handleSessionStream ignored chunk.EventType for harness_* events and
	// fell through to the unconditional `if chunk.Content != ""` branch,
	// emitting a plain {"content":"..."} SSE event whose payload was the
	// raw JSON marshalled by emitHarnessComplete (`{"valid":...,"score":...,
	// "attemptCount":2,...}`). The frontend's parseSSEPayload classified that
	// as a content chunk and appended the raw JSON string into the live
	// assistant bubble — non-technical users saw a wall of JSON in chat.
	//
	// Contract: harness_* event chunks MUST be emitted as their typed SSE
	// events (writeSSEHarness*) so the frontend's discriminated union
	// dispatch (`case 'harness_complete': ...`) handles them as observability
	// metadata, not assistant content. The Content field on a typed event
	// chunk MUST NOT be re-emitted as a plain content chunk.
	DescribeTable("emits typed SSE events for harness_* event chunks (no raw JSON in content)",
		func(eventType, content, expectedTypeField string) {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			Expect(err).NotTo(HaveOccurred())

			respCh := make(chan *http.Response, 1)
			go func() {
				resp, doErr := http.DefaultClient.Do(req)
				if doErr == nil {
					respCh <- resp
				}
			}()

			time.Sleep(50 * time.Millisecond)

			source := make(chan provider.StreamChunk, 3)
			source <- provider.StreamChunk{EventType: eventType, Content: content}
			source <- provider.StreamChunk{Done: true}
			close(source)
			go broker.Publish(sess.ID, source)

			var resp *http.Response
			Eventually(respCh, 3*time.Second).Should(Receive(&resp))
			defer resp.Body.Close()

			eventsCh := make(chan []string, 1)
			go func() {
				reader := bufio.NewReader(resp.Body)
				var evts []string
				for {
					line, readErr := reader.ReadString('\n')
					if line != "" {
						line = strings.TrimSpace(line)
						if strings.HasPrefix(line, "data: ") {
							evts = append(evts, strings.TrimPrefix(line, "data: "))
						}
						if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
							break
						}
					}
					if readErr != nil {
						break
					}
				}
				eventsCh <- evts
			}()

			var evts []string
			Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

			// MUST emit the typed event (e.g. {"type":"harness_complete",...}).
			Expect(evts).To(ContainElement(ContainSubstring(`"type":"`+expectedTypeField+`"`)),
				"expected typed SSE event for %s; got: %v", eventType, evts)

			// MUST NOT emit a raw {"content":"..."} chunk carrying the typed
			// event's payload — that's the JSON-leak the user reported.
			for _, e := range evts {
				if strings.Contains(e, `"content":`) && !strings.Contains(e, `"type":`) {
					Fail("plain content chunk leaked harness payload — non-technical users would see raw JSON in chat: " + e)
				}
			}
			Expect(evts).To(ContainElement("[DONE]"))
		},
		Entry("harness_complete with attemptCount JSON",
			"harness_complete",
			`{"valid":true,"score":0.95,"attemptCount":2,"errors":[],"warnings":[]}`,
			"harness_complete",
		),
		Entry("harness_retry with reason content",
			"harness_retry",
			"validation failed: schema mismatch on attempt 1",
			"harness_retry",
		),
		Entry("harness_attempt_start with attempt label",
			"harness_attempt_start",
			"attempt 2 of 3",
			"harness_attempt_start",
		),
		Entry("harness_critic_feedback with critic notes",
			"harness_critic_feedback",
			"critic verdict: REJECT — missing acceptance criteria",
			"harness_critic_feedback",
		),
	)

	// Drop #2 — Thinking SSE dispatch.
	//
	// Pre-fix the dispatcher in handleSessionStream had no branch for
	// chunk.Thinking. The Anthropic adapter at internal/provider/anthropic/
	// streaming.go:142 already emits provider.StreamChunk{Thinking: "..."},
	// and the Drop #1 fix to openaicompat will make zai/glm-4.6 emit it too,
	// but the wire silently swallowed the data. The end result was the live
	// 92-second silent gap we instrumented in Phase 1d (586 reasoning_content
	// deltas dropped end-to-end).
	//
	// Contract: a chunk with Thinking populated MUST emit a typed SSE event
	// {"type":"thinking","content":"<thinking text>"} so the watchdog re-arms
	// and the frontend can route it through its discriminated union without a
	// bespoke channel. Thinking content MUST NOT leak into a plain
	// {"content":"..."} chunk where the chat store would render it as the
	// assistant's reply.
	It("emits a typed thinking SSE event when chunk.Thinking is populated", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		source := make(chan provider.StreamChunk, 3)
		source <- provider.StreamChunk{Thinking: "let me reason about this..."}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		Expect(evts).To(ContainElement(ContainSubstring(`"type":"thinking"`)),
			"expected typed thinking SSE event; got: %v", evts)
		Expect(evts).To(ContainElement(ContainSubstring(`"content":"let me reason about this..."`)),
			"thinking event must carry the model's reasoning text; got: %v", evts)

		// Thinking MUST NOT leak into a plain content chunk — that would
		// render the model's private reasoning as the assistant's reply.
		for _, e := range evts {
			if strings.Contains(e, `"content":"let me reason`) && !strings.Contains(e, `"type":"thinking"`) {
				Fail("thinking content leaked into a plain content chunk: " + e)
			}
		}
		Expect(evts).To(ContainElement("[DONE]"))
	})

	// Track B — provider_changed SSE round-trip.
	//
	// When the failover hook switches providers mid-request (e.g. anthropic
	// 429s and zai/glm-4.6 takes over), the user must be alerted: the
	// answer they're now seeing was produced by a DIFFERENT MODEL than the
	// one selected, which can change quality / style / format. Prior to
	// this branch the failover was silent — the EventBus fired
	// "provider.error" but no SSE consumer subscribed to it, so the chat
	// UI showed no signal at all.
	//
	// Wire contract: the failover hook prepends a synthetic StreamChunk
	// with EventType "provider_changed" and Content carrying a JSON
	// payload {from, to, reason} (see internal/plugin/failover/stream_hook.go
	// providerChangedPayload). The dispatcher in handleSessionStream
	// routes that chunk to writeSSEProviderChanged, which injects the
	// "type":"provider_changed" discriminant the frontend's
	// parseSSEPayload union dispatches on.
	//
	// Forward-compat note: the original {from, to, reason} payload is the
	// failover hook's wire format (no type field). The SSE writer must
	// re-marshal with the type field injected — same pattern as
	// writeSSEDelegationInfo at server.go:1599. A bare pass-through would
	// land as "unknown" in the frontend's discriminated union.
	It("emits a typed provider_changed SSE event when the failover hook prepends a transition chunk", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		source := make(chan provider.StreamChunk, 3)
		// Mirrors what failover.prependProviderChangedChunk emits when
		// anthropic+claude-sonnet-4-6 is rate-limited and zai+glm-4.6
		// takes over.
		source <- provider.StreamChunk{
			EventType: "provider_changed",
			Content:   `{"from":"anthropic+claude-sonnet-4-6","to":"zai+glm-4.6","reason":"rate_limited"}`,
		}
		source <- provider.StreamChunk{Content: "answer from glm-4.6"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		// MUST emit the typed provider_changed event with all three
		// payload fields visible to the frontend dispatcher.
		Expect(evts).To(ContainElement(ContainSubstring(`"type":"provider_changed"`)),
			"expected typed provider_changed SSE event; got: %v", evts)
		Expect(evts).To(ContainElement(ContainSubstring(`"from":"anthropic+claude-sonnet-4-6"`)),
			"from must carry the previous provider+model so the toast can show what was retired")
		Expect(evts).To(ContainElement(ContainSubstring(`"to":"zai+glm-4.6"`)),
			"to must carry the new provider+model the user is now talking to")
		Expect(evts).To(ContainElement(ContainSubstring(`"reason":"rate_limited"`)),
			"reason must be a stable machine-readable token the frontend maps to plain language")

		// MUST NOT leak the raw provider_changed JSON as a plain content
		// chunk — that would render the JSON inside the assistant bubble.
		// The dispatcher at server.go:816 routes EventType-tagged chunks
		// AWAY from the plain content emitter; this guard pins that
		// invariant against accidental fall-through during refactors.
		for _, e := range evts {
			if strings.Contains(e, `"from":"anthropic`) && !strings.Contains(e, `"type":"provider_changed"`) {
				Fail("provider_changed payload leaked into a plain content chunk: " + e)
			}
		}

		// The real assistant content from the new provider must still
		// arrive intact — the transition is metadata, not a stream
		// terminator.
		Expect(evts).To(ContainElement(ContainSubstring("answer from glm-4.6")))
		Expect(evts).To(ContainElement("[DONE]"))
	})

	// model_active SSE round-trip.
	//
	// The user reported (May 2026) that the persistent toolbar chip
	// "shows what was selected, not what actually ran". Until this event
	// existed, the chip stayed at the user's selection until the
	// post-stream reconcile pulled the engine-stamped (model, provider)
	// pair from the persisted assistant message — which arrives AFTER
	// the answer has already streamed in.
	//
	// Wire contract: the failover hook prepends a synthetic StreamChunk
	// with EventType "model_active" and Content carrying a JSON payload
	// {provider, model} on EVERY successful stream (not only on failover).
	// The dispatcher in handleSessionStream routes that chunk to
	// writeSSEModelActive, which injects the "type":"model_active"
	// discriminant the frontend's parseSSEPayload union dispatches on.
	// The chip then pivots from selection to actual immediately, before
	// the first user-visible token arrives.
	It("emits a typed model_active SSE event when the failover hook prepends an active-model chunk", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		source := make(chan provider.StreamChunk, 3)
		// Mirrors what failover.prependModelActiveChunk emits at the
		// start of EVERY stream — selection-vs-actual divergence is
		// the common case the chip needs to pivot on.
		source <- provider.StreamChunk{
			EventType: "model_active",
			Content:   `{"provider":"zai","model":"glm-4.6"}`,
		}
		source <- provider.StreamChunk{Content: "answer body"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		Expect(evts).To(ContainElement(ContainSubstring(`"type":"model_active"`)),
			"expected typed model_active SSE event; got: %v", evts)
		Expect(evts).To(ContainElement(ContainSubstring(`"provider":"zai"`)),
			"provider must carry the actual provider id so the chip can render it")
		Expect(evts).To(ContainElement(ContainSubstring(`"model":"glm-4.6"`)),
			"model must carry the actual model id so the chip can render `<model> · <provider>` truthfully")

		// MUST NOT leak the JSON payload as a plain content chunk —
		// that would render `{"provider":"zai","model":"glm-4.6"}`
		// inside the assistant bubble.
		for _, e := range evts {
			if strings.Contains(e, `"provider":"zai"`) && !strings.Contains(e, `"type":"model_active"`) {
				Fail("model_active payload leaked into a plain content chunk: " + e)
			}
		}

		Expect(evts).To(ContainElement(ContainSubstring("answer body")))
		Expect(evts).To(ContainElement("[DONE]"))
	})

	// context_usage SSE round-trip.
	//
	// Phase 2 of the May 2026 context-window saturation fix. The engine
	// emits a chunk{EventType:"context_usage", Content:<json>} as the
	// first artefact of every Stream. Content is the marshalled
	// engine.contextUsagePayload (input_tokens / output_reserve / limit
	// / percentage / provider / model). The dispatcher in
	// handleSessionStream routes that chunk to writeSSEContextUsage,
	// which injects the "type":"context_usage" discriminant the
	// frontend's parseSSEPayload union dispatches on. The chip then
	// renders the live usage figure alongside the model picker.
	It("emits a typed context_usage SSE event when the engine prepends a usage chunk", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		source := make(chan provider.StreamChunk, 3)
		source <- provider.StreamChunk{
			EventType: "context_usage",
			Content: `{"input_tokens":1234,"output_reserve":4096,"limit":100000,` +
				`"percentage":1,"provider":"zai","model":"glm-4.6"}`,
		}
		source <- provider.StreamChunk{Content: "answer body"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		Expect(evts).To(ContainElement(ContainSubstring(`"type":"context_usage"`)),
			"expected typed context_usage SSE event; got: %v", evts)
		Expect(evts).To(ContainElement(ContainSubstring(`"input_tokens":1234`)),
			"input_tokens must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"output_reserve":4096`)),
			"output_reserve must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"limit":100000`)),
			"limit must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"provider":"zai"`)),
			"provider must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"model":"glm-4.6"`)),
			"model must round-trip onto the wire")

		// MUST NOT leak the JSON payload as a plain content chunk.
		for _, e := range evts {
			if strings.Contains(e, `"input_tokens":1234`) && !strings.Contains(e, `"type":"context_usage"`) {
				Fail("context_usage payload leaked into a plain content chunk: " + e)
			}
		}

		Expect(evts).To(ContainElement(ContainSubstring("answer body")))
		Expect(evts).To(ContainElement("[DONE]"))
	})

	It("does not replay history when broker is live — only broker-published events appear", func() {
		// Regression guard for the SSE duplication bug: when handleSessionStream
		// is connected to a live broker, it must NOT replay sess.Messages before
		// subscribing. Historical content is loaded by the client via
		// GET /messages; replaying it here caused the streaming placeholder to
		// accumulate historical content inside the new response bubble.

		// Restore a session with pre-populated messages directly — avoids the
		// CreateSession→RestoreSessions conflict (RestoreSessions skips existing IDs).
		sessID := "sse-replay-regression-test"
		mgr.RestoreSessions([]*session.Session{
			{
				ID:      sessID,
				AgentID: "test-agent",
				Messages: []session.Message{
					{ID: "m1", Role: "assistant", Content: "historical-msg-one"},
					{ID: "m2", Role: "assistant", Content: "historical-msg-two"},
					// A new user message was appended by POST /messages before SSE opens.
					// The fast-path only fires when the last message is non-user, so this
					// correctly models the real-app flow where a new Publish is pending.
					{ID: "m3", Role: "user", Content: "new-user-prompt"},
				},
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sessID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		// Publish a single live chunk then close the broker channel.
		source := make(chan provider.StreamChunk, 2)
		source <- provider.StreamChunk{Content: "live-only-chunk"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sessID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		// The live-published chunk must appear.
		Expect(evts).To(ContainElement(ContainSubstring("live-only-chunk")))
		// Historical messages must NOT appear — they would duplicate content
		// already rendered by the frontend via GET /messages.
		Expect(evts).NotTo(ContainElement(ContainSubstring("historical-msg-one")))
		Expect(evts).NotTo(ContainElement(ContainSubstring("historical-msg-two")))
		Expect(evts).To(ContainElement("[DONE]"))
	})

	It("emits no content events to a fresh subscriber on an idle session even when no broker is wired", func() {
		// Regression for SSE replay-on-subscribe bug. The bug: when handleSessionStream
		// was hit on a server with sessionBroker == nil (production wiring previously
		// gated broker creation on backgroundManager being non-nil, which it isn't for
		// non-delegating agents), the handler iterated sess.Messages and emitted every
		// assistant content as an SSE chunk, then [DONE]. A fresh subscriber on an
		// idle session would therefore receive a "ghost" of the previous turn — which
		// the Vue frontend rendered as a duplicate bubble inside the next turn's
		// streaming placeholder.
		//
		// Contract: the SSE stream is for events emitted strictly after subscription.
		// Historical content lives on GET /messages. A fresh subscriber on an idle
		// session must receive zero content events.
		brokerlessMgr := session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{}})
		brokerlessSrv := api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			agent.NewRegistry(),
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(brokerlessMgr),
			// NOTE: deliberately no WithSessionBroker — mirrors the
			// pre-fix production wiring where the broker stayed nil for
			// agents without delegation. The handler must not replay
			// sess.Messages even in this configuration.
		)
		brokerlessHTTP := httptest.NewServer(brokerlessSrv.Handler())
		defer brokerlessHTTP.Close()

		brokerlessMgr.RestoreSessions([]*session.Session{
			{
				ID:      "sse-no-broker-replay",
				AgentID: "test-agent",
				Messages: []session.Message{
					{ID: "u1", Role: "user", Content: "ping"},
					{ID: "a1", Role: "assistant", Content: "PONG"},
				},
			},
		})

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
			brokerlessHTTP.URL+"/api/v1/sessions/sse-no-broker-replay/stream", http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		resp, err := http.DefaultClient.Do(streamReq)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		// MUST receive only [DONE]; never any historical content.
		for _, e := range evts {
			Expect(e).NotTo(ContainSubstring(`"content"`),
				"fresh subscriber on idle session must receive zero content events; got: %v", evts)
			Expect(e).NotTo(ContainSubstring("PONG"),
				"fresh subscriber must not see prior assistant message replayed; got: %v", evts)
		}
		Expect(evts).To(ContainElement("[DONE]"))
	})

	It("emits no content events to a fresh subscriber after a sealed turn (real broker, full POST→stream flow)", func() {
		// End-to-end regression: drive the real Server through POST /messages
		// to seal a turn, then subscribe with a fresh GET /stream and assert
		// no content is replayed. This mirrors the production curl reproduction
		// without involving any provider-layer mocks beyond the streamer.
		realBroker := api.NewSessionBroker()
		realStreamer := &mockStreamer{chunks: []provider.StreamChunk{
			{Content: "PONG"},
			{Done: true},
		}}
		realMgr := session.NewManager(realStreamer)
		realSrv := api.NewServer(
			realStreamer,
			agent.NewRegistry(),
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(realMgr),
			api.WithSessionBroker(realBroker),
		)
		realHTTP := httptest.NewServer(realSrv.Handler())
		defer realHTTP.Close()

		sess, err := realMgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		// Seal turn 1: POST blocks until the broker drains the chunks.
		postResp, err := http.Post(
			realHTTP.URL+"/api/v1/sessions/"+sess.ID+"/messages",
			"application/json",
			strings.NewReader(`{"content":"hi"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		_ = postResp.Body.Close()
		Expect(postResp.StatusCode).To(Equal(http.StatusOK))
		Expect(realBroker.IsPublishing(sess.ID)).To(BeFalse(),
			"broker must not still be publishing after POST returned")

		// Fresh SSE subscriber on the now-idle session.
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		streamReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
			realHTTP.URL+"/api/v1/sessions/"+sess.ID+"/stream", http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		streamResp, err := http.DefaultClient.Do(streamReq)
		Expect(err).NotTo(HaveOccurred())
		defer streamResp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(streamResp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		for _, e := range evts {
			Expect(e).NotTo(ContainSubstring(`"content"`),
				"fresh subscriber after sealed turn must receive zero content events; got: %v", evts)
		}
		Expect(evts).To(ContainElement("[DONE]"))
	})

	It("drives concurrent POST /messages and GET /stream without a data race on session.Messages", func() {
		// Regression for the data race surfaced during Track A's
		// streaming signal-drop fixes. `go test -race` reported:
		//
		//   Write at 0x... by goroutine N: session/manager.go:647
		//     (sess.Messages = append(...) under WLock in SendMessage)
		//   Previous read at 0x... by goroutine M: api/server.go:756
		//     (msgs := freshSess.Messages outside any lock in
		//      handleSessionStream's idle-session fast-path)
		//
		// The read site held no lock because GetSession returns the
		// *Session pointer and releases its RLock on return. The fix
		// replaces the dereference with Manager.LastMessageRole, which
		// projects the role value under RLock — no pointer leaks past
		// the lock boundary. This spec drives both code paths in
		// parallel against a real Server and httptest backend so the
		// race detector flags any future regression.
		raceBroker := api.NewSessionBroker()
		raceStreamer := &mockStreamer{chunks: []provider.StreamChunk{
			{Content: "ack"},
			{Done: true},
		}}
		raceMgr := session.NewManager(raceStreamer)
		raceSrv := api.NewServer(
			raceStreamer,
			agent.NewRegistry(),
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(raceMgr),
			api.WithSessionBroker(raceBroker),
		)
		raceHTTP := httptest.NewServer(raceSrv.Handler())
		defer raceHTTP.Close()

		// Seed via RestoreSessions so the session has an assistant
		// message as its tail, exercising the SSE fast-path's
		// "last message role != user" branch on each GET. The fast
		// path is exactly where the pre-fix dereference of
		// freshSess.Messages raced with SendMessage's append.
		const sessID = "race-session-id"
		raceMgr.RestoreSessions([]*session.Session{
			{
				ID:      sessID,
				AgentID: "race-agent",
				Messages: []session.Message{
					{ID: "u0", Role: "user", Content: "seed"},
					{ID: "a0", Role: "assistant", Content: "ack"},
				},
			},
		})

		const iterations = 20
		var wg sync.WaitGroup
		wg.Add(2)

		// Writer goroutine: fires POSTs that flow through
		// SendMessage's sess.Messages = append under WLock.
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			for range iterations {
				resp, postErr := http.Post(
					raceHTTP.URL+"/api/v1/sessions/"+sessID+"/messages",
					"application/json",
					strings.NewReader(`{"content":"hello"}`),
				)
				if postErr == nil {
					_ = resp.Body.Close()
				}
			}
		}()

		// Reader goroutine: fires GETs that flow through
		// handleSessionStream's idle-session fast-path which used to
		// read sess.Messages outside the lock.
		go func() {
			defer GinkgoRecover()
			defer wg.Done()
			for range iterations {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet,
					raceHTTP.URL+"/api/v1/sessions/"+sessID+"/stream", http.NoBody)
				if reqErr == nil {
					if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						_ = resp.Body.Close()
					}
				}
				cancel()
			}
		}()

		wg.Wait()
		// The spec passes if `go test -race` does not abort. No
		// content assertions — the contract under test is purely
		// concurrency: both endpoints stay race-free under load.
	})

	// Critical-stream-error severity gating regression specs.
	//
	// Pre-fix the SSE fan-out at handleSessionStream's chunk.Error
	// branch wrote a sanitized "stream_error" event and then
	// `continue`d the receive loop unconditionally. That meant a
	// fatal provider error (revoked OAuth token, 401, model-not-
	// found, billing/quota lockout) was treated indistinguishably
	// from a transient blip: the loop kept reading subsequent
	// chunks from the live channel and emitting whatever landed,
	// and the UI had no signal that the session had reached an
	// unrecoverable state.
	//
	// internal/provider/stream_error.go has classified error
	// severities since P18a, but IsCriticalStreamError had zero
	// production callers — the broker fan-out never consulted it.
	//
	// The contract these specs pin:
	//
	//   1. When chunk.Error classifies as SeverityCritical, the
	//      fan-out emits a typed critical-class SSE error event,
	//      writes [DONE], and breaks the loop. NO further chunks
	//      from the live channel reach the client after the
	//      critical signal — even chunks the broker has already
	//      buffered for fan-out.
	//
	//   2. When chunk.Error classifies as transient/user (the
	//      non-critical path), the fan-out emits the existing
	//      "stream_error" event and continues the loop. Subsequent
	//      chunks on the live channel still flow through to the
	//      client.
	//
	// Both specs assert behaviour on the wire (the bytes the SSE
	// reader observes), not internal function names. The
	// behaviour-pinning shape is the same as the
	// "forwards live chunks" spec above — same fixture, same
	// SSE reader pattern.
	It("breaks the SSE fan-out and emits a critical-class error when chunk.Error is critical, with no further chunks reaching the client", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		// "401 unauthorized" matches the criticalKeywords list in
		// internal/provider/stream_error.go ("401" and "unauthori"),
		// so ClassifyStreamError returns SeverityCritical without
		// needing a structured *provider.Error wrapper. The post-
		// critical chunks ("after-critical-content" and Done) are
		// pushed into the broker before Publish runs so the broker
		// fans them out promptly — pre-fix, the consumer's
		// continue-loop would surface them on the wire.
		source := make(chan provider.StreamChunk, 4)
		source <- provider.StreamChunk{Content: "pre-error-content"}
		source <- provider.StreamChunk{Error: errors.New("401 unauthorized")}
		source <- provider.StreamChunk{Content: "after-critical-content"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var events []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						events = append(events, strings.TrimPrefix(line, "data: "))
					}
					if len(events) > 0 && events[len(events)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- events
		}()

		var events []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&events))

		// Pre-error content must reach the client (we did not
		// regress the happy path before the critical chunk).
		Expect(events).To(ContainElement(ContainSubstring("pre-error-content")),
			"chunks emitted before the critical error must still reach the client")

		// The critical signal must surface on the wire as a
		// distinct event from the non-critical "stream error".
		// The exact category text comes from clientError's
		// "stream_critical" branch in errors.go.
		Expect(events).To(ContainElement(ContainSubstring("critical stream error")),
			"a critical chunk.Error must surface as a critical-class SSE event, not as the non-critical 'stream error' message")

		// The fan-out must terminate after the critical signal.
		// Chunks the broker fans out after the critical error —
		// "after-critical-content" here — must NOT reach the
		// client. This is the bug the gate fixes.
		Expect(events).NotTo(ContainElement(ContainSubstring("after-critical-content")),
			"after a critical chunk.Error the SSE fan-out must break and emit no further chunks to the client")

		// The session settles into a recoverable closed state:
		// the client always sees the [DONE] sentinel so it can
		// re-render and resume.
		Expect(events).To(ContainElement("[DONE]"),
			"the SSE handler must always close the stream with [DONE] after a critical error so the client can settle")
	})

	It("continues the SSE fan-out on a non-critical chunk.Error and lets subsequent chunks flow to the client", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		// "connection refused" matches the transientKeywords list,
		// so ClassifyStreamError returns SeverityTransient and
		// IsCriticalStreamError reports false. The gate must NOT
		// fire — subsequent chunks must still surface on the wire,
		// preserving today's behaviour for self-healing errors.
		source := make(chan provider.StreamChunk, 4)
		source <- provider.StreamChunk{Content: "before-transient-error"}
		source <- provider.StreamChunk{Error: errors.New("connection refused")}
		source <- provider.StreamChunk{Content: "after-transient-error"}
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var events []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						events = append(events, strings.TrimPrefix(line, "data: "))
					}
					if len(events) > 0 && events[len(events)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- events
		}()

		var events []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&events))

		// Pre-error content reaches the client.
		Expect(events).To(ContainElement(ContainSubstring("before-transient-error")),
			"chunks emitted before a transient error must reach the client")

		// The non-critical event surfaces with the existing
		// "stream error" message — NOT the critical-class one.
		Expect(events).To(ContainElement(ContainSubstring("stream error")),
			"a transient chunk.Error must surface as the existing 'stream error' SSE event")
		Expect(events).NotTo(ContainElement(ContainSubstring("critical stream error")),
			"a transient chunk.Error must NOT escalate to the critical-class SSE event")

		// The contract: the fan-out keeps reading and chunks
		// after a non-critical error still reach the client.
		// This is the regression-resistance assertion — without
		// it, a future change that always-breaks on chunk.Error
		// would silently drop transient-error sessions.
		Expect(events).To(ContainElement(ContainSubstring("after-transient-error")),
			"after a transient chunk.Error the SSE fan-out must continue and subsequent chunks must reach the client")

		Expect(events).To(ContainElement("[DONE]"))
	})
})

// Sibling races to the SSE-fast-path data race fixed in commit aaa6f1f:
// every handler that called GetSession and then dereferenced fields on
// the returned *Session held no lock during the read. SendMessage's
// `sess.Messages = append(...)` under WLock raced each of those reads.
// These specs drive concurrent POST /messages (the writer) against each
// affected reader handler under `-race` so the race detector flags any
// future regression.
//
// All four specs share the same shape: seed a session via
// RestoreSessions, fire iterations of POSTs in one goroutine, fire
// iterations of GET/PATCH against the targeted handler in a second
// goroutine, wait. No content assertions — the contract under test is
// purely "no race detected".
var _ = Describe("GetSession-deref-after-lock-release sibling races", func() {
	const (
		sessID     = "sibling-race-session"
		iterations = 20
	)

	// raceSetup builds an httptest server with a real Manager + Broker
	// and seeds one session. Returned closer must be invoked.
	raceSetup := func() (*session.Manager, *httptest.Server, func()) {
		broker := api.NewSessionBroker()
		streamer := &mockStreamer{chunks: []provider.StreamChunk{
			{Content: "ack"},
			{Done: true},
		}}
		mgr := session.NewManager(streamer)
		srv := api.NewServer(
			streamer,
			agent.NewRegistry(),
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(mgr),
			api.WithSessionBroker(broker),
		)
		ts := httptest.NewServer(srv.Handler())
		mgr.RestoreSessions([]*session.Session{
			{
				ID:                sessID,
				AgentID:           "race-agent",
				CurrentAgentID:    "race-agent",
				CurrentProviderID: "anthropic",
				CurrentModelID:    "claude-3-5-sonnet",
				Messages: []session.Message{
					{ID: "u0", Role: "user", Content: "seed"},
					{ID: "a0", Role: "assistant", Content: "ack"},
				},
			},
		})
		return mgr, ts, ts.Close
	}

	// driveWriter fires POST /messages in a tight loop; each call goes
	// through SendMessage's append-under-WLock path that all four
	// reader sites used to race against.
	driveWriter := func(wg *sync.WaitGroup, baseURL string) {
		defer GinkgoRecover()
		defer wg.Done()
		for range iterations {
			resp, postErr := http.Post(
				baseURL+"/api/v1/sessions/"+sessID+"/messages",
				"application/json",
				strings.NewReader(`{"content":"hello"}`),
			)
			if postErr == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}
		}
	}

	// driveReader fires the targeted handler request in a tight loop.
	// Each spec passes a different requestBuilder so the same wrapper
	// drives POST/PATCH/GET shapes interchangeably.
	driveReader := func(wg *sync.WaitGroup, requestBuilder func(ctx context.Context) (*http.Request, error)) {
		defer GinkgoRecover()
		defer wg.Done()
		for range iterations {
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			req, reqErr := requestBuilder(ctx)
			if reqErr == nil {
				if resp, doErr := http.DefaultClient.Do(req); doErr == nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					_ = resp.Body.Close()
				}
			}
			cancel()
		}
	}

	It("handleSessionMessage (POST /messages → NewSessionResponse) is race-free", func() {
		// Pre-fix server.go:667 read NewSessionResponse(sess) on a
		// *Session whose RLock had already dropped. POST /messages
		// itself drives both the writer and the reader through that
		// path, so two concurrent POSTs are sufficient — one's
		// append-under-WLock races the other's NewSessionResponse
		// deref.
		_, ts, closeFn := raceSetup()
		defer closeFn()

		var wg sync.WaitGroup
		wg.Add(2)
		go driveWriter(&wg, ts.URL)
		go driveWriter(&wg, ts.URL)
		wg.Wait()
	})

	It("handleSessionMessages (GET /messages → sess.Messages) is race-free", func() {
		// Pre-fix server.go:1722 read `messages := sess.Messages`
		// outside any lock, racing SendMessage's append.
		_, ts, closeFn := raceSetup()
		defer closeFn()

		var wg sync.WaitGroup
		wg.Add(2)
		go driveWriter(&wg, ts.URL)
		go driveReader(&wg, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodGet,
				ts.URL+"/api/v1/sessions/"+sessID+"/messages", http.NoBody)
		})
		wg.Wait()
	})

	It("handleUpdateSessionAgent (PATCH /agent → NewSessionResponse) is race-free", func() {
		// Pre-fix server.go:1796 called GetSession after
		// UpdateSessionAgent and then NewSessionResponse(sess) on
		// the leaked pointer.
		_, ts, closeFn := raceSetup()
		defer closeFn()

		var wg sync.WaitGroup
		wg.Add(2)
		go driveWriter(&wg, ts.URL)
		go driveReader(&wg, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodPatch,
				ts.URL+"/api/v1/sessions/"+sessID+"/agent",
				strings.NewReader(`{"agentId":"race-agent"}`))
		})
		wg.Wait()
	})

	It("handleUpdateSessionModel (PATCH /model → NewSessionResponse) is race-free", func() {
		// Pre-fix server.go:1841 called GetSession after
		// UpdateSessionModel and then NewSessionResponse(sess) on
		// the leaked pointer.
		_, ts, closeFn := raceSetup()
		defer closeFn()

		var wg sync.WaitGroup
		wg.Add(2)
		go driveWriter(&wg, ts.URL)
		go driveReader(&wg, func(ctx context.Context) (*http.Request, error) {
			return http.NewRequestWithContext(ctx, http.MethodPatch,
				ts.URL+"/api/v1/sessions/"+sessID+"/model",
				strings.NewReader(`{"providerId":"anthropic","modelId":"claude-3-5-sonnet"}`))
		})
		wg.Wait()
	})
})

func parseSSEEvents(body *bytes.Buffer) []string {
	var events []string
	reader := bufio.NewReader(body)

	for {
		line, err := reader.ReadString('\n')
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			events = append(events, data)
		}
	}

	return events
}

var _ = Describe("Session hierarchy endpoints", func() {
	var (
		server *api.Server
		mgr    *session.Manager
	)

	BeforeEach(func() {
		streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
		mgr = session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		server = api.NewServer(
			streamer,
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
	})

	Describe("GET /api/v1/sessions/{id}/children", func() {
		It("returns 200 with empty list for non-existent session", func() {
			req := httptest.NewRequest("GET", "/api/v1/sessions/nonexistent/children", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var children []*session.Session
			err := json.Unmarshal(w.Body.Bytes(), &children)
			Expect(err).NotTo(HaveOccurred())
			Expect(children).To(BeEmpty())
		})

		It("returns 200 with empty list when session has no children", func() {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest("GET", "/api/v1/sessions/"+sess.ID+"/children", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var children []*session.Session
			err = json.Unmarshal(w.Body.Bytes(), &children)
			Expect(err).NotTo(HaveOccurred())
			Expect(children).To(BeEmpty())
		})
	})

	Describe("GET /api/v1/sessions/{id}/tree", func() {
		It("returns 200 with session tree", func() {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest("GET", "/api/v1/sessions/"+sess.ID+"/tree", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var tree []*session.Session
			err = json.Unmarshal(w.Body.Bytes(), &tree)
			Expect(err).NotTo(HaveOccurred())
			Expect(tree).NotTo(BeEmpty())
		})

		It("returns 404 for non-existent session", func() {
			req := httptest.NewRequest("GET", "/api/v1/sessions/nonexistent/tree", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotFound))
		})
	})

	Describe("GET /api/v1/sessions/{id}/parent", func() {
		It("returns 200 with root session", func() {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest("GET", "/api/v1/sessions/"+sess.ID+"/parent", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var rootSess *session.Session
			err = json.Unmarshal(w.Body.Bytes(), &rootSess)
			Expect(err).NotTo(HaveOccurred())
			Expect(rootSess).NotTo(BeNil())
		})

		It("returns 404 for non-existent session", func() {
			req := httptest.NewRequest("GET", "/api/v1/sessions/nonexistent/parent", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotFound))
		})
	})
})

var _ = Describe("Background task endpoints", func() {
	var (
		server *api.Server
		mgr    *engine.BackgroundTaskManager
	)

	BeforeEach(func() {
		mgr = engine.NewBackgroundTaskManager()
		streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		sessionMgr := session.NewManager(streamer)
		server = api.NewServer(
			streamer,
			registry,
			disc,
			nil,
			api.WithSessionManager(sessionMgr),
			api.WithBackgroundManager(mgr),
		)
	})

	Describe("GET /api/v1/tasks", func() {
		It("returns 200 with empty task list", func() {
			req := httptest.NewRequest("GET", "/api/v1/tasks", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusOK))

			var tasks []*engine.BackgroundTask
			err := json.Unmarshal(w.Body.Bytes(), &tasks)
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns 501 when no background manager is configured", func() {
			streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			sessionMgr := session.NewManager(streamer)
			noMgrServer := api.NewServer(
				streamer,
				registry,
				disc,
				nil,
				api.WithSessionManager(sessionMgr),
			)

			req := httptest.NewRequest("GET", "/api/v1/tasks", http.NoBody)
			w := httptest.NewRecorder()
			noMgrServer.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("GET /api/v1/tasks/{id}", func() {
		It("returns 404 for non-existent task", func() {
			req := httptest.NewRequest("GET", "/api/v1/tasks/nonexistent", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotFound))
		})

		It("returns 501 when no background manager is configured", func() {
			streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			sessionMgr := session.NewManager(streamer)
			noMgrServer := api.NewServer(
				streamer,
				registry,
				disc,
				nil,
				api.WithSessionManager(sessionMgr),
			)

			req := httptest.NewRequest("GET", "/api/v1/tasks/any", http.NoBody)
			w := httptest.NewRecorder()
			noMgrServer.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("DELETE /api/v1/tasks/{id}", func() {
		It("returns 404 for non-existent task", func() {
			req := httptest.NewRequest("DELETE", "/api/v1/tasks/nonexistent", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotFound))
		})

		It("returns 501 when no background manager is configured", func() {
			streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			sessionMgr := session.NewManager(streamer)
			noMgrServer := api.NewServer(
				streamer,
				registry,
				disc,
				nil,
				api.WithSessionManager(sessionMgr),
			)

			req := httptest.NewRequest("DELETE", "/api/v1/tasks/any", http.NoBody)
			w := httptest.NewRecorder()
			noMgrServer.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("DELETE /api/v1/tasks", func() {
		It("returns 400 when ?all=true is not set", func() {
			req := httptest.NewRequest("DELETE", "/api/v1/tasks", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusBadRequest))
		})

		It("returns 204 when ?all=true is set", func() {
			req := httptest.NewRequest("DELETE", "/api/v1/tasks?all=true", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNoContent))
		})

		It("returns 501 when no background manager is configured", func() {
			streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			sessionMgr := session.NewManager(streamer)
			noMgrServer := api.NewServer(
				streamer,
				registry,
				disc,
				nil,
				api.WithSessionManager(sessionMgr),
			)

			req := httptest.NewRequest("DELETE", "/api/v1/tasks?all=true", http.NoBody)
			w := httptest.NewRecorder()
			noMgrServer.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})
})

var _ = Describe("Session manager nil-safety", func() {
	var server *api.Server

	BeforeEach(func() {
		streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		server = api.NewServer(streamer, registry, disc, nil)
	})

	Describe("POST /api/v1/sessions", func() {
		It("returns 501 when session manager is nil", func() {
			body := `{"agent_id":"test-agent"}`
			req := httptest.NewRequest("POST", "/api/v1/sessions", strings.NewReader(body))
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("GET /api/v1/sessions", func() {
		It("returns 501 when session manager is nil", func() {
			req := httptest.NewRequest("GET", "/api/v1/sessions", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("POST /api/v1/sessions/{id}/messages", func() {
		It("returns 501 when session manager is nil", func() {
			body := `{"content":"hello"}`
			req := httptest.NewRequest("POST", "/api/v1/sessions/sess-1/messages", strings.NewReader(body))
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("GET /api/v1/sessions/{id}/stream", func() {
		It("returns 501 when session manager is nil", func() {
			req := httptest.NewRequest("GET", "/api/v1/sessions/sess-1/stream", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Describe("GET /api/v1/sessions/{id}/ws", func() {
		It("returns 501 when session manager is nil", func() {
			req := httptest.NewRequest("GET", "/api/v1/sessions/sess-1/ws", http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNotImplemented))
		})
	})
})

var _ = Describe("GET /health", func() {
	var server *api.Server

	BeforeEach(func() {
		streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		server = api.NewServer(streamer, registry, disc, nil)
	})

	It("returns 200 with status ok", func() {
		req := httptest.NewRequest("GET", "/health", http.NoBody)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
		Expect(w.Header().Get("Content-Type")).To(Equal("application/json"))

		var resp map[string]string
		Expect(json.Unmarshal(w.Body.Bytes(), &resp)).To(Succeed())
		Expect(resp["status"]).To(Equal("ok"))
	})
})

var _ = Describe("GET /metrics", func() {
	It("returns 200 with metrics when handler is configured", func() {
		reg := prometheus.NewRegistry()
		metricsHandler := promhttp.HandlerFor(reg, promhttp.HandlerOpts{})

		streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		server := api.NewServer(
			streamer, registry, disc, nil,
			api.WithMetricsHandler(metricsHandler),
		)

		req := httptest.NewRequest("GET", "/metrics", http.NoBody)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)

		Expect(w.Code).To(Equal(http.StatusOK))
	})

	It("does not serve metrics when no handler is configured", func() {
		streamer := &mockStreamer{chunks: []provider.StreamChunk{}}
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		server := api.NewServer(streamer, registry, disc, nil)

		req := httptest.NewRequest("GET", "/metrics", http.NoBody)
		w := httptest.NewRecorder()
		server.Handler().ServeHTTP(w, req)

		Expect(w.Header().Get("Content-Type")).NotTo(ContainSubstring("text/plain"))
	})
})

var _ = Describe("GET /api/v1/sessions JSON contract", func() {
	var (
		recorder *httptest.ResponseRecorder
		mgr      *session.Manager
		srv      *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Content: "ok", Done: true}}})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
	})

	Context("with one created session", func() {
		var sess *session.Session

		BeforeEach(func() {
			var err error
			sess, err = mgr.CreateSession("agent-x")
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns HTTP 200 with a JSON array body", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			srv.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
		})

		It("emits a non-empty title for the listed session", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			srv.Handler().ServeHTTP(recorder, req)

			var rows []map[string]interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &rows)).To(Succeed())
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["id"]).To(Equal(sess.ID))
			title, ok := rows[0]["title"].(string)
			Expect(ok).To(BeTrue(), "title should be a string in the JSON payload")
			Expect(title).NotTo(BeEmpty(), "Title should be populated from session metadata, not hardcoded empty")
		})

		It("emits a createdAt key in the JSON summary", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			srv.Handler().ServeHTTP(recorder, req)

			var rows []map[string]interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &rows)).To(Succeed())
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]).To(HaveKey("createdAt"), "frontend SessionSummary contract requires createdAt")
			createdAt, ok := rows[0]["createdAt"].(string)
			Expect(ok).To(BeTrue(), "createdAt should be a string in the JSON payload")
			Expect(createdAt).NotTo(BeEmpty())
			Expect(createdAt).NotTo(Equal("0001-01-01T00:00:00Z"), "createdAt should be a real timestamp, not the zero value")
		})

		It("emits a non-zero updatedAt timestamp", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			srv.Handler().ServeHTTP(recorder, req)

			var rows []map[string]interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &rows)).To(Succeed())
			Expect(rows).To(HaveLen(1))
			updatedAt, ok := rows[0]["updatedAt"].(string)
			Expect(ok).To(BeTrue(), "updatedAt should be a string in the JSON payload")
			Expect(updatedAt).NotTo(Equal("0001-01-01T00:00:00Z"), "updatedAt should be populated, not the zero time")
		})
	})

	Context("when no session manager is configured", func() {
		It("returns HTTP 501", func() {
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			bare := api.NewServer(&mockStreamer{}, registry, disc, nil)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			bare.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusNotImplemented))
		})
	})

	Context("isStreaming field in session list", func() {
		It("emits isStreaming: false when no broker is configured for a session", func() {
			sess, err := mgr.CreateSession("agent-x")
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			srv.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			var rows []map[string]interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &rows)).To(Succeed())
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["id"]).To(Equal(sess.ID))
			Expect(rows[0]).To(HaveKey("isStreaming"),
				"frontend needs isStreaming on every summary to detect active sessions on page load")
			Expect(rows[0]["isStreaming"]).To(BeFalse())
		})

		It("emits isStreaming: true for a session whose broker is actively publishing", func() {
			broker := api.NewSessionBroker()
			srvWithBroker := api.NewServer(
				&mockStreamer{chunks: []provider.StreamChunk{}},
				agent.NewRegistry(),
				discovery.NewAgentDiscovery(nil),
				nil,
				api.WithSessionManager(mgr),
				api.WithSessionBroker(broker),
			)
			sess, err := mgr.CreateSession("agent-streaming")
			Expect(err).NotTo(HaveOccurred())

			// Start publishing in the background so the broker marks the session active.
			chunks := make(chan provider.StreamChunk)
			go broker.Publish(sess.ID, chunks)

			// Give the goroutine time to set active before the request.
			Eventually(func() bool {
				return broker.IsPublishing(sess.ID)
			}).Should(BeTrue())

			req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions", http.NoBody)
			w := httptest.NewRecorder()
			srvWithBroker.Handler().ServeHTTP(w, req)

			Expect(w.Code).To(Equal(http.StatusOK))
			var rows []map[string]interface{}
			Expect(json.Unmarshal(w.Body.Bytes(), &rows)).To(Succeed())
			Expect(rows).To(HaveLen(1))
			Expect(rows[0]["id"]).To(Equal(sess.ID))
			Expect(rows[0]).To(HaveKey("isStreaming"))
			Expect(rows[0]["isStreaming"]).To(BeTrue(),
				"session list must expose isStreaming: true when the broker has an active publish for the session")

			// Clean up: close the channel to end the publish goroutine.
			close(chunks)
		})
	})
})

var _ = Describe("GET /api/v1/sessions/{id}/messages JSON contract", func() {
	var (
		recorder *httptest.ResponseRecorder
		mgr      *session.Manager
		srv      *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Content: "ok", Done: true}}})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
	})

	It("returns [] not null for a freshly created session with no messages", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/"+sess.ID+"/messages", http.NoBody)
		srv.Handler().ServeHTTP(recorder, req)

		Expect(recorder.Code).To(Equal(http.StatusOK))
		body := strings.TrimSpace(recorder.Body.String())
		Expect(body).To(Equal("[]"), "empty messages slice must serialise to [] not null")
		Expect(body).NotTo(Equal("null"))
	})

	It("returns [] not null for a restored session whose Messages field is nil", func() {
		mgr.RestoreSessions([]*session.Session{{ID: "restored-1", AgentID: "agent-y"}})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/restored-1/messages", http.NoBody)
		srv.Handler().ServeHTTP(recorder, req)

		Expect(recorder.Code).To(Equal(http.StatusOK))
		body := strings.TrimSpace(recorder.Body.String())
		Expect(body).To(Equal("[]"), "restored session with nil Messages must serialise to [] not null")
	})

	It("returns 404 for an unknown session id", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/sessions/does-not-exist/messages", http.NoBody)
		srv.Handler().ServeHTTP(recorder, req)

		Expect(recorder.Code).To(Equal(http.StatusNotFound))
	})
})

var _ = Describe("POST /api/v1/sessions JSON contract", func() {
	var (
		recorder *httptest.ResponseRecorder
		mgr      *session.Manager
		srv      *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Content: "ok", Done: true}}})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
	})

	postCreate := func() map[string]interface{} {
		body := `{"agent_id":"agent-x"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		var out map[string]interface{}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		return out
	}

	It("returns a body with camelCase agentId, not snake_case agent_id", func() {
		out := postCreate()
		Expect(out).To(HaveKey("agentId"), "create response must use camelCase agentId to match SessionSummary contract")
		Expect(out).NotTo(HaveKey("agent_id"), "create response must not leak snake_case agent_id")
		Expect(out["agentId"]).To(Equal("agent-x"))
	})

	It("returns a body with camelCase createdAt and updatedAt", func() {
		out := postCreate()
		Expect(out).To(HaveKey("createdAt"))
		Expect(out).To(HaveKey("updatedAt"))
		Expect(out).NotTo(HaveKey("created_at"))
		Expect(out).NotTo(HaveKey("updated_at"))
	})

	It("returns a body that includes messageCount: 0 for a freshly created session", func() {
		out := postCreate()
		Expect(out).To(HaveKey("messageCount"), "create response must include messageCount so the frontend doesn't read undefined")
		count, ok := out["messageCount"].(float64)
		Expect(ok).To(BeTrue(), "messageCount should be a number in the JSON payload")
		Expect(count).To(Equal(float64(0)))
	})
})

var _ = Describe("POST /api/v1/sessions/{id}/messages JSON contract", func() {
	var (
		recorder *httptest.ResponseRecorder
		mgr      *session.Manager
		srv      *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Content: "ok", Done: true}}})
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
	})

	It("returns a response with messageCount reflecting the appended message", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		body := `{"content":"hello"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/messages", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		var out map[string]interface{}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		Expect(out).To(HaveKey("messageCount"), "send-message response must include messageCount")
		Expect(out).To(HaveKey("agentId"))
		Expect(out).NotTo(HaveKey("agent_id"))
		count, ok := out["messageCount"].(float64)
		Expect(ok).To(BeTrue())
		Expect(count).To(BeNumerically(">=", 1), "messageCount should reflect at least the user message that was just appended")
	})
})

var _ = Describe("POST /api/v1/sessions/{id}/messages assistant reply contract", func() {
	assertAssistantInResponse := func(withBroker bool) {
		recorder := httptest.NewRecorder()
		streamer := &mockStreamer{chunks: []provider.StreamChunk{{Content: "ok"}, {Done: true}}}
		mgr := session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		opts := []api.ServerOption{api.WithSessionManager(mgr)}
		if withBroker {
			opts = append(opts, api.WithSessionBroker(api.NewSessionBroker()))
		}
		srv := api.NewServer(streamer, registry, disc, nil, opts...)

		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		body := `{"content":"hello"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/messages", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		var out struct {
			MessageCount int               `json:"messageCount"`
			Messages     []session.Message `json:"messages"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		Expect(out.MessageCount).To(BeNumerically(">=", 2), "response must include both user and assistant messages before returning")

		var assistantContent string
		for _, m := range out.Messages {
			if m.Role == "assistant" {
				assistantContent = m.Content
				break
			}
		}
		Expect(assistantContent).To(Equal("ok"), "assistant reply must be appended to the session before the HTTP response is written so the frontend renders it without polling")
	}

	It("includes the assistant reply when no broker is configured", func() {
		assertAssistantInResponse(false)
	})

	It("includes the assistant reply when a broker is configured", func() {
		assertAssistantInResponse(true)
	})

	It("streams all chunks to the SSE subscriber and persists the assistant reply before returning", func() {
		// Full round-trip: POST /messages → broker publishes → SSE client
		// receives chunks → GET /messages confirms persistence.
		//
		// This exercises the three behaviours added by the May 2026 fixes:
		//   1. The handler streams every chunk to the live SSE subscriber.
		//   2. AccumulateStream persists the assistant reply before returning.
		//   3. GET /messages reflects the completed reply immediately after
		//      the POST response is received — no polling required.
		streamer := &mockStreamer{chunks: []provider.StreamChunk{
			{Content: "Hello"},
			{Content: " there!"},
			{Done: true},
		}}
		broker := api.NewSessionBroker()
		mgr := session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(
			streamer, registry, disc, nil,
			api.WithSessionManager(mgr),
			api.WithSessionBroker(broker),
		)
		httpSrv := httptest.NewServer(srv.Handler())
		defer httpSrv.Close()

		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		// Step 1: subscribe to the SSE stream endpoint before POSTing so
		// broker.Publish finds a live subscriber to forward chunks to.
		streamCtx, cancelStream := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelStream()

		streamURL := httpSrv.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		streamReq, err := http.NewRequestWithContext(streamCtx, http.MethodGet, streamURL, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		streamRespCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(streamReq)
			if doErr == nil {
				streamRespCh <- resp
			}
		}()

		// Give the SSE subscriber time to register before we trigger the POST.
		time.Sleep(50 * time.Millisecond)

		// Step 2: POST the message. The handler runs broker.Publish synchronously
		// inside handleSessionMessage, blocking until AccumulateStream closes the
		// channel (i.e., until all chunks are drained and the reply is persisted).
		postURL := httpSrv.URL + "/api/v1/sessions/" + sess.ID + "/messages"
		postDone := make(chan struct{})
		go func() {
			defer close(postDone)
			postResp, postErr := http.Post(postURL, "application/json", strings.NewReader(`{"content":"hello"}`)) //nolint:noctx
			if postErr == nil {
				postResp.Body.Close()
			}
		}()

		// Step 3: collect SSE events from the stream connection.
		var streamResp *http.Response
		Eventually(streamRespCh, 3*time.Second).Should(Receive(&streamResp))
		defer streamResp.Body.Close()

		sseEventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(streamResp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "data: ") {
						evts = append(evts, strings.TrimPrefix(line, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			sseEventsCh <- evts
		}()

		var sseEvts []string
		Eventually(sseEventsCh, 4*time.Second).Should(Receive(&sseEvts))

		// Step 4: assert SSE content chunks and [DONE] arrived.
		Expect(sseEvts).To(ContainElement(ContainSubstring("Hello")),
			"first content chunk must appear in the SSE stream")
		Expect(sseEvts).To(ContainElement(ContainSubstring("there!")),
			"second content chunk must appear in the SSE stream")
		Expect(sseEvts).To(ContainElement("[DONE]"),
			"stream must terminate with [DONE]")

		// Wait for the POST goroutine to finish so the reply is persisted.
		Eventually(postDone, 3*time.Second).Should(BeClosed())

		// Step 5: GET /messages and assert the assistant reply is persisted.
		getResp, err := http.Get(httpSrv.URL + "/api/v1/sessions/" + sess.ID + "/messages") //nolint:noctx
		Expect(err).NotTo(HaveOccurred())
		defer getResp.Body.Close()
		Expect(getResp.StatusCode).To(Equal(http.StatusOK))

		var messages []session.Message
		Expect(json.NewDecoder(getResp.Body).Decode(&messages)).To(Succeed())

		var assistantContent string
		for _, m := range messages {
			if m.Role == "assistant" {
				assistantContent = m.Content
				break
			}
		}
		Expect(assistantContent).To(Equal("Hello there!"),
			"assistant reply must be persisted in session.Messages before GET /messages is served")
	})
})

var _ = Describe("PATCH /api/v1/sessions/{id}/agent JSON contract", func() {
	var (
		recorder *httptest.ResponseRecorder
		streamer *mockStreamer
		mgr      *session.Manager
		srv      *api.Server
		eng      *fakeDispatchEngine
		registry *agent.Registry
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		streamer = &mockStreamer{chunks: []provider.StreamChunk{{Content: "ok"}, {Done: true}}}
		mgr = session.NewManager(streamer)
		registry = agent.NewRegistry()
		// Register the target agent so the orchestrator's SwitchAgent
		// resolves the manifest and fans out engine.SetManifest. Pre-lift
		// the registry was unused on this route.
		registry.Register(&agent.Manifest{ID: "plan-writer", Name: "Plan Writer"})
		disc := discovery.NewAgentDiscovery(nil)
		eng = &fakeDispatchEngine{}
		srv = api.NewServer(
			streamer,
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
			api.WithDispatchEngine(eng),
		)
	})

	It("updates the session's current agent", func() {
		sess, err := mgr.CreateSession("agent-original")
		Expect(err).NotTo(HaveOccurred())

		body := `{"agentId":"plan-writer"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/agent", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		updated, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.CurrentAgentID).To(Equal("plan-writer"), "agent switch must persist on the session so SendMessage routes through the new agent")

		var out map[string]interface{}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		Expect(out).To(HaveKey("agentId"))
		Expect(out).NotTo(HaveKey("agent_id"))
	})

	It("fans out the agent switch to the engine via Orchestrator.SwitchAgent (post-lift)", func() {
		sess, err := mgr.CreateSession("agent-original")
		Expect(err).NotTo(HaveOccurred())

		body := `{"agentId":"plan-writer"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/agent", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		// Per ADR - Session Orchestrator for Surface Parity §"SwitchAgent",
		// the API agent route must fan out to engine.SetManifest alongside
		// the session-manager metadata update. Pre-lift only the manager
		// half ran; the engine kept the stale manifest until the next
		// stream call, breaking multi-turn web chat. Closes Audit Finding 3.
		Expect(eng.manifestSet).To(HaveLen(1),
			"engine.SetManifest must fire once per agent switch via the orchestrator fan-out")
		Expect(eng.manifestSet[0].ID).To(Equal("plan-writer"))
	})

	It("routes subsequent messages through the new agent", func() {
		sess, err := mgr.CreateSession("agent-original")
		Expect(err).NotTo(HaveOccurred())

		patchBody := `{"agentId":"plan-writer"}`
		patchReq := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/agent", strings.NewReader(patchBody))
		srv.Handler().ServeHTTP(httptest.NewRecorder(), patchReq)

		msgBody := `{"content":"hello"}`
		msgReq := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/messages", strings.NewReader(msgBody))
		srv.Handler().ServeHTTP(httptest.NewRecorder(), msgReq)

		Expect(streamer.capturedAgentID).To(Equal("plan-writer"), "the streamer must be invoked with the agent the user selected, not the original session agent")
	})

	It("returns 404 when the session does not exist", func() {
		body := `{"agentId":"plan-writer"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/nonexistent/agent", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
	})

	It("returns 400 when agentId is missing", func() {
		sess, err := mgr.CreateSession("agent-original")
		Expect(err).NotTo(HaveOccurred())

		body := `{"agentId":""}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/agent", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("PATCH /api/v1/sessions/{id}/model JSON contract", func() {
	var (
		recorder *httptest.ResponseRecorder
		streamer *mockStreamer
		mgr      *session.Manager
		srv      *api.Server
		eng      *fakeDispatchEngine
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		streamer = &mockStreamer{chunks: []provider.StreamChunk{{Done: true}}}
		mgr = session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		eng = &fakeDispatchEngine{}
		srv = api.NewServer(
			streamer,
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
			api.WithDispatchEngine(eng),
		)
	})

	It("fans out the model switch to the engine via Orchestrator.SwitchModel (post-lift)", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		body := `{"modelId":"claude-opus-4.7","providerId":"anthropic"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/model", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		// Per ADR - Session Orchestrator for Surface Parity §"SwitchModel",
		// the API model route must fan out to engine.SetModelPreference
		// alongside the session-manager metadata update. Pre-lift only the
		// manager half ran. Closes Audit Finding 3 (model half).
		Expect(eng.modelPrefProviders).To(Equal([]string{"anthropic"}),
			"engine.SetModelPreference must fire with the supplied provider id")
		Expect(eng.modelPrefModels).To(Equal([]string{"claude-opus-4.7"}),
			"engine.SetModelPreference must fire with the supplied model id")
	})

	It("updates the session's current model and provider and returns camelCase JSON", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		body := `{"modelId":"claude-opus-4.7","providerId":"anthropic"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/model", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		updated, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(updated.CurrentModelID).To(Equal("claude-opus-4.7"),
			"model switch must persist on the session so subsequent turns route through the new model")
		Expect(updated.CurrentProviderID).To(Equal("anthropic"),
			"provider switch must persist on the session so subsequent turns route through the new provider")

		var out map[string]interface{}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		Expect(out).To(HaveKeyWithValue("currentModelId", "claude-opus-4.7"))
		Expect(out).To(HaveKeyWithValue("currentProviderId", "anthropic"))
		Expect(out).NotTo(HaveKey("current_model_id"))
		Expect(out).NotTo(HaveKey("current_provider_id"))
	})

	It("returns 404 when the session does not exist", func() {
		body := `{"modelId":"claude-opus-4.7","providerId":"anthropic"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/nonexistent/model", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusNotFound))
	})

	It("returns 400 when modelId is missing", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		body := `{"modelId":"","providerId":"anthropic"}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/model", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
	})

	It("returns 400 when providerId is missing", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		body := `{"modelId":"claude-opus-4.7","providerId":""}`
		req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/model", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusBadRequest))
	})
})

var _ = Describe("GET /api/v1/models", func() {
	It("returns providers grouped from the injected ModelLister, sorted alphabetically by provider", func() {
		lister := func() ([]provider.Model, error) {
			return []provider.Model{
				{ID: "gpt-4o", Provider: "openai"},
				{ID: "claude-opus-4.7", Provider: "anthropic"},
				{ID: "gpt-4o-mini", Provider: "openai"},
				{ID: "claude-sonnet-4", Provider: "anthropic"},
			}, nil
		}
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(
			&mockStreamer{},
			registry,
			disc,
			nil,
			api.WithModelLister(lister),
		)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		var out struct {
			Providers []struct {
				ID     string `json:"id"`
				Models []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"models"`
			} `json:"providers"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Providers).To(HaveLen(2))
		Expect(out.Providers[0].ID).To(Equal("anthropic"),
			"providers must be sorted alphabetically so the Vue picker renders deterministically")
		Expect(out.Providers[1].ID).To(Equal("openai"))

		Expect(out.Providers[0].Models).To(HaveLen(2))
		anthropicIDs := []string{out.Providers[0].Models[0].ID, out.Providers[0].Models[1].ID}
		Expect(anthropicIDs).To(ConsistOf("claude-opus-4.7", "claude-sonnet-4"))

		Expect(out.Providers[1].Models).To(HaveLen(2))
		openaiIDs := []string{out.Providers[1].Models[0].ID, out.Providers[1].Models[1].ID}
		Expect(openaiIDs).To(ConsistOf("gpt-4o", "gpt-4o-mini"))

		Expect(out.Providers[0].Models[0].Name).NotTo(BeEmpty(),
			"each model must carry a non-empty display name so the Vue picker has something to render")
	})

	It("returns 501 when no ModelLister is configured", func() {
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(&mockStreamer{}, registry, disc, nil)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/models", nil)
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusNotImplemented),
			"absent a ModelLister the endpoint must signal not-implemented rather than 200 with an empty list")
	})
})

// Plans/Delegation Bus Bridge — Engine to SSE (May 2026) §"Test
// Strategy" §"API SSE seam". The /api/swarm/events endpoint
// subscribes to the three new delegation topics and projects each
// event variant into a `streaming.SwarmEvent` with
// `metadata.child_session_id` populated. These specs pin both the
// subscription wiring and the projection shape.
var _ = Describe("GET /api/swarm/events delegation projection", func() {
	var (
		bus *eventbus.EventBus
		srv *api.Server
		hs  *httptest.Server
	)

	BeforeEach(func() {
		bus = eventbus.NewEventBus()
		srv = api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
		hs = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		hs.Close()
	})

	publishAndDrain := func(publish func()) []map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/api/swarm/events", http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		// Give the handler time to subscribe before we publish.
		time.Sleep(80 * time.Millisecond)
		publish()

		var resp *http.Response
		Eventually(respCh, 2*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []map[string]any, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var collected []map[string]any
			for {
				line, readErr := reader.ReadString('\n')
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "data: ") {
					payload := strings.TrimPrefix(trimmed, "data: ")
					var parsed map[string]any
					if json.Unmarshal([]byte(payload), &parsed) == nil {
						collected = append(collected, parsed)
					}
				}
				if readErr != nil {
					break
				}
				if len(collected) >= 2 {
					// connected + delegation event
					break
				}
			}
			eventsCh <- collected
		}()

		var collected []map[string]any
		Eventually(eventsCh, 3*time.Second).Should(Receive(&collected))
		return collected
	}

	It("emits a delegation event with metadata.child_session_id when delegation.started fires on the bus", func() {
		collected := publishAndDrain(func() {
			bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
				ChainID:         "chain-sse-1",
				ParentSessionID: "parent-sse",
				ChildSessionID:  "child-sse",
				SourceAgent:     "orchestrator",
				TargetAgent:     "qa-agent",
				Description:     "test delegation",
			}))
		})

		var delegation map[string]any
		for _, ev := range collected {
			if ev["type"] == string(streaming.EventDelegation) {
				delegation = ev
				break
			}
		}
		Expect(delegation).NotTo(BeNil(),
			"the SSE stream must emit a delegation event when the bus publishes delegation.started")
		Expect(delegation["status"]).To(Equal("started"))
		Expect(delegation["agent_id"]).To(Equal("qa-agent"))
		Expect(delegation["id"]).To(Equal("chain-sse-1"))

		metadata, ok := delegation["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata["child_session_id"]).To(Equal("child-sse"),
			"child_session_id is the load-bearing field the Vue DelegationPanel.vue clicks through on")
		Expect(metadata["parent_session_id"]).To(Equal("parent-sse"))
		Expect(metadata["source_agent"]).To(Equal("orchestrator"))
	})

	It("emits a delegation event with status=completed when delegation.completed fires", func() {
		collected := publishAndDrain(func() {
			bus.Publish(events.EventDelegationCompleted, events.NewDelegationCompletedEvent(events.DelegationEventData{
				ChainID:         "chain-sse-c",
				ParentSessionID: "parent",
				ChildSessionID:  "child",
				SourceAgent:     "lead",
				TargetAgent:     "qa",
				ModelName:       "claude-3",
				ProviderName:    "anthropic",
				ToolCalls:       4,
				LastTool:        "bash",
			}))
		})

		var delegation map[string]any
		for _, ev := range collected {
			if ev["type"] == string(streaming.EventDelegation) {
				delegation = ev
				break
			}
		}
		Expect(delegation).NotTo(BeNil())
		Expect(delegation["status"]).To(Equal("completed"))
		metadata, ok := delegation["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata["child_session_id"]).To(Equal("child"))
		Expect(metadata["model_name"]).To(Equal("claude-3"))
		Expect(metadata["provider_name"]).To(Equal("anthropic"))
	})

	It("emits a delegation event with status=failed and the error message when delegation.failed fires", func() {
		collected := publishAndDrain(func() {
			bus.Publish(events.EventDelegationFailed, events.NewDelegationFailedEvent(events.DelegationEventData{
				ChainID:         "chain-sse-f",
				ParentSessionID: "parent",
				ChildSessionID:  "child",
				SourceAgent:     "lead",
				TargetAgent:     "qa",
				Error:           "stream initialisation failed",
			}))
		})

		var delegation map[string]any
		for _, ev := range collected {
			if ev["type"] == string(streaming.EventDelegation) {
				delegation = ev
				break
			}
		}
		Expect(delegation).NotTo(BeNil())
		Expect(delegation["status"]).To(Equal("failed"))
		metadata, ok := delegation["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata["error"]).To(Equal("stream initialisation failed"))
	})
})

// Plans/Tool Execute Bus Bridge — Engine to SSE (May 2026) §"Test
// Strategy" §"API SSE seam". The /api/swarm/events endpoint already
// subscribes to tool.execute.{before,result,error}; its projector
// must use the new InternalToolCallID as the SwarmEvent.ID, expose
// provider_tool_use_id and content in metadata, and stamp
// SchemaVersion. The legacy <sessionID>:<toolName> id-fabrication is
// preserved as a defensive fallback when the engine has not yet
// surfaced the internal id.
var _ = Describe("GET /api/swarm/events tool.execute.* projection", func() {
	var (
		bus *eventbus.EventBus
		srv *api.Server
		hs  *httptest.Server
	)

	BeforeEach(func() {
		bus = eventbus.NewEventBus()
		srv = api.NewServer(nil, nil, nil, nil, api.WithEventBus(bus))
		hs = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		hs.Close()
	})

	publishAndDrain := func(publish func()) []map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/api/swarm/events", http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(80 * time.Millisecond)
		publish()

		var resp *http.Response
		Eventually(respCh, 2*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []map[string]any, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var collected []map[string]any
			for {
				line, readErr := reader.ReadString('\n')
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "data: ") {
					payload := strings.TrimPrefix(trimmed, "data: ")
					var parsed map[string]any
					if json.Unmarshal([]byte(payload), &parsed) == nil {
						collected = append(collected, parsed)
					}
				}
				if readErr != nil {
					break
				}
				if len(collected) >= 2 {
					break
				}
			}
			eventsCh <- collected
		}()

		var collected []map[string]any
		Eventually(eventsCh, 3*time.Second).Should(Receive(&collected))
		return collected
	}

	It("uses InternalToolCallID as the SwarmEvent ID for tool.execute.before", func() {
		collected := publishAndDrain(func() {
			bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
				SessionID:          "sess-1",
				ToolName:           "bash",
				ToolCallID:         "toolu_01ABC",
				InternalToolCallID: "fs_internal_42",
			}))
		})

		var ev map[string]any
		for _, c := range collected {
			if c["type"] == string(streaming.EventToolCall) {
				ev = c
				break
			}
		}
		Expect(ev).NotTo(BeNil(),
			"the SSE stream must emit a tool_call event for tool.execute.before")
		Expect(ev["id"]).To(Equal("fs_internal_42"),
			"the SwarmEvent.ID is the InternalToolCallID — the failover-stable correlation id")
		Expect(ev["status"]).To(Equal("started"))
		Expect(ev["agent_id"]).To(Equal("sess-1"))
		Expect(ev["schema_version"]).NotTo(BeNil(),
			"every projected event must stamp SchemaVersion")

		metadata, ok := ev["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata["tool_name"]).To(Equal("bash"))
		Expect(metadata["provider_tool_use_id"]).To(Equal("toolu_01ABC"),
			"provider_tool_use_id preserves the upstream wire id for the audit trail")
	})

	It("uses InternalToolCallID and exposes content in metadata for tool.execute.result", func() {
		collected := publishAndDrain(func() {
			bus.Publish(events.EventToolExecuteResult, events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
				SessionID:          "sess-1",
				ToolName:           "bash",
				Result:             "file contents",
				ToolCallID:         "toolu_01OK",
				InternalToolCallID: "fs_internal_ok",
			}))
		})

		var ev map[string]any
		for _, c := range collected {
			if c["type"] == string(streaming.EventToolResult) {
				ev = c
				break
			}
		}
		Expect(ev).NotTo(BeNil())
		Expect(ev["id"]).To(Equal("fs_internal_ok"))
		Expect(ev["status"]).To(Equal("completed"))
		metadata, ok := ev["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata["tool_name"]).To(Equal("bash"))
		Expect(metadata["provider_tool_use_id"]).To(Equal("toolu_01OK"))
		Expect(metadata["content"]).To(Equal("file contents"),
			"the result body must surface as metadata.content for the activity pane")
	})

	It("uses InternalToolCallID and exposes the error message for tool.execute.error", func() {
		collected := publishAndDrain(func() {
			bus.Publish(events.EventToolExecuteError, events.NewToolExecuteErrorEvent(events.ToolExecuteErrorEventData{
				SessionID:          "sess-1",
				ToolName:           "bash",
				Error:              errors.New("exit 1"),
				ToolCallID:         "toolu_01ERR",
				InternalToolCallID: "fs_internal_err",
			}))
		})

		var ev map[string]any
		for _, c := range collected {
			if c["type"] == string(streaming.EventToolResult) {
				ev = c
				break
			}
		}
		Expect(ev).NotTo(BeNil())
		Expect(ev["id"]).To(Equal("fs_internal_err"))
		Expect(ev["status"]).To(Equal("error"))
		metadata, ok := ev["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		Expect(metadata["tool_name"]).To(Equal("bash"))
		Expect(metadata["provider_tool_use_id"]).To(Equal("toolu_01ERR"))
		Expect(metadata["error"]).To(Equal("exit 1"))
	})

	It("falls back to <sessionID>:<toolName> when InternalToolCallID is empty (defensive)", func() {
		// Defensive path: in normal operation post-bridge the engine always
		// stamps the internal id. The fallback prevents a regression in any
		// code path that does not yet route through executeToolCall from
		// manifesting as a silent SSE drop (the projector keys on a
		// non-empty id).
		collected := publishAndDrain(func() {
			bus.Publish(events.EventToolExecuteBefore, events.NewToolEvent(events.ToolEventData{
				SessionID: "sess-fallback",
				ToolName:  "bash",
			}))
		})

		var ev map[string]any
		for _, c := range collected {
			if c["type"] == string(streaming.EventToolCall) {
				ev = c
				break
			}
		}
		Expect(ev).NotTo(BeNil())
		Expect(ev["id"]).To(Equal("sess-fallback:bash"),
			"absent the internal id, the legacy fabricated form ensures the SSE projector still emits an event")
	})
})

var _ = Describe("POST /api/v1/sessions seeds default model from agent manifest", func() {
	// Regression cover for the May 2026 chip-not-rendering bug: a brand-new
	// session was returned with empty currentModelId / currentProviderId
	// because handleCreateSession called CreateSession (no defaults) and
	// nothing else populated the pair until either the user picked a model
	// or a provider_changed transition fired. The chip's v-if fell through
	// and non-technical users had no way to confirm which model they were
	// using.
	//
	// The fix: handleCreateSession looks up the agent's manifest and seeds
	// the new session with the first PreferredModels entry. These specs pin
	// the contract so a future refactor can't silently undo it.

	var (
		recorder *httptest.ResponseRecorder
		mgr      *session.Manager
		registry *agent.Registry
		srv      *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Content: "ok", Done: true}}})
		registry = agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
	})

	postCreate := func(agentID string) map[string]interface{} {
		body := `{"agent_id":"` + agentID + `"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))
		var out map[string]interface{}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		return out
	}

	It("populates currentModelId and currentProviderId from the agent manifest's first preferred model", func() {
		registry.Register(&agent.Manifest{
			ID:   "team-lead",
			Name: "Team Lead",
			PreferredModels: []agent.ModelPreference{
				{Provider: "anthropic", Model: "claude-sonnet-4-6"},
				{Provider: "zai", Model: "glm-4.6"},
			},
		})

		out := postCreate("team-lead")

		Expect(out).To(HaveKeyWithValue("currentModelId", "claude-sonnet-4-6"),
			"new session must carry the agent's first preferred model so the chip renders immediately")
		Expect(out).To(HaveKeyWithValue("currentProviderId", "anthropic"),
			"new session must carry the matching provider for the chip's `<model> · <provider>` format")
	})

	It("omits currentModelId and currentProviderId when the agent manifest declares no preferred models", func() {
		registry.Register(&agent.Manifest{
			ID:              "barebones-agent",
			Name:            "Barebones",
			PreferredModels: nil,
		})

		out := postCreate("barebones-agent")

		Expect(out).NotTo(HaveKey("currentModelId"),
			"omitempty: a manifest with no preferred models must not synthesise a fake default")
		Expect(out).NotTo(HaveKey("currentProviderId"))
	})

	It("omits currentModelId and currentProviderId when the agent is unknown to the registry", func() {
		// No Register call: the registry has no manifest for "ghost-agent".
		// The session is still created (CreateSession does not validate the
		// agent id), but defaults degrade silently.
		out := postCreate("ghost-agent")

		Expect(out).NotTo(HaveKey("currentModelId"))
		Expect(out).NotTo(HaveKey("currentProviderId"))
		// And the session itself must still be present in the manager so the
		// degraded path doesn't leak a 500.
		Expect(out).To(HaveKey("id"))
	})
})

var _ = Describe("POST /api/v1/sessions persists default model+provider to the manager", func() {
	// Companion to the wire-shape spec above: the seed must land on the
	// session manager's in-memory state too, so the very first GET
	// /api/v1/sessions/{id}/messages or PATCH /agent reads the same
	// pair the create response advertised.

	It("writes CurrentProviderID and CurrentModelID onto the session in the manager", func() {
		mgr := session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{}})
		registry := agent.NewRegistry()
		registry.Register(&agent.Manifest{
			ID: "code-reviewer",
			PreferredModels: []agent.ModelPreference{
				{Provider: "openai", Model: "gpt-4o"},
			},
		})
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)

		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"agent_id":"code-reviewer"}`))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		var out map[string]interface{}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		sessID, _ := out["id"].(string)
		Expect(sessID).NotTo(BeEmpty())

		stored, err := mgr.GetSession(sessID)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored.CurrentProviderID).To(Equal("openai"))
		Expect(stored.CurrentModelID).To(Equal("gpt-4o"))
	})
})

// Web Swarm Mention Parity (May 2026) — the Vue web chat needs the
// same @swarm trigger the TUI has had since the Multi-Agent Chat UX
// work. Two seams under test here:
//
//  1. GET /api/swarms — read-side endpoint feeding the web @-picker.
//     Mirrors GET /api/agents in shape (JSON array of registered
//     manifests in stable id order). The web client maps each entry
//     into a FuzzySearchItem with `meta` taken from the lead.
//  2. POST /api/chat — when a registered swarm id appears as @<id>
//     in the message body, the orchestrator MUST resolve the mention
//     to a swarm dispatch identical to the TUI's chat path. Parity
//     with the TUI is non-negotiable: no opt-in field on the request
//     body — ScanMentions runs unconditionally.
var _ = Describe("Web Swarm Mention Parity", func() {
	var (
		recorder *httptest.ResponseRecorder
		registry *agent.Registry
		streamer *mockStreamer
		swarmReg *swarm.Registry
		engStub  *fakeDispatchEngine
		server   *api.Server
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		registry = agent.NewRegistry()
		registry.Register(&agent.Manifest{ID: "lead-one", Name: "Lead One"})
		registry.Register(&agent.Manifest{ID: "lead-two", Name: "Lead Two"})
		registry.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})

		swarmReg = swarm.NewRegistry()
		swarmReg.Register(&swarm.Manifest{
			SchemaVersion: "1.0.0",
			ID:            "alpha-team",
			Description:   "Alpha analysis swarm",
			Lead:          "lead-one",
			Members:       []string{"lead-two"},
		})
		swarmReg.Register(&swarm.Manifest{
			SchemaVersion: "1.0.0",
			ID:            "beta-team",
			Description:   "Beta codegen swarm",
			Lead:          "lead-two",
			Members:       []string{},
		})

		streamer = &mockStreamer{
			chunks: []provider.StreamChunk{
				{Content: "ack"},
				{Content: "", Done: true},
			},
		}
		engStub = &fakeDispatchEngine{}
		server = api.NewServer(streamer, registry, nil, nil,
			api.WithSwarmRegistry(swarmReg),
			api.WithDispatchEngine(engStub),
		)
	})

	Describe("GET /api/swarms", func() {
		It("returns the registered swarms as a JSON array", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/swarms", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

			var swarms []map[string]any
			Expect(json.Unmarshal(recorder.Body.Bytes(), &swarms)).To(Succeed())
			Expect(swarms).To(HaveLen(2))
		})

		It("publishes a stable JSON shape: id, description, lead, members", func() {
			req := httptest.NewRequest(http.MethodGet, "/api/swarms", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))

			var swarms []map[string]any
			Expect(json.Unmarshal(recorder.Body.Bytes(), &swarms)).To(Succeed())
			Expect(swarms).To(HaveLen(2))

			// Sorted alphabetically by id (matches Registry.List() contract).
			Expect(swarms[0]).To(HaveKeyWithValue("id", "alpha-team"))
			Expect(swarms[0]).To(HaveKeyWithValue("description", "Alpha analysis swarm"))
			Expect(swarms[0]).To(HaveKeyWithValue("lead", "lead-one"))
			members, ok := swarms[0]["members"].([]any)
			Expect(ok).To(BeTrue(), "members must serialise as a JSON array, not null")
			Expect(members).To(ConsistOf("lead-two"))

			// Empty Members must round-trip as [], not null — the web
			// client renders this list directly and will iterate it.
			Expect(swarms[1]).To(HaveKeyWithValue("id", "beta-team"))
			Expect(swarms[1]).To(HaveKeyWithValue("lead", "lead-two"))
			betaMembers, ok := swarms[1]["members"].([]any)
			Expect(ok).To(BeTrue(),
				"empty roster must serialise as [], so the picker can iterate without a nil guard")
			Expect(betaMembers).To(BeEmpty())
		})

		Context("when the swarm registry is not configured", func() {
			It("returns an empty JSON array", func() {
				bareServer := api.NewServer(streamer, registry, nil, nil)

				req := httptest.NewRequest(http.MethodGet, "/api/swarms", http.NoBody)
				bareServer.Handler().ServeHTTP(recorder, req)

				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Header().Get("Content-Type")).To(Equal("application/json"))

				body := strings.TrimSpace(recorder.Body.String())
				Expect(body).To(Equal("[]"),
					"a missing swarm registry must surface as [], not null — matches GET /api/agents")
			})
		})
	})

	Describe("POST /api/chat with @<swarm-id> in the body", func() {
		It("dispatches the swarm even though agent_id names a plain agent (TUI parity)", func() {
			// Web user types `@alpha-team please look` while the
			// MessageInput's bound agent_id is whatever the toolbar
			// AgentPicker had selected. Without ScanMentions the server
			// would stream from default-assistant and ignore the
			// mention; with parity wiring the mention overrides the
			// default and the streamer drives the swarm's lead.
			body := `{"agent_id":"default-assistant","message":"@alpha-team please look at the auth path"}`
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(streamer.capturedAgentID).To(Equal("lead-one"),
				"the @swarm mention must override agent_id, dispatching from the swarm's lead")
			Expect(engStub.installedContext).NotTo(BeNil(),
				"a swarm context must be installed on the engine when the mention resolves")
			Expect(engStub.installedContext.SwarmID).To(Equal("alpha-team"))
			Expect(engStub.installedContext.LeadAgent).To(Equal("lead-one"))
		})

		It("falls through to agent_id when no @-mention resolves to a swarm", func() {
			body := `{"agent_id":"default-assistant","message":"plain message no mention"}`
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(streamer.capturedAgentID).To(Equal("default-assistant"),
				"absent a swarm @-mention the default agent_id still drives the stream")
			Expect(engStub.installedContext).To(BeNil(),
				"no swarm context when the message contains no swarm mention")
		})

		It("ignores @<agent-id> mentions and stays with agent_id (agent mentions don't redirect)", func() {
			body := `{"agent_id":"default-assistant","message":"ask @lead-one to look"}`
			req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(streamer.capturedAgentID).To(Equal("default-assistant"),
				"agent @-mentions don't redirect — only swarm mentions do (orchestrator parity contract)")
			Expect(engStub.installedContext).To(BeNil())
		})
	})
})

// fakeContextUsageProvider records the (provider, model, messages) the
// api server hands it and returns a deterministic JSON payload so tests
// pin the wire shape and the call site without standing up the engine.
type fakeContextUsageProvider struct {
	mu            sync.Mutex
	calls         int
	lastProvider  string
	lastModel     string
	lastMsgCount  int
	hasUsage      bool
	staticPayload string
}

func (f *fakeContextUsageProvider) ContextUsageJSONForSession(providerID, modelID string, messages []provider.Message) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastProvider = providerID
	f.lastModel = modelID
	f.lastMsgCount = len(messages)
	if !f.hasUsage {
		return "", false
	}
	return f.staticPayload, true
}

// Phase 3 — TUI-cadence parity. The chip must reflect the *current*
// usage at all times: on session-load (SSE-connect), after each
// completed turn (engine emission), and on agent/model switch
// (PATCH response carries the figure).
var _ = Describe("Phase 3 — context_usage cadence parity", func() {
	Describe("GET /api/v1/sessions/{id}/stream emits a context_usage SSE event on connect", func() {
		var (
			broker     *api.SessionBroker
			mgr        *session.Manager
			srv        *api.Server
			httpServer *httptest.Server
			usage      *fakeContextUsageProvider
		)

		BeforeEach(func() {
			broker = api.NewSessionBroker()
			mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Done: true}}})
			usage = &fakeContextUsageProvider{
				hasUsage: true,
				staticPayload: `{"input_tokens":1234,"output_reserve":4096,"limit":100000,` +
					`"percentage":1,"provider":"zai","model":"glm-4.6"}`,
			}
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			srv = api.NewServer(
				&mockStreamer{chunks: []provider.StreamChunk{}},
				registry,
				disc,
				nil,
				api.WithSessionManager(mgr),
				api.WithSessionBroker(broker),
				api.WithContextUsageProvider(usage),
			)
			httpServer = httptest.NewServer(srv.Handler())
		})

		AfterEach(func() {
			httpServer.Close()
		})

		It("writes a typed context_usage SSE event before any broker chunk on session-load", func() {
			// Session with no live publisher — the SSE handler still
			// emits a context_usage event upfront so the chip
			// hydrates the moment the user opens the session,
			// matching the TUI's StatusBar which reads
			// LastContextResult on every redraw.
			sessID := "phase3-session-load"
			mgr.RestoreSessions([]*session.Session{
				{
					ID:                sessID,
					AgentID:           "test-agent",
					CurrentProviderID: "zai",
					CurrentModelID:    "glm-4.6",
					Messages: []session.Message{
						{ID: "m1", Role: "assistant", Content: "previous reply"},
					},
				},
			})

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			url := httpServer.URL + "/api/v1/sessions/" + sessID + "/stream"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			Expect(err).NotTo(HaveOccurred())

			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			eventsCh := make(chan []string, 1)
			go func() {
				reader := bufio.NewReader(resp.Body)
				var evts []string
				for {
					line, readErr := reader.ReadString('\n')
					if line != "" {
						trimmed := strings.TrimSpace(line)
						if strings.HasPrefix(trimmed, "data: ") {
							evts = append(evts, strings.TrimPrefix(trimmed, "data: "))
						}
						if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
							break
						}
					}
					if readErr != nil {
						break
					}
				}
				eventsCh <- evts
			}()

			var evts []string
			Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

			Expect(evts).NotTo(BeEmpty(), "SSE handler must emit at least one event on session-load")
			Expect(evts).To(ContainElement(ContainSubstring(`"type":"context_usage"`)),
				"SSE handler must emit a context_usage event on connect so the chip hydrates immediately")
			Expect(evts).To(ContainElement(ContainSubstring(`"limit":100000`)),
				"context_usage payload must round-trip onto the wire")

			Expect(usage.calls).To(BeNumerically(">=", 1),
				"the SSE handler must call the context-usage provider with the session's current state")
			Expect(usage.lastProvider).To(Equal("zai"))
			Expect(usage.lastModel).To(Equal("glm-4.6"))
			Expect(usage.lastMsgCount).To(BeNumerically(">=", 1),
				"the helper must receive the session's current messages so the input-token estimate reflects accumulated history")
		})

		It("falls back gracefully when the context-usage provider has nothing to compute", func() {
			usage.hasUsage = false

			// Sealed session: last message is non-user. The handler
			// uses SubscribeIfPublishing which fast-paths to [DONE]
			// when no broker run is in flight, giving the test a
			// deterministic terminator without a publisher.
			sessID := "phase3-no-usage"
			mgr.RestoreSessions([]*session.Session{
				{
					ID:      sessID,
					AgentID: "test-agent",
					Messages: []session.Message{
						{ID: "m1", Role: "assistant", Content: "previous reply"},
					},
				},
			})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			url := httpServer.URL + "/api/v1/sessions/" + sessID + "/stream"
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
			Expect(err).NotTo(HaveOccurred())

			resp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()

			eventsCh := make(chan []string, 1)
			go func() {
				reader := bufio.NewReader(resp.Body)
				var evts []string
				for {
					line, readErr := reader.ReadString('\n')
					if line != "" {
						trimmed := strings.TrimSpace(line)
						if strings.HasPrefix(trimmed, "data: ") {
							evts = append(evts, strings.TrimPrefix(trimmed, "data: "))
						}
						if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
							break
						}
					}
					if readErr != nil {
						break
					}
				}
				eventsCh <- evts
			}()

			var evts []string
			Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

			for _, e := range evts {
				Expect(e).NotTo(ContainSubstring(`"type":"context_usage"`),
					"no usage event when provider can't compute — chip stays on prior state")
			}
		})
	})

	Describe("PATCH /api/v1/sessions/{id}/agent includes contextUsage in response", func() {
		var (
			recorder *httptest.ResponseRecorder
			mgr      *session.Manager
			srv      *api.Server
			usage    *fakeContextUsageProvider
		)

		BeforeEach(func() {
			recorder = httptest.NewRecorder()
			mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Done: true}}})
			usage = &fakeContextUsageProvider{
				hasUsage: true,
				staticPayload: `{"input_tokens":2222,"output_reserve":4096,"limit":200000,` +
					`"percentage":1,"provider":"anthropic","model":"claude-sonnet-4-6"}`,
			}
			registry := agent.NewRegistry()
			// Post-lift the agent route routes through Orchestrator.SwitchAgent
			// which resolves agentId against the registry before SetManifest /
			// UpdateSessionAgent. Pre-lift the registry was unused on this
			// route; register the target so the test's intent (verify the
			// contextUsage is annotated on the response) still holds.
			registry.Register(&agent.Manifest{ID: "plan-writer", Name: "Plan Writer"})
			disc := discovery.NewAgentDiscovery(nil)
			srv = api.NewServer(
				&mockStreamer{chunks: []provider.StreamChunk{}},
				registry,
				disc,
				nil,
				api.WithSessionManager(mgr),
				api.WithContextUsageProvider(usage),
			)
		})

		It("returns the new context_usage shape in the JSON response so the chip updates on switch", func() {
			sess, err := mgr.CreateSession("agent-original")
			Expect(err).NotTo(HaveOccurred())

			body := `{"agentId":"plan-writer"}`
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/agent", strings.NewReader(body))
			srv.Handler().ServeHTTP(recorder, req)
			Expect(recorder.Code).To(Equal(http.StatusOK))

			var out map[string]interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
			Expect(out).To(HaveKey("contextUsage"),
				"agent-switch response must carry the fresh context_usage shape so the chip updates without waiting for the next turn")

			cu, ok := out["contextUsage"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "contextUsage must be a JSON object, not a raw string")
			Expect(cu).To(HaveKey("input_tokens"))
			Expect(cu).To(HaveKey("limit"))
			Expect(cu).To(HaveKeyWithValue("limit", float64(200000)))
		})
	})

	Describe("PATCH /api/v1/sessions/{id}/model includes contextUsage in response", func() {
		var (
			recorder *httptest.ResponseRecorder
			mgr      *session.Manager
			srv      *api.Server
			usage    *fakeContextUsageProvider
		)

		BeforeEach(func() {
			recorder = httptest.NewRecorder()
			mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Done: true}}})
			usage = &fakeContextUsageProvider{
				hasUsage: true,
				staticPayload: `{"input_tokens":1500,"output_reserve":4096,"limit":100000,` +
					`"percentage":1,"provider":"zai","model":"glm-4.6"}`,
			}
			registry := agent.NewRegistry()
			disc := discovery.NewAgentDiscovery(nil)
			srv = api.NewServer(
				&mockStreamer{chunks: []provider.StreamChunk{}},
				registry,
				disc,
				nil,
				api.WithSessionManager(mgr),
				api.WithContextUsageProvider(usage),
			)
		})

		It("returns the new context_usage shape in the JSON response on model switch", func() {
			sess, err := mgr.CreateSession("agent-a")
			Expect(err).NotTo(HaveOccurred())

			body := `{"modelId":"glm-4.6","providerId":"zai"}`
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/sessions/"+sess.ID+"/model", strings.NewReader(body))
			srv.Handler().ServeHTTP(recorder, req)
			Expect(recorder.Code).To(Equal(http.StatusOK))

			var out map[string]interface{}
			Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
			Expect(out).To(HaveKey("contextUsage"),
				"model-switch response must carry the fresh context_usage shape so the chip pivots to the new limit immediately")

			Expect(usage.lastProvider).To(Equal("zai"),
				"the helper must receive the new provider id so the figure reflects the post-switch state")
			Expect(usage.lastModel).To(Equal("glm-4.6"),
				"the helper must receive the new model id so the figure reflects the post-switch state")
		})
	})
})

// Slice 6a — SSE bridge for EventContextCompacted.
//
// The engine's L2 auto-compactor publishes EventContextCompacted on
// the engine bus when compaction succeeds. Slice 6a bridges that bus
// event onto the SSE wire so Vue clients can render a compaction
// affordance (Slice 6b consumes this on the chip). The bridge
// preserves the existing SSE writer pattern: the engine emits
// untyped JSON bodies, the SSE writer injects the
// `"type":"context_compacted"` discriminant, and the frontend's
// discriminated union routes on that field.
var _ = Describe("Slice 6a — SSE bridge for EventContextCompacted", func() {
	var (
		broker     *api.SessionBroker
		mgr        *session.Manager
		bus        *eventbus.EventBus
		srv        *api.Server
		httpServer *httptest.Server
	)

	BeforeEach(func() {
		broker = api.NewSessionBroker()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Done: true}}})
		bus = eventbus.NewEventBus()
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
			api.WithSessionBroker(broker),
			api.WithEventBus(bus),
		)
		httpServer = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		httpServer.Close()
	})

	It("forwards EventContextCompacted onto the SSE wire as a typed context_compacted event", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		// Give the SSE handler a chance to subscribe to the bus
		// before we publish the event. Without this race window
		// the publish can land before any subscriber is wired and
		// the event is silently dropped.
		time.Sleep(50 * time.Millisecond)

		bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
			SessionID:      sess.ID,
			AgentID:        "Tech-Lead",
			OriginalTokens: 50_000,
			SummaryTokens:  5_000,
			LatencyMS:      420,
		}))

		// Close the broker channel to terminate the SSE stream after
		// the bus event has had a chance to flush. A no-content
		// stream chunk pinned by Done lets the existing dispatcher
		// emit [DONE] without polluting the wire with content.
		source := make(chan provider.StreamChunk, 1)
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "data: ") {
						evts = append(evts, strings.TrimPrefix(trimmed, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		Expect(evts).To(ContainElement(ContainSubstring(`"type":"context_compacted"`)),
			"SSE handler must emit a context_compacted event when EventContextCompacted fires for this session")
		Expect(evts).To(ContainElement(ContainSubstring(`"original_tokens":50000`)),
			"original_tokens must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"summary_tokens":5000`)),
			"summary_tokens must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"latency_ms":420`)),
			"latency_ms must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"agent_id":"Tech-Lead"`)),
			"agent_id must round-trip onto the wire so the chip can attribute the compaction")
		Expect(evts).To(ContainElement(ContainSubstring(`"session_id":"`+sess.ID+`"`)),
			"session_id must round-trip onto the wire so the frontend can correlate state")

		// MUST NOT leak the JSON payload as a plain content chunk —
		// otherwise the assistant bubble would render the raw JSON.
		for _, e := range evts {
			if strings.Contains(e, `"original_tokens":50000`) && !strings.Contains(e, `"type":"context_compacted"`) {
				Fail("context_compacted payload leaked into a plain content chunk: " + e)
			}
		}
	})

	It("does not forward EventContextCompacted from a different session", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		// Different session id — MUST NOT reach this subscriber.
		bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
			SessionID:      "sess-other",
			AgentID:        "Tech-Lead",
			OriginalTokens: 50_000,
			SummaryTokens:  5_000,
			LatencyMS:      420,
		}))

		// Terminate the stream.
		source := make(chan provider.StreamChunk, 1)
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "data: ") {
						evts = append(evts, strings.TrimPrefix(trimmed, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		for _, e := range evts {
			Expect(e).NotTo(ContainSubstring(`"type":"context_compacted"`),
				"compaction events from a different session must not reach this SSE subscriber")
		}
	})
})

// Plans/Gate Bus Bridge — Engine to SSE and TUI (May 2026):
// the engine publishes gate.failed when runSwarmGates /
// dispatchMemberGates halts. The api-side bridge in
// internal/api/event_bridge.go routes the bus payload onto the
// SSE wire via a typed writer that injects the canonical
// `"type":"gate_failed"` discriminant. The Vue surface consumes
// this on a discriminated-union branch and renders a persistent
// banner (companion plan: Vue surface in C4).
var _ = Describe("Gate Bus Bridge — SSE bridge for EventGateFailed", func() {
	var (
		broker     *api.SessionBroker
		mgr        *session.Manager
		bus        *eventbus.EventBus
		srv        *api.Server
		httpServer *httptest.Server
	)

	BeforeEach(func() {
		broker = api.NewSessionBroker()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Done: true}}})
		bus = eventbus.NewEventBus()
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
			api.WithSessionBroker(broker),
			api.WithEventBus(bus),
		)
		httpServer = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		httpServer.Close()
	})

	It("forwards EventGateFailed onto the SSE wire as a typed gate_failed event", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		// Race window: give the SSE handler a chance to subscribe to
		// the bus before publishing. Same pattern as the slice-6a SSE
		// bridge test for context.compacted above.
		time.Sleep(50 * time.Millisecond)

		bus.Publish(events.EventGateFailed, events.NewGateFailedEvent(events.GateEventData{
			SwarmID:        "a-team",
			SessionID:      sess.ID,
			Lifecycle:      "post-member",
			MemberID:       "researcher",
			GateName:       "post-member-researcher-relevance-gate",
			GateKind:       "ext:relevance-gate",
			Reason:         "off-topic",
			Cause:          "score 0.31 < threshold 0.5",
			CoordStoreKeys: []string{"chain/researcher/output", "chain/topic/spec"},
		}))

		// Terminate the stream so the SSE response closes cleanly.
		source := make(chan provider.StreamChunk, 1)
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "data: ") {
						evts = append(evts, strings.TrimPrefix(trimmed, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		Expect(evts).To(ContainElement(ContainSubstring(`"type":"gate_failed"`)),
			"SSE handler must emit a gate_failed event when EventGateFailed fires for this session")
		Expect(evts).To(ContainElement(ContainSubstring(`"swarm_id":"a-team"`)),
			"swarm_id must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"lifecycle":"post-member"`)),
			"lifecycle must round-trip onto the wire so the banner can attribute the halt")
		Expect(evts).To(ContainElement(ContainSubstring(`"member_id":"researcher"`)),
			"member_id must round-trip onto the wire")
		Expect(evts).To(ContainElement(ContainSubstring(`"gate_name":"post-member-researcher-relevance-gate"`)),
			"gate_name must round-trip onto the wire so the banner title can name the failing gate")
		Expect(evts).To(ContainElement(ContainSubstring(`"gate_kind":"ext:relevance-gate"`)))
		Expect(evts).To(ContainElement(ContainSubstring(`"reason":"off-topic"`)))
		// Go's json.Marshal escapes `<` `>` `&` to their unicode escape forms
		// by default for HTML-safety; the wire carries `<` rather than
		// the raw `<`. The Vue parser unescapes transparently because
		// JSON.parse does the right thing.
		Expect(evts).To(ContainElement(ContainSubstring("\"cause\":\"score 0.31 \\u003c threshold 0.5\"")))
		Expect(evts).To(ContainElement(ContainSubstring(`"coord_store_keys":["chain/researcher/output","chain/topic/spec"]`)))

		// Defence: gate_failed payload must never leak as a plain
		// content chunk — otherwise the assistant bubble would render
		// the raw JSON in the chat surface.
		for _, e := range evts {
			if strings.Contains(e, `"swarm_id":"a-team"`) && !strings.Contains(e, `"type":"gate_failed"`) {
				Fail("gate_failed payload leaked into a plain content chunk: " + e)
			}
		}
	})

	It("does NOT forward EventGateEvaluating onto the SSE wire (pass-event policy: failures only)", func() {
		sess, err := mgr.CreateSession("test-agent")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		url := httpServer.URL + "/api/v1/sessions/" + sess.ID + "/stream"
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		respCh := make(chan *http.Response, 1)
		go func() {
			resp, doErr := http.DefaultClient.Do(req)
			if doErr == nil {
				respCh <- resp
			}
		}()

		time.Sleep(50 * time.Millisecond)

		bus.Publish(events.EventGateEvaluating, events.NewGateEvaluatingEvent(events.GateEventData{
			SwarmID:   "a-team",
			SessionID: sess.ID,
			Lifecycle: "pre",
			GateCount: 3,
		}))

		source := make(chan provider.StreamChunk, 1)
		source <- provider.StreamChunk{Done: true}
		close(source)
		go broker.Publish(sess.ID, source)

		var resp *http.Response
		Eventually(respCh, 3*time.Second).Should(Receive(&resp))
		defer resp.Body.Close()

		eventsCh := make(chan []string, 1)
		go func() {
			reader := bufio.NewReader(resp.Body)
			var evts []string
			for {
				line, readErr := reader.ReadString('\n')
				if line != "" {
					trimmed := strings.TrimSpace(line)
					if strings.HasPrefix(trimmed, "data: ") {
						evts = append(evts, strings.TrimPrefix(trimmed, "data: "))
					}
					if len(evts) > 0 && evts[len(evts)-1] == "[DONE]" {
						break
					}
				}
				if readErr != nil {
					break
				}
			}
			eventsCh <- evts
		}()

		var evts []string
		Eventually(eventsCh, 4*time.Second).Should(Receive(&evts))

		for _, e := range evts {
			Expect(e).NotTo(ContainSubstring(`"type":"gate_evaluating"`),
				"web SSE wire must not carry gate_evaluating events; pass-event policy is gate_failed-only")
			Expect(e).NotTo(ContainSubstring(`"type":"gate_passed"`),
				"web SSE wire must not carry gate_passed events; pass-event policy is gate_failed-only")
		}
	})
})
