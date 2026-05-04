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
	capturedAgentID string
	capturedMessage string
}

func (m *mockStreamer) Stream(_ context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	m.capturedAgentID = agentID
	m.capturedMessage = message
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
				Expect(events).To(ContainElement(ContainSubstring(`"error":"stream failed"`)))
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
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		streamer = &mockStreamer{chunks: []provider.StreamChunk{{Content: "ok"}, {Done: true}}}
		mgr = session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			streamer,
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
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
	)

	BeforeEach(func() {
		recorder = httptest.NewRecorder()
		streamer = &mockStreamer{chunks: []provider.StreamChunk{{Done: true}}}
		mgr = session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			streamer,
			registry,
			disc,
			nil,
			api.WithSessionManager(mgr),
		)
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
