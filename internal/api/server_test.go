package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/api"
	"github.com/baphled/flowstate/internal/discovery"
	"github.com/baphled/flowstate/internal/dispatch"
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
	"github.com/baphled/flowstate/internal/turn"

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

// dripStreamer mimics the real engine streamer: it returns the chunks
// channel IMMEDIATELY (before all chunks have been emitted), drips chunks
// over time, and honours ctx.Done() — on cancellation it emits
// `{Error: ctx.Err(), Done: true}` and exits without emitting the remaining
// content chunks. This matches the engine's behaviour at engine.go:3774-3776
// where the select on `<-ctx.Done()` produces the regression observed in
// commit e4bf9632.
//
// mockStreamer's pre-filled+closed channel cannot exercise the
// streamer-vs-request-lifetime contract because the channel is already
// drained-and-closed by the time Stream returns. dripStreamer keeps the
// channel live across the handler return so the context propagation can be
// observed.
type dripStreamer struct {
	chunks       []provider.StreamChunk
	emitInterval time.Duration
}

func (d *dripStreamer) Stream(ctx context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	out := make(chan provider.StreamChunk, 1)
	go func() {
		defer close(out)
		for _, c := range d.chunks {
			select {
			case <-ctx.Done():
				// Match the engine: emit Done{Error: ctx.Err()} on
				// cancellation. See internal/engine/engine.go:3774-3776
				// for the canonical shape.
				select {
				case out <- provider.StreamChunk{Error: ctx.Err(), Done: true}:
				default:
				}
				return
			case <-time.After(d.emitInterval):
			}
			select {
			case out <- c:
			case <-ctx.Done():
				select {
				case out <- provider.StreamChunk{Error: ctx.Err(), Done: true}:
				default:
				}
				return
			}
		}
	}()
	return out, nil
}

// spyDispatcher records every DispatchEphemeral invocation and replays
// the supplied content chunk through the caller's consumer so the
// Phase 1 wire-shape pin spec can observe the full SSE byte sequence
// without spinning up a real engine + streamer chain.
//
// Per the "Dispatcher Service Unification (May 2026)" plan §"Phase 1",
// the API handler routes through the Dispatcher seam; this spy is the
// canonical fixture for asserting "handleChat called DispatchEphemeral
// with the expected request shape".
type spyDispatcher struct {
	mu            sync.Mutex
	requests      []dispatch.DispatchRequest
	replayContent string
}

func (sp *spyDispatcher) DispatchEphemeral(_ context.Context, req dispatch.DispatchRequest, consumer streaming.StreamConsumer) (dispatch.EphemeralHandle, error) {
	sp.mu.Lock()
	sp.requests = append(sp.requests, req)
	content := sp.replayContent
	sp.mu.Unlock()

	done := make(chan error, 1)
	go func() {
		defer close(done)
		if content != "" {
			_ = consumer.WriteChunk(content)
		}
		consumer.Done()
		done <- nil
	}()
	return dispatch.EphemeralHandle{Done: done}, nil
}

// DispatchSessioned added in Phase 2 of Dispatcher Service Unification
// (May 2026) to keep spyDispatcher satisfying the DispatcherService
// interface. /api/chat wire-shape pin specs that wire spyDispatcher via
// WithDispatcher don't drive /messages, so this method returns a stub
// snapshot; the /messages migration tests instead exercise the live
// *dispatch.Dispatcher path via the production NewServer auto-wire.
func (sp *spyDispatcher) DispatchSessioned(_ context.Context, req dispatch.DispatchRequest, _ streaming.StreamConsumer) (dispatch.SessionedHandle, error) {
	sp.mu.Lock()
	sp.requests = append(sp.requests, req)
	sp.mu.Unlock()
	return dispatch.SessionedHandle{}, nil
}

// TurnRegistry returns nil — the spy does not exercise the Turn surface.
// handleGetTurn maps a nil registry to HTTP 501 so callers that wire the
// spy can still observe the route's other behaviours. Phase 2 of "Turn-
// Based Post-Then-Poll Architecture (May 2026)".
func (sp *spyDispatcher) TurnRegistry() *turn.Registry { return nil }

func (sp *spyDispatcher) callCount() int {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	return len(sp.requests)
}

func (sp *spyDispatcher) lastRequest() dispatch.DispatchRequest {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	if len(sp.requests) == 0 {
		return dispatch.DispatchRequest{}
	}
	return sp.requests[len(sp.requests)-1]
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
	manifestSet        []agent.Manifest
	modelPrefProviders []string
	modelPrefModels    []string
	contextStoreCalls  int
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

// MaybeCompactForModel satisfies the Phase-5 Slice α addition to the
// orchestrator's Engine interface. The API parity tests do not assert
// against the trigger directly (the engine-side spec at the gate-
// proximity seam pins the per-trigger behaviour), but without this
// method the fake fails the orchestrator's `if wider, ok := eng.(Engine)`
// auto-narrow type assertion and the lifecycle-half engine field
// silently stays nil — every parity-fan-out spec then fails because
// SetManifest / SetModelPreference never run.
func (f *fakeDispatchEngine) MaybeCompactForModel(_ context.Context, _, _, _ string) string {
	return ""
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

		// Phase 1 of "Dispatcher Service Unification (May 2026)" —
		// handleChat now routes through dispatcher.DispatchEphemeral
		// when a Dispatcher is wired (production wiring via
		// internal/app + auto-construct in NewServer, or via the
		// explicit WithDispatcher option). The wire-shape contract
		// for /api/chat is unchanged byte-for-byte: same response
		// headers, same SSE event sequence (content+ followed by
		// [DONE]).
		Context("when a Dispatcher is wired (Phase 1)", func() {
			It("routes through Dispatcher.DispatchEphemeral; wire shape unchanged", func() {
				spy := &spyDispatcher{
					replayContent: "hello-from-spy",
				}
				registry.Register(&testManifest)
				srv := api.NewServer(streamer, registry, disc, skills,
					api.WithDispatcher(spy),
				)

				body := `{"agent_id":"test-agent","message":"hi"}`
				req := httptest.NewRequest(http.MethodPost, "/api/chat", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				srv.Handler().ServeHTTP(recorder, req)

				// Spy was called exactly once with the chat request shape.
				Expect(spy.callCount()).To(Equal(1))
				lastReq := spy.lastRequest()
				Expect(lastReq.AgentID).To(Equal("test-agent"))
				Expect(lastReq.Content).To(Equal("hi"))
				Expect(lastReq.ScanMentions).To(BeTrue())

				// Response headers unchanged.
				Expect(recorder.Code).To(Equal(http.StatusOK))
				Expect(recorder.Header().Get("Content-Type")).To(Equal("text/event-stream"))
				Expect(recorder.Header().Get("Cache-Control")).To(Equal("no-cache"))
				Expect(recorder.Header().Get("Connection")).To(Equal("keep-alive"))

				// SSE event sequence: at least one content frame
				// followed by [DONE]. The spy replays one content
				// chunk through the supplied consumer so the wire-
				// shape contract can be observed end-to-end.
				events := parseSSEEvents(recorder.Body)
				hasContent := false
				hasDone := false
				for _, ev := range events {
					if strings.Contains(ev, "hello-from-spy") {
						hasContent = true
					}
					if ev == "[DONE]" {
						hasDone = true
					}
				}
				Expect(hasContent).To(BeTrue(), "spy's content chunk must reach the SSE response")
				Expect(hasDone).To(BeTrue(), "[DONE] must terminate the SSE stream")
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


// Phase 2 GREEN gate per "Dispatcher Service Unification (May 2026)" v6.
//
// The refresh-bug class is structurally closed when /messages routes through
// dispatch.Dispatcher.DispatchSessioned. This spec pins the three load-bearing
// contracts that the migration cannot break:
//
//  1. Snapshot-before-SSE-content — the POST /api/v1/sessions/{id}/messages
//     response body's messages[] field contains the new user message as a
//     populated row BEFORE any SSE `content` event reaches the subscriber.
//     The Dispatcher appends the user message synchronously, captures the
//     snapshot, and returns the handle to the caller; chunks fan out via
//     `go sessionBroker.Publish(...)` AFTER the handler writes the snapshot.
//
//  2. SSE event sequence — the subscriber sees `[context_usage?, model_active?,
//     content+, DONE]`. At least one `content` event lands before `[DONE]`
//     and NO event carries `context canceled` / `context.Canceled` substrings.
//     This subsumes the e4bf9632 + 51fb416c regression pin on the unified
//     code path.
//
//  3. Async-POST contract preserved — POST returns 200 in <1s, well under the
//     several-seconds streamer time induced by dripStreamer's emit interval.
//     A SessionedHandle carries Snapshot only (no Done channel), so the
//     handler structurally cannot block on stream completion.
//
// The spec uses dripStreamer (not mockStreamer) so chunks remain in-flight
// across the POST→snapshot handoff — the bug only surfaces when the streamer
// channel is still live at the moment the handler returns. mockStreamer's
// pre-filled+closed channel cannot exercise this contract (and the v6 plan
// explicitly bans it for any Dispatcher-related Describe).
var _ = Describe("GetSession-deref-after-lock-release sibling races", func() {
	const (
		sessID     = "sibling-race-session"
		iterations = 20
	)

	// raceSetup builds an httptest server with a real Manager and seeds
	// one session. Returned closer must be invoked. Phase-4-Commit-2
	// retired the SessionBroker — race specs no longer require it.
	raceSetup := func() (*session.Manager, *httptest.Server, func()) {
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

	// DELETE /api/v1/sessions/{id} is the destructive route backing the Vue
	// UI's per-row trash button (SessionBrowser / SessionSwitcher). The
	// contract mirrors DELETE /api/v1/tasks/{id}: 204 on success, 404 for an
	// unknown id, 501 when the session manager is not configured. Closes
	// Quick-wins QW-11.
	Describe("DELETE /api/v1/sessions/{id}", func() {
		It("returns 204 and removes the session for an existing id", func() {
			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			req := httptest.NewRequest("DELETE", "/api/v1/sessions/"+sess.ID, http.NoBody)
			w := httptest.NewRecorder()
			server.Handler().ServeHTTP(w, req)
			Expect(w.Code).To(Equal(http.StatusNoContent))

			// Behavioural assertion: GET /children for the deleted id now
			// returns empty (session is gone from the in-memory map).
			_, err = mgr.GetSession(sess.ID)
			Expect(err).To(MatchError(session.ErrSessionNotFound),
				"the route must call into Manager.DeleteSession — otherwise the in-memory record survives")
		})

		It("returns 404 for a non-existent session", func() {
			req := httptest.NewRequest("DELETE", "/api/v1/sessions/nonexistent-id", http.NoBody)
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

	// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
	// (May 2026)" retired the per-session SSE/WebSocket routes
	// (handleSessionStream, handleSessionWebSocket). The nil-safety
	// pins they previously had moved to the negative-contract spec at
	// the bottom of this file ("retired routes return 404").

	Describe("DELETE /api/v1/sessions/{id}", func() {
		It("returns 501 when session manager is nil", func() {
			req := httptest.NewRequest("DELETE", "/api/v1/sessions/sess-1", http.NoBody)
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

		It("emits isStreaming: true for a session whose Turn registry has an active Running turn", func() {
			// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
			// (May 2026)" retired the SessionBroker; the isStreaming
			// field is now sourced from the dispatcher's Turn registry
			// — equivalent to `activeTurnId != ""`. The fixture below
			// drives a slow drip so the registry's Running entry is
			// observable across the GET /api/v1/sessions call.
			drip := &dripStreamer{
				chunks: []provider.StreamChunk{
					{Content: "running"},
					{Done: true},
				},
				emitInterval: 250 * time.Millisecond,
			}
			localMgr := session.NewManager(drip)
			localReg := agent.NewRegistry()
			localReg.Register(&agent.Manifest{ID: "agent-streaming", Name: "Agent Streaming"})
			srvWithTurn := api.NewServer(
				drip,
				localReg,
				discovery.NewAgentDiscovery(nil),
				nil,
				api.WithSessionManager(localMgr),
			)
			localHTTP := httptest.NewServer(srvWithTurn.Handler())
			defer localHTTP.Close()

			sess, err := localMgr.CreateSession("agent-streaming")
			Expect(err).NotTo(HaveOccurred())

			// Fire a POST /messages — this Starts the Turn in the registry.
			postResp, err := http.Post( //nolint:noctx
				localHTTP.URL+"/api/v1/sessions/"+sess.ID+"/messages",
				"application/json",
				strings.NewReader(`{"content":"hi"}`),
			)
			Expect(err).NotTo(HaveOccurred())
			postResp.Body.Close()
			Expect(postResp.StatusCode).To(Equal(http.StatusOK))

			// Eventually the GET returns isStreaming: true while the
			// Turn is still Running.
			Eventually(func() bool {
				getResp, getErr := http.Get(localHTTP.URL + "/api/v1/sessions") //nolint:noctx
				if getErr != nil {
					return false
				}
				defer getResp.Body.Close()
				if getResp.StatusCode != http.StatusOK {
					return false
				}
				raw, _ := io.ReadAll(getResp.Body)
				var rows []map[string]interface{}
				if err := json.Unmarshal(raw, &rows); err != nil {
					return false
				}
				for _, row := range rows {
					if row["id"] == sess.ID {
						b, _ := row["isStreaming"].(bool)
						return b
					}
				}
				return false
			}, "2s", "50ms").Should(BeTrue(),
				"session list must expose isStreaming: true while the Turn registry has a Running entry for the session")
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
	// Commit e4bf9632 ("fix(api): make POST /messages async, remove client-
	// side timeout") moved chunk publishing into a background goroutine so
	// the snapshot returns immediately to the client. The previous contract
	// — drain chunks synchronously so the assistant reply lands BEFORE the
	// HTTP response is written — was retired by that commit: long replies
	// blocked the HTTP response for minutes.
	//
	// Phase-4-Commit-2 of "Turn-Based Post-Then-Poll Architecture
	// (May 2026)" retired the SSE side-channel that previously surfaced
	// the assistant reply. The surviving contract is:
	//   (a) The user message MUST be appended synchronously and visible in
	//       the POST response snapshot, so the frontend can render the user
	//       bubble immediately without polling.
	//   (b) The POST response also carries `turn_id` so the frontend can
	//       long-poll GET /api/v1/sessions/{id}/turns/{turn_id} for the
	//       assistant reply. The "Turn-based poll endpoints" Describe pins
	//       (b) including the §AC#13 and §AC#11 SLOs.
	It("includes the user message in the POST response snapshot synchronously", func() {
		recorder := httptest.NewRecorder()
		streamer := &mockStreamer{chunks: []provider.StreamChunk{{Content: "ok"}, {Done: true}}}
		mgr := session.NewManager(streamer)
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv := api.NewServer(streamer, registry, disc, nil, api.WithSessionManager(mgr))

		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		body := `{"content":"hello"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sess.ID+"/messages", strings.NewReader(body))
		srv.Handler().ServeHTTP(recorder, req)
		Expect(recorder.Code).To(Equal(http.StatusOK))

		// Phase 4 POST response shape: {turn_id, snapshot}. The snapshot
		// carries messageCount + the user message; turn_id is the
		// long-poll handle for the assistant reply.
		var out struct {
			TurnID   string `json:"turn_id"`
			Snapshot struct {
				MessageCount int               `json:"messageCount"`
				Messages     []session.Message `json:"messages"`
			} `json:"snapshot"`
		}
		Expect(json.Unmarshal(recorder.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Snapshot.MessageCount).To(BeNumerically(">=", 1),
			"response must include the user message synchronously so the frontend renders the user bubble without polling")

		var userContent string
		for _, m := range out.Snapshot.Messages {
			if m.Role == "user" {
				userContent = m.Content
				break
			}
		}
		Expect(userContent).To(Equal("hello"),
			"user message must be appended to the session before the HTTP response is written")
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

	publishAndDrain := func(sessionID string, publish func()) []map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// H5 (May 2026): /api/swarm/events now requires ?session_id= and
		// filters cross-tenant events. Existing projection specs scope to the
		// session whose publish() they exercise.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/api/swarm/events?session_id="+sessionID, http.NoBody)
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
		collected := publishAndDrain("parent-sse", func() {
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
		collected := publishAndDrain("parent", func() {
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
		collected := publishAndDrain("parent", func() {
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

	// Gap 1 (load_skills propagation, May 2026). The Vue surface's
	// delegation-skills-row renders chips from
	// SwarmEvent.metadata.load_skills (string[]); the projector must
	// surface DelegationEventData.LoadSkills onto the wire, and must
	// omit the key when the slice is empty so the chip block stays
	// hidden for delegations that did not pass load_skills.
	It("includes metadata.load_skills when DelegationEventData.LoadSkills is non-empty", func() {
		collected := publishAndDrain("parent-skills", func() {
			bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
				ChainID:         "chain-skills-1",
				ParentSessionID: "parent-skills",
				ChildSessionID:  "child-skills",
				SourceAgent:     "orchestrator",
				TargetAgent:     "knowledge-base-curator",
				LoadSkills:      []string{"memory-keeper", "knowledge-base"},
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
		metadata, ok := delegation["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		skills, ok := metadata["load_skills"].([]any)
		Expect(ok).To(BeTrue(),
			"load_skills must project as a JSON array so the Vue surface receives a string[] after the wire decode")
		Expect(skills).To(HaveLen(2))
		Expect(skills[0]).To(Equal("memory-keeper"))
		Expect(skills[1]).To(Equal("knowledge-base"))
	})

	It("omits metadata.load_skills when DelegationEventData.LoadSkills is empty", func() {
		collected := publishAndDrain("parent-no-skills", func() {
			bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
				ChainID:         "chain-no-skills-1",
				ParentSessionID: "parent-no-skills",
				ChildSessionID:  "child-no-skills",
				SourceAgent:     "orchestrator",
				TargetAgent:     "qa-agent",
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
		metadata, ok := delegation["metadata"].(map[string]any)
		Expect(ok).To(BeTrue())
		_, present := metadata["load_skills"]
		Expect(present).To(BeFalse(),
			"omitting the load_skills key keeps the wire small and matches the frontend's `if load_skills` chip-render gate")
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

	publishAndDrain := func(sessionID string, publish func()) []map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		// H5 (May 2026): /api/swarm/events now requires ?session_id= and
		// filters cross-tenant events. Existing projection specs scope to the
		// session whose publish() they exercise.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/api/swarm/events?session_id="+sessionID, http.NoBody)
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
		collected := publishAndDrain("sess-1", func() {
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
		collected := publishAndDrain("sess-1", func() {
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
		collected := publishAndDrain("sess-1", func() {
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
		collected := publishAndDrain("sess-fallback", func() {
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

// QA bughunt 2026-05-08, C3: handleSwarmEvents blocking sends DoS engine.
//
// The /api/swarm/events handler historically registered bus event handlers as
// `func(msg any) { eventCh <- msg }` with no `select { case ...: default: }`
// guard. The eventCh has cap 64. When the SSE client is slow or has just
// disconnected (deferred Unsubscribe still in-flight), eventCh fills and every
// subsequent bus.Publish from any session — tool execution, delegation,
// background tasks — calls these handlers, which block on the full channel.
// One slow swarm-events client wedges every tool execution across every
// session.
//
// H6 (handler still queued after disconnect) shares the same root cause and is
// closed by the same fix: dropping on a full channel means the in-flight
// Publish goroutines that captured the handler before deferred Unsubscribe ran
// no longer block on a dead consumer.
//
// Pinning specs: bus.Publish must NEVER block on the swarm-events handler,
// even when the SSE consumer has stalled and eventCh is at capacity. This
// matches the pattern already used by event_bridge.go (drop-on-full).
var _ = Describe("GET /api/swarm/events backpressure isolation (C3 / H6)", func() {
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

	// Stand the handler up with a deliberately stalled consumer. We do this by
	// dialling the server raw, sending a GET, and then never reading the
	// response body. Once the kernel's write buffer fills, flusher.Flush()
	// blocks the consumer goroutine inside handleSwarmEvents, eventCh stops
	// draining, and after `cap(eventCh)` events any further blocking handler
	// would wedge bus.Publish.
	startStalledSwarmConsumer := func() (cleanup func()) {
		u, err := url.Parse(hs.URL)
		Expect(err).NotTo(HaveOccurred())

		conn, err := net.Dial("tcp", u.Host)
		Expect(err).NotTo(HaveOccurred())

		// H5 (May 2026): the handler now requires ?session_id=. The
		// backpressure spec only cares that a slow consumer cannot wedge the
		// bus, so any non-empty session id is valid here — both flood
		// publishes use ChildSessionID="child" / ParentSessionID="parent",
		// "parent" is in scope for this consumer and exercises the same
		// drop-on-full path.
		req := "GET /api/swarm/events?session_id=parent HTTP/1.1\r\n" +
			"Host: " + u.Host + "\r\n" +
			"Accept: text/event-stream\r\n" +
			"Connection: keep-alive\r\n\r\n"
		_, err = conn.Write([]byte(req))
		Expect(err).NotTo(HaveOccurred())

		// Give the handler time to register its bus subscriptions.
		time.Sleep(120 * time.Millisecond)

		return func() { _ = conn.Close() }
	}

	It("does not wedge bus.Publish when the SSE consumer has stalled and eventCh is full", func() {
		cleanup := startStalledSwarmConsumer()
		defer cleanup()

		// Flood the bus with far more events than the handler's eventCh
		// capacity (64) + any kernel TCP send buffer. Each Publish runs
		// synchronously through every registered handler — including the
		// stalled swarm-events handler — so once eventCh fills, an unguarded
		// handler blocks Publish indefinitely.
		//
		// Delegation events are projected with the full `description` and
		// other metadata onto the wire, so a bulky Description guarantees the
		// consumer goroutine's flusher.Flush() blocks on the kernel send
		// buffer once the stalled TCP reader is overwhelmed, which in turn
		// stops eventCh from draining.
		bulkyDesc := strings.Repeat("x", 16384)
		floodDone := make(chan struct{})
		go func() {
			defer close(floodDone)
			for i := 0; i < 500; i++ {
				bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
					ChainID:         "chain-flood",
					ParentSessionID: "parent",
					ChildSessionID:  "child",
					SourceAgent:     "lead",
					TargetAgent:     "qa",
					Description:     bulkyDesc,
				}))
			}
		}()

		// The flood goroutine itself must terminate within a tight deadline.
		// Pre-fix this hangs forever on roughly the 65th publish (cap of
		// eventCh) once the stalled consumer's flusher.Flush() also blocks
		// on the kernel send buffer.
		Eventually(floodDone, 3*time.Second).Should(BeClosed(),
			"bus.Publish must not block on the stalled swarm-events handler — "+
				"a slow SSE consumer must not DoS every other publisher")

		// Independent publish from a second goroutine, simulating a tool
		// execution path. With the fix in place this returns immediately; the
		// blocking-send variant wedges this goroutine on the stalled consumer.
		independentDone := make(chan struct{})
		go func() {
			defer close(independentDone)
			bus.Publish(events.EventDelegationCompleted, events.NewDelegationCompletedEvent(events.DelegationEventData{
				ChainID:         "engine-call",
				ParentSessionID: "engine",
				ChildSessionID:  "engine-child",
				SourceAgent:     "engine",
				TargetAgent:     "tool-runner",
				Description:     bulkyDesc,
			}))
		}()

		Eventually(independentDone, 500*time.Millisecond).Should(BeClosed(),
			"a fresh bus.Publish from any other session must complete promptly even "+
				"while the swarm-events consumer is stalled")
	})

	It("does not leak goroutines wedged on a dead consumer after disconnect (H6)", func() {
		// Capture a baseline goroutine count, then churn through many
		// connect-flood-disconnect cycles. With the blocking-send variant,
		// every disconnect leaves Publish goroutines wedged on the now-closed
		// handler's eventCh. The drop-on-full guard makes this safe.
		baseline := runtime.NumGoroutine()

		bulkyDesc := strings.Repeat("y", 16384)
		for cycle := 0; cycle < 5; cycle++ {
			cleanup := startStalledSwarmConsumer()

			// Fill the channel and overflow it. Bulky description ensures we
			// pin the disconnect-with-in-flight-Publish race; a small payload
			// would clear the consumer's TCP buffer faster than the test can
			// disconnect.
			for i := 0; i < 200; i++ {
				bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
					ChainID:         "chain-cycle",
					ParentSessionID: "parent",
					ChildSessionID:  "child",
					SourceAgent:     "lead",
					TargetAgent:     "qa",
					Description:     bulkyDesc,
				}))
			}

			cleanup()
			// Allow deferred Unsubscribe + handler goroutines to settle.
			time.Sleep(80 * time.Millisecond)
		}

		// We allow some slack — httptest workers, GC goroutines, etc. — but a
		// goroutine leak proportional to events published would blow well past
		// this bound (5 cycles * 100 publishes = 500 wedged goroutines).
		Eventually(runtime.NumGoroutine, 2*time.Second, 50*time.Millisecond).
			Should(BeNumerically("<", baseline+50),
				"connect-flood-disconnect churn must not leak goroutines wedged on a dead handler's eventCh")
	})
})

// QA bughunt 2026-05-08, H5: handleSwarmEvents cross-tenant event leak.
//
// The /api/swarm/events handler historically subscribed to nine bus topics
// globally with no per-request session filter and no auth. Any client could
// connect anonymously and receive SessionID, tool names, error strings, and
// delegation chains for every active session in the process. Documented as
// "H5" in vault Bug Hunt Findings (May 2026).
//
// Fix shape: require ?session_id=<id> on the request; filter projected events
// such that only events whose underlying SessionID (tool/background) or
// Parent/Child SessionID (delegation) matches the requested session are
// emitted. Anonymous global subscription returns 400.
//
// Pinning specs:
//
//  1. A request without ?session_id= MUST be rejected (no leak surface).
//  2. A request with ?session_id=A MUST receive events for session A and MUST
//     NOT receive events for session B published concurrently — the cross-
//     tenant leak.
//  3. Delegation events match on either Parent or Child session id, since
//     both surfaces participate in the chain (the parent surface needs the
//     started/completed; the child surface needs the parent linkage).
var _ = Describe("GET /api/swarm/events session filter (H5)", func() {
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

	// streamEvents opens an SSE connection at the given URL and returns up to
	// `expect` decoded data frames (after the initial `connected` frame). The
	// connection is closed when the deadline fires or the expected count is
	// reached, whichever is first.
	streamEvents := func(rawURL string, expect int, deadline time.Duration) []map[string]any {
		ctx, cancel := context.WithTimeout(context.Background(), deadline)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil
		}

		var collected []map[string]any
		reader := bufio.NewReader(resp.Body)
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
				return collected
			}
			if expect > 0 && len(collected) >= expect {
				return collected
			}
		}
	}

	It("rejects requests without a session_id query parameter", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, hs.URL+"/api/swarm/events", http.NoBody)
		Expect(err).NotTo(HaveOccurred())

		resp, err := http.DefaultClient.Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		Expect(resp.StatusCode).To(Equal(http.StatusBadRequest),
			"unfiltered subscription is the cross-tenant leak — the handler must reject anonymous global subscribers")
	})

	It("emits tool events for the requested session and drops events from other sessions", func() {
		// Open a stream scoped to session-A. Publish tool events for both
		// session-A and session-B concurrently. The connection must see the
		// session-A event and never see the session-B event.
		//
		// To make the leak detection deterministic regardless of scheduling
		// order we publish session-B FIRST (the leak), then session-A. With
		// the unfiltered handler both arrive on the wire; with the filtered
		// handler only the session-A frame arrives. Reading until we observe
		// the connected hello + the owned session-A frame proves session-B
		// did not appear (since publish order placed it earlier in the bus
		// queue).
		streamURL := hs.URL + "/api/swarm/events?session_id=session-A"

		eventsCh := make(chan []map[string]any, 1)
		go func() {
			eventsCh <- streamEvents(streamURL, 2, 2*time.Second)
		}()

		// Allow the handler to register its bus subscriptions.
		time.Sleep(120 * time.Millisecond)

		// session-B published FIRST — must NOT be emitted on session-A's
		// stream. This is the cross-tenant leak: tool name + result body for
		// an unrelated tenant ahead of the owner's frame.
		bus.Publish(events.EventToolExecuteResult, events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
			SessionID:          "session-B",
			ToolName:           "secret_tool",
			InternalToolCallID: "tcid-B",
			ToolCallID:         "prov-B",
			Result:             "leaked-from-B",
		}))

		// session-A — should be emitted.
		bus.Publish(events.EventToolExecuteResult, events.NewToolExecuteResultEvent(events.ToolExecuteResultEventData{
			SessionID:          "session-A",
			ToolName:           "bash",
			InternalToolCallID: "tcid-A",
			ToolCallID:         "prov-A",
			Result:             "for-A",
		}))

		var collected []map[string]any
		Eventually(eventsCh, 3*time.Second).Should(Receive(&collected))

		// Sanity: connected frame is first.
		Expect(collected).NotTo(BeEmpty())
		Expect(collected[0]["type"]).To(Equal("connected"))

		// Event frames after the connected hello.
		var leaked []map[string]any
		var owned []map[string]any
		for _, ev := range collected[1:] {
			if ev["agent_id"] == "session-A" {
				owned = append(owned, ev)
			}
			if ev["agent_id"] == "session-B" {
				leaked = append(leaked, ev)
			}
			if md, ok := ev["metadata"].(map[string]any); ok {
				if name, _ := md["tool_name"].(string); name == "secret_tool" {
					leaked = append(leaked, ev)
				}
			}
		}

		Expect(leaked).To(BeEmpty(),
			"H5: a session-A subscriber must NEVER receive tool events for session-B")
		Expect(owned).NotTo(BeEmpty(),
			"a session-A subscriber must still receive its own tool events post-filter")
	})

	It("emits delegation events when either Parent or Child session id matches the filter", func() {
		// Subscribe scoped to the child session — the typical surface, since
		// the child UI clicks through into its own delegation panel.
		streamURL := hs.URL + "/api/swarm/events?session_id=child-1"

		eventsCh := make(chan []map[string]any, 1)
		go func() {
			eventsCh <- streamEvents(streamURL, 3, 2*time.Second)
		}()
		time.Sleep(120 * time.Millisecond)

		// Owned: child-1 is the ChildSessionID — must be emitted.
		bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
			ChainID:         "chain-owned",
			ParentSessionID: "parent-1",
			ChildSessionID:  "child-1",
			SourceAgent:     "lead",
			TargetAgent:     "qa",
		}))

		// Foreign: neither parent nor child match — must be filtered.
		bus.Publish(events.EventDelegationStarted, events.NewDelegationStartedEvent(events.DelegationEventData{
			ChainID:         "chain-foreign",
			ParentSessionID: "parent-X",
			ChildSessionID:  "child-X",
			SourceAgent:     "lead",
			TargetAgent:     "qa",
		}))

		var collected []map[string]any
		Eventually(eventsCh, 3*time.Second).Should(Receive(&collected))

		var owned, leaked []map[string]any
		for _, ev := range collected[1:] {
			if ev["id"] == "chain-owned" {
				owned = append(owned, ev)
			}
			if ev["id"] == "chain-foreign" {
				leaked = append(leaked, ev)
			}
		}
		Expect(owned).NotTo(BeEmpty(),
			"a delegation whose ChildSessionID matches the filter must reach the subscriber")
		Expect(leaked).To(BeEmpty(),
			"H5: a delegation whose Parent/Child both fall outside the filter must NOT leak")
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

// POST /api/v1/sessions/{id}/messages swarm auto-dispatch + in-content
// @<swarm-id> mention specs were MOVED to
// internal/dispatch/dispatcher_test.go::Describe("Dispatcher.DispatchSessioned")
// per Phase 2 of "Dispatcher Service Unification (May 2026)". The
// behaviour is identical — the deleted server.go helpers
// (resolveAutoDispatchSwarm @ 07b0480e, resolveInContentMention @
// 48380376, wrapWithSwarmLifecycle) are SUBSUMED by
// Dispatcher.DispatchSessioned. The handler-thinness regression at
// internal/api/handler_thinness_test.go::TestHandlerThinness_handleSessionMessage
// pins that no banned symbols remain in handleSessionMessage.
var _ = Describe("POST /api/v1/sessions/{id}/messages swarm auto-dispatch — MOVED to dispatch package", func() {
	It("placeholder — see internal/dispatch/dispatcher_test.go", func() {
		// Behaviour pinned at the Dispatcher seam per Phase 2 of
		// "Dispatcher Service Unification (May 2026)". This stub keeps
		// the file-level test count stable post-deletion and documents
		// where to find the live regression specs.
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
var _ = Describe("POST /api/v1/sessions/{id}/attachments", func() {
	var (
		mgr *session.Manager
		srv *api.Server
		dir string
	)

	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00}
	jpgBytes := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{}})
		mgr.SetSessionsDir(dir)
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

	// uploadRequest builds a multipart POST for the upload endpoint.
	// Field name is `files`, matching the handler contract; each part
	// carries an explicit Content-Type header (advisory only — the
	// handler content-sniffs the bytes for the authoritative type).
	uploadRequest := func(sessionID string, parts []multipartPart) *http.Request {
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		for _, p := range parts {
			h := make(textproto.MIMEHeader)
			h.Set("Content-Disposition", `form-data; name="files"; filename="`+p.filename+`"`)
			if p.contentType != "" {
				h.Set("Content-Type", p.contentType)
			}
			pw, err := mw.CreatePart(h)
			Expect(err).NotTo(HaveOccurred())
			_, err = pw.Write(p.data)
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(mw.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions/"+sessionID+"/attachments", body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		return req
	}

	It("uploads a single image and returns its id and metadata", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		req := uploadRequest(sess.ID, []multipartPart{
			{filename: "cat.png", contentType: "image/png", data: pngBytes},
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		var out struct {
			Attachments []struct {
				ID               string `json:"id"`
				MediaType        string `json:"mediaType"`
				SizeBytes        int64  `json:"sizeBytes"`
				OriginalFilename string `json:"originalFilename"`
			} `json:"attachments"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Attachments).To(HaveLen(1))
		Expect(out.Attachments[0].ID).NotTo(BeEmpty())
		Expect(out.Attachments[0].MediaType).To(Equal("image/png"))
		Expect(out.Attachments[0].SizeBytes).To(Equal(int64(len(pngBytes))))
		Expect(out.Attachments[0].OriginalFilename).To(Equal("cat.png"))
	})

	It("uploads multiple images in one request", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		req := uploadRequest(sess.ID, []multipartPart{
			{filename: "cat.png", contentType: "image/png", data: pngBytes},
			{filename: "dog.jpg", contentType: "image/jpeg", data: jpgBytes},
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		var out struct {
			Attachments []map[string]any `json:"attachments"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Attachments).To(HaveLen(2))
	})

	It("returns 404 for an unknown session id", func() {
		req := uploadRequest("ghost-session", []multipartPart{
			{filename: "cat.png", contentType: "image/png", data: pngBytes},
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("rejects an unsupported media type with 415 (content sniff)", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		// Plain text payload — bytes do not sniff to an allowed image.
		req := uploadRequest(sess.ID, []multipartPart{
			{filename: "evil.png", contentType: "image/png", data: []byte("not an image")},
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnsupportedMediaType))
	})

	It("rejects a SVG payload with 415 (PR1 excludes svg)", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"/>`)
		req := uploadRequest(sess.ID, []multipartPart{
			{filename: "vec.svg", contentType: "image/svg+xml", data: svg},
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusUnsupportedMediaType))
	})

	It("rejects a file over the per-file cap with 413", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		big := make([]byte, session.MaxAttachmentFileBytes+10)
		// PNG magic so the sniff would pass — but the size check fires first.
		copy(big, pngBytes)
		req := uploadRequest(sess.ID, []multipartPart{
			{filename: "huge.png", contentType: "image/png", data: big},
		})
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge))
	})

	It("rejects >10 files in one request with 400", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		parts := make([]multipartPart, 11)
		for i := range parts {
			// Make each unique so dedup doesn't collapse them in the
			// store (we never reach storage on this path, but defensive).
			b := make([]byte, len(pngBytes)+1)
			copy(b, pngBytes)
			b[len(pngBytes)] = byte(i)
			parts[i] = multipartPart{
				filename:    "p.png",
				contentType: "image/png",
				data:        b,
			}
		}
		req := uploadRequest(sess.ID, parts)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	It("dedups a re-upload of the same content and returns the same id", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		// First upload.
		req1 := uploadRequest(sess.ID, []multipartPart{
			{filename: "cat.png", contentType: "image/png", data: pngBytes},
		})
		rec1 := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec1, req1)
		Expect(rec1.Code).To(Equal(http.StatusOK))
		var out1 struct {
			Attachments []struct {
				ID string `json:"id"`
			} `json:"attachments"`
		}
		Expect(json.Unmarshal(rec1.Body.Bytes(), &out1)).To(Succeed())

		// Second upload — identical bytes.
		req2 := uploadRequest(sess.ID, []multipartPart{
			{filename: "cat-renamed.png", contentType: "image/png", data: pngBytes},
		})
		rec2 := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec2, req2)
		Expect(rec2.Code).To(Equal(http.StatusOK))
		var out2 struct {
			Attachments []struct {
				ID string `json:"id"`
			} `json:"attachments"`
		}
		Expect(json.Unmarshal(rec2.Body.Bytes(), &out2)).To(Succeed())

		Expect(out2.Attachments[0].ID).To(Equal(out1.Attachments[0].ID))
	})

	It("returns 400 for an empty multipart form (no files)", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		req := uploadRequest(sess.ID, nil)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusBadRequest))
	})

	// Plan §6 task-15 + §7a — Cap Precedence Order. Six rejection paths
	// evaluate deterministically; where two share an HTTP status code
	// (rows 1 and 6 are both 415) the `error` field disambiguates.
	Context("cap-precedence ladder (plan §7a)", func() {
		pdfBytesLocal := []byte("%PDF-1.4\n%fake-pdf-body\n")

		// extractErr unmarshals the structured error envelope and
		// returns (status, error-code) for ladder-precedence asserts.
		extractErr := func(body []byte) (string, string) {
			var env struct {
				Error   string `json:"error"`
				Message string `json:"message"`
			}
			Expect(json.Unmarshal(body, &env)).To(Succeed())
			return env.Error, env.Message
		}

		It("step 1 fires first: .txt upload returns 415/media_type_not_allowed (precedence over step 6)", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "ollama", "llama3.2")
			Expect(err).NotTo(HaveOccurred())

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "data.txt", contentType: "text/plain", data: []byte("a,b,c\n1,2,3\n")},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnsupportedMediaType))
			code, _ := extractErr(rec.Body.Bytes())
			Expect(code).To(Equal("media_type_not_allowed"))
		})

		It("step 2 fires before step 6: 12 MB PDF on Ollama returns 413/file_too_large", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "ollama", "llama3.2")
			Expect(err).NotTo(HaveOccurred())

			big := make([]byte, session.MaxPDFFileSize+1)
			copy(big, []byte("%PDF-1.4\n"))
			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "huge.pdf", contentType: "application/pdf", data: big},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge))
			code, _ := extractErr(rec.Body.Bytes())
			Expect(code).To(Equal("file_too_large"))
		})

		It("step 3 fires: >5 PDFs on an Anthropic session returns 400/too_many_attachments", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())

			parts := make([]multipartPart, 6)
			for i := range parts {
				// Make each unique so dedup doesn't collapse them.
				body := append([]byte("%PDF-1.4\n%body-"), byte('a'+i))
				parts[i] = multipartPart{
					filename:    fmt.Sprintf("doc-%d.pdf", i),
					contentType: "application/pdf",
					data:        body,
				}
			}
			req := uploadRequest(sess.ID, parts)
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusBadRequest))
			code, _ := extractErr(rec.Body.Bytes())
			Expect(code).To(Equal("too_many_attachments"))
		})

		It("step 5 fires: cumulative >25 MB returns 413/request_too_large", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())

			// 3 × 10 MiB PDFs = 30 MiB total, over the 25 MB ceiling.
			// Each PDF is exactly at MaxPDFFileSize, so step 2 passes
			// (size cap is "exceeds limit" not "equals limit").
			big1 := make([]byte, session.MaxPDFFileSize)
			copy(big1, []byte("%PDF-1.4\n%a"))
			big2 := make([]byte, session.MaxPDFFileSize)
			copy(big2, []byte("%PDF-1.4\n%b"))
			big3 := make([]byte, session.MaxPDFFileSize)
			copy(big3, []byte("%PDF-1.4\n%c"))

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "a.pdf", contentType: "application/pdf", data: big1},
				{filename: "b.pdf", contentType: "application/pdf", data: big2},
				{filename: "c.pdf", contentType: "application/pdf", data: big3},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusRequestEntityTooLarge))
			code, _ := extractErr(rec.Body.Bytes())
			Expect(code).To(Equal("request_too_large"))
		})

		It("step 6 fires last: 2 MB PDF on Ollama returns 415/provider_does_not_support_pdf", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "ollama", "llama3.2")
			Expect(err).NotTo(HaveOccurred())

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "doc.pdf", contentType: "application/pdf", data: pdfBytesLocal},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusUnsupportedMediaType))
			code, msg := extractErr(rec.Body.Bytes())
			Expect(code).To(Equal("provider_does_not_support_pdf"))
			Expect(msg).To(ContainSubstring("Anthropic"))
		})

		It("positive control: PDF on an Anthropic session returns 200", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "doc.pdf", contentType: "application/pdf", data: pdfBytesLocal},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			var out struct {
				Attachments []struct {
					Kind      string `json:"kind"`
					MediaType string `json:"mediaType"`
				} `json:"attachments"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out.Attachments).To(HaveLen(1))
			Expect(out.Attachments[0].Kind).To(Equal("document"))
			Expect(out.Attachments[0].MediaType).To(Equal("application/pdf"))
		})

		It("image upload on Ollama is unaffected by the PDF gate (returns 200)", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "ollama", "llama3.2")
			Expect(err).NotTo(HaveOccurred())

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "cat.png", contentType: "image/png", data: pngBytes},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
		})

		It("mixed image+PDF on Anthropic returns 200 with both kinds in source order", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "anthropic", "claude-sonnet-4-6")
			Expect(err).NotTo(HaveOccurred())

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "cat.png", contentType: "image/png", data: pngBytes},
				{filename: "doc.pdf", contentType: "application/pdf", data: pdfBytesLocal},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Code).To(Equal(http.StatusOK))
			var out struct {
				Attachments []struct {
					Kind string `json:"kind"`
				} `json:"attachments"`
			}
			Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
			Expect(out.Attachments).To(HaveLen(2))
			Expect(out.Attachments[0].Kind).To(Equal("image"))
			Expect(out.Attachments[1].Kind).To(Equal("document"))
		})

		It("structured-JSON error envelope shape: {error, message} for every rejection path", func() {
			sess, err := mgr.CreateSessionWithDefaults("agent-x", "ollama", "llama3.2")
			Expect(err).NotTo(HaveOccurred())

			req := uploadRequest(sess.ID, []multipartPart{
				{filename: "doc.pdf", contentType: "application/pdf", data: pdfBytesLocal},
			})
			rec := httptest.NewRecorder()
			srv.Handler().ServeHTTP(rec, req)

			Expect(rec.Header().Get("Content-Type")).To(ContainSubstring("application/json"))
			var env map[string]any
			Expect(json.Unmarshal(rec.Body.Bytes(), &env)).To(Succeed())
			Expect(env).To(HaveKey("error"))
			Expect(env).To(HaveKey("message"))
		})
	})
})

// multipartPart is a small helper struct for building multipart parts
// in the upload-endpoint specs above.
type multipartPart struct {
	filename    string
	contentType string
	data        []byte
}

// GET /api/v1/sessions/{id}/attachments/{aid} — plan "Chat Attachments
// Backend (May 2026)" §6 task-07. Binary retrieval endpoint backing
// the inbound `<img src="/api/v1/sessions/.../attachments/...">`
// surface from task-08. Rides the same path-param session-scope gate
// as `internal/api/server.go:832-888 (handleSessionMessage)` until
// Auth Track v1's RequireSession middleware lands (plan R3).
//
// Cross-session probe MUST return 404 with NO media-type leak — the
// handler must not even check whether the id exists in the other
// session, since that would surface a length / timing side-channel
// confirming or denying the id (R9: cross-session injection via
// prompt-poisoned `<img src>`).
//
// Extends the existing server_test.go seam per memory
// feedback_extend_existing_specs.
var _ = Describe("GET /api/v1/sessions/{id}/attachments/{aid}", func() {
	var (
		mgr *session.Manager
		srv *api.Server
		dir string
	)

	pngBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00}

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{}})
		mgr.SetSessionsDir(dir)
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

	// putAttachment uploads a fixture image and returns the assigned id.
	// Routes through the production upload handler so the on-disk state
	// matches what the GET endpoint reads on the other side.
	putAttachment := func(sessionID string, data []byte, filename string) string {
		body := &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		h := make(textproto.MIMEHeader)
		h.Set("Content-Disposition", `form-data; name="files"; filename="`+filename+`"`)
		h.Set("Content-Type", "image/png")
		pw, err := mw.CreatePart(h)
		Expect(err).NotTo(HaveOccurred())
		_, err = pw.Write(data)
		Expect(err).NotTo(HaveOccurred())
		Expect(mw.Close()).To(Succeed())
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/sessions/"+sessionID+"/attachments", body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)
		Expect(rec.Code).To(Equal(http.StatusOK))
		var out struct {
			Attachments []struct {
				ID string `json:"id"`
			} `json:"attachments"`
		}
		Expect(json.Unmarshal(rec.Body.Bytes(), &out)).To(Succeed())
		Expect(out.Attachments).To(HaveLen(1))
		return out.Attachments[0].ID
	}

	It("returns the raw bytes with the stored Content-Type", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())
		aid := putAttachment(sess.ID, pngBytes, "cat.png")

		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/sessions/"+sess.ID+"/attachments/"+aid, http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Content-Type")).To(Equal("image/png"))
		Expect(rec.Body.Bytes()).To(Equal(pngBytes),
			"the served body must be the exact uploaded bytes")
	})

	It("sets Cache-Control: private, max-age=300 (content-hash names are stable)", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())
		aid := putAttachment(sess.ID, pngBytes, "cat.png")

		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/sessions/"+sess.ID+"/attachments/"+aid, http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusOK))
		Expect(rec.Header().Get("Cache-Control")).To(Equal("private, max-age=300"))
	})

	It("returns 404 for an unknown attachment id inside a valid session", func() {
		sess, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/sessions/"+sess.ID+"/attachments/ghost-id", http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	It("returns 404 for an unknown session id", func() {
		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/sessions/ghost-session/attachments/some-id", http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNotFound))
	})

	// R9: cross-session injection. A real-but-other session id MUST
	// return 404 with NO leak of the real attachment's media type or
	// any other metadata. The handler must not even consult the other
	// session's store — the path-param IS the auth scope.
	It("returns 404 when an attachment id exists in another session (no media-type leak)", func() {
		sessA, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())
		sessB, err := mgr.CreateSession("agent-x")
		Expect(err).NotTo(HaveOccurred())

		// Put the attachment in sessA — sessB MUST NOT see it via its
		// own path scope.
		aid := putAttachment(sessA.ID, pngBytes, "cat.png")

		req := httptest.NewRequest(http.MethodGet,
			"/api/v1/sessions/"+sessB.ID+"/attachments/"+aid, http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		Expect(rec.Code).To(Equal(http.StatusNotFound),
			"cross-session probe must return 404")
		// CRITICAL: no Content-Type leak. The 404 response must not
		// carry the real attachment's media type (would confirm the id
		// exists in some other session). http.Error sets Content-Type
		// to text/plain for the error body, which is fine — we only
		// forbid image/* leakage.
		ct := rec.Header().Get("Content-Type")
		Expect(ct).NotTo(HavePrefix("image/"),
			"cross-session 404 must not leak the real attachment's image media type")
		// And the body must not contain the bytes either.
		Expect(rec.Body.Bytes()).NotTo(Equal(pngBytes),
			"cross-session 404 must not leak the real attachment bytes")
	})
})

// Content-Security-Policy header — plan "Chat Attachments Backend
// (May 2026)" §6 task-09. Extends the existing securityHeaders
// middleware (internal/api/server.go:381-389 (securityHeaders)) to
// permit `data:` and `'self'` `<img>` sources so (a) base64 data
// URLs from assistant responses and (b) same-origin GETs to
// `/api/v1/sessions/{id}/attachments/{aid}` render without CSP
// violations. Extends the existing server_test.go seam per memory
// feedback_extend_existing_specs.
var _ = Describe("Content-Security-Policy header (task-09)", func() {
	var srv *api.Server

	BeforeEach(func() {
		registry := agent.NewRegistry()
		disc := discovery.NewAgentDiscovery(nil)
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			registry,
			disc,
			nil,
		)
	})

	It("includes img-src 'self' data: alongside default-src 'self'", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/skills", http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		Expect(csp).NotTo(BeEmpty(), "CSP header must be set on every response")
		Expect(csp).To(ContainSubstring("default-src 'self'"),
			"default-src must remain 'self' — task-09 must not widen non-image directives")
		Expect(csp).To(ContainSubstring("img-src 'self' data:"),
			"img-src must permit 'self' and data: so attachments + base64 data URLs render")
	})

	It("does not widen script-src or style-src as a side effect of the img-src extension", func() {
		req := httptest.NewRequest(http.MethodGet, "/api/skills", http.NoBody)
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		csp := rec.Header().Get("Content-Security-Policy")
		// The PR2 extension is image-only. Script and style remain
		// constrained to 'self' via default-src; no explicit widening
		// (and no `unsafe-inline` / `unsafe-eval` slips in).
		Expect(csp).NotTo(ContainSubstring("unsafe-inline"))
		Expect(csp).NotTo(ContainSubstring("unsafe-eval"))
		Expect(csp).NotTo(ContainSubstring("script-src"))
		Expect(csp).NotTo(ContainSubstring("style-src"))
	})
})


// Phase 2 RED gate per "Turn-Based Post-Then-Poll Architecture
// (May 2026)". These specs pin the HTTP surface that exposes the Turn
// resource introduced in Phase 1 (internal/turn package + dispatcher
// integration, commit 6ce8048d):
//
//   - POST /api/v1/sessions/{id}/messages returns {turn_id, snapshot}
//     so the frontend can drive GET /turns/{turn_id}.
//   - GET /api/v1/sessions/{id}/turns/{turn_id} returns the Turn's
//     status, started_at / completed_at, model, error, and
//     engine-emitted messages.
//   - dispatch.ErrTurnConflict maps to HTTP 409 Conflict — v1
//     supports ONE in-flight turn per session.
//
// All specs use dripStreamer through the production NewServer
// auto-construct path so the real *dispatch.Dispatcher writes into
// the registry — the spy bypasses the registry and would hide
// integration bugs.
var _ = Describe("Turn-based poll endpoints (POST /messages + GET /turns/{turn_id})", func() {
	var (
		drip     *dripStreamer
		mgr      *session.Manager
		srv      *api.Server
		httpSrv  *httptest.Server
		reg      *agent.Registry
	)

	// uuidV4Regex matches the google/uuid library's default canonical
	// form so the spec can pin both "non-empty" and "well-formed
	// UUID" on handle.TurnID.
	uuidV4Regex := `^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`

	setup := func(chunks []provider.StreamChunk, interval time.Duration) {
		drip = &dripStreamer{chunks: chunks, emitInterval: interval}
		mgr = session.NewManager(drip)
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})
		srv = api.NewServer(
			drip,
			reg,
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(mgr),
		)
		httpSrv = httptest.NewServer(srv.Handler())
	}

	AfterEach(func() {
		if httpSrv != nil {
			httpSrv.Close()
			httpSrv = nil
		}
	})

	// Helper that POSTs a message and decodes the response. Returns
	// (statusCode, decoded-body, raw-body) so individual specs can
	// assert on whichever shape they care about.
	postMessage := func(sessionID, content string) (int, map[string]any, []byte) {
		resp, err := http.Post( //nolint:noctx
			httpSrv.URL+"/api/v1/sessions/"+sessionID+"/messages",
			"application/json",
			strings.NewReader(`{"content":"`+content+`"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		if len(raw) > 0 && raw[0] == '{' {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out, raw
	}

	getTurn := func(sessionID, turnID string) (int, map[string]any, []byte) {
		resp, err := http.Get( //nolint:noctx
			httpSrv.URL + "/api/v1/sessions/" + sessionID + "/turns/" + turnID,
		)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		if len(raw) > 0 && raw[0] == '{' {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out, raw
	}

	It("POST /messages returns {turn_id, snapshot}", func() {
		setup([]provider.StreamChunk{{Content: "ok"}, {Done: true}}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		status, body, raw := postMessage(sess.ID, "hello")
		Expect(status).To(Equal(http.StatusOK))
		Expect(body).To(HaveKey("turn_id"), "POST response must include turn_id; got: %s", string(raw))
		Expect(body).To(HaveKey("snapshot"), "POST response must include snapshot; got: %s", string(raw))

		tid, ok := body["turn_id"].(string)
		Expect(ok).To(BeTrue(), "turn_id must decode as string; got: %s", string(raw))
		Expect(tid).To(MatchRegexp(uuidV4Regex),
			"turn_id must be a well-formed UUIDv4 minted by the registry; got %q", tid)

		snapshot, ok := body["snapshot"].(map[string]any)
		Expect(ok).To(BeTrue(), "snapshot must decode as object; got: %s", string(raw))
		msgs, ok := snapshot["messages"].([]any)
		Expect(ok).To(BeTrue(), "snapshot.messages must be a JSON array; got: %s", string(raw))

		var sawUser bool
		for _, m := range msgs {
			row, _ := m.(map[string]any)
			if row["role"] == "user" && row["content"] == "hello" {
				sawUser = true
				break
			}
		}
		Expect(sawUser).To(BeTrue(),
			"snapshot.messages must include the user message appended synchronously by Dispatcher; got: %s", string(raw))
	})

	It("GET /turns/{turn_id} returns turn state with status=running mid-stream", func() {
		// dripStreamer with a slow emit so the Turn stays Running
		// across the POST → GET window. 200ms per chunk is well above
		// the GET's round-trip overhead, making the mid-stream
		// observation deterministic.
		//
		// The chunks shape forces an intermediate AppendMessage call:
		// applyToolCall (accumulator.go:559) flushes any pending content
		// and persists a tool_call row immediately. Without an
		// intermediate flush, the accumulator would buffer content
		// until terminal Done and Turn.MessagesAdded would stay empty
		// mid-stream. The tool_call chunk is the canonical intermediate-
		// flush signal in production engine output (every multi-round
		// tool loop produces one).
		setup([]provider.StreamChunk{
			{ToolCall: &provider.ToolCall{Name: "search", Arguments: map[string]any{"q": "hi"}}},
			{Content: "tail"},
			{Done: true},
		}, 200*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		status, body, _ := postMessage(sess.ID, "hi")
		Expect(status).To(Equal(http.StatusOK))
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Poll until the registry has at least one engine-emitted row
		// for this turn. The Phase 1 accumulator integration appends
		// on every persisted chunk, so a Running turn with content
		// emitted will surface messages > 0 within the drip interval.
		var (
			gotStatus  string
			gotMsgs    []any
			lastGetRaw []byte
		)
		Eventually(func() bool {
			getStatus, getBody, getRaw := getTurn(sess.ID, turnID)
			lastGetRaw = getRaw
			if getStatus != http.StatusOK {
				return false
			}
			gotStatus, _ = getBody["status"].(string)
			gotMsgs, _ = getBody["messages"].([]any)
			return gotStatus == "running" && len(gotMsgs) >= 1
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} mid-stream must surface status=running with engine-emitted messages accumulating; last response: %s", string(lastGetRaw))

		Expect(gotStatus).To(Equal("running"))
		Expect(gotMsgs).NotTo(BeEmpty(),
			"engine-emitted rows must appear on Turn.MessagesAdded as they persist")
	})

	It("GET /turns/{turn_id} transitions to status=completed after stream finishes", func() {
		setup([]provider.StreamChunk{
			{Content: "done"},
			{Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "ping")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Poll for terminal state. The dispatcher's wrap goroutine
		// calls turnRegistry.Complete after the streamer's Done chunk
		// drains; within the drip's emit interval this happens in
		// well under a second.
		var (
			gotStatus  string
			gotCompAt  any
			gotMsgs    []any
			lastGetRaw []byte
		)
		Eventually(func() bool {
			st, getBody, raw := getTurn(sess.ID, turnID)
			lastGetRaw = raw
			if st != http.StatusOK {
				return false
			}
			gotStatus, _ = getBody["status"].(string)
			gotCompAt = getBody["completed_at"]
			gotMsgs, _ = getBody["messages"].([]any)
			return gotStatus == "completed"
		}, "5s", "30ms").Should(BeTrue(),
			"Turn must transition to status=completed after the streamer's Done chunk drains; last response: %s", string(lastGetRaw))

		Expect(gotCompAt).NotTo(BeNil(),
			"completed_at must be non-null once the turn reaches a terminal state")
		Expect(gotMsgs).NotTo(BeEmpty(),
			"a completed turn must surface its accumulated engine-emitted rows on messages[]")
	})

	It("GET /turns/{turn_id} reports status=failed on provider error", func() {
		// Streamer emits an error chunk so the wrap goroutine maps it
		// onto turnRegistry.Fail. Done:true with non-nil Error is the
		// canonical engine-side failure shape (see engine.go:3774-3776
		// and the dripStreamer ctx-cancel branch).
		failErr := errors.New("provider blew up")
		setup([]provider.StreamChunk{
			{Error: failErr, Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "fail-me")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		var (
			gotStatus  string
			gotErr     string
			lastGetRaw []byte
		)
		Eventually(func() bool {
			st, getBody, raw := getTurn(sess.ID, turnID)
			lastGetRaw = raw
			if st != http.StatusOK {
				return false
			}
			gotStatus, _ = getBody["status"].(string)
			gotErr, _ = getBody["error"].(string)
			return gotStatus == "failed"
		}, "5s", "30ms").Should(BeTrue(),
			"Turn must transition to status=failed when the streamer emits an error chunk; last response: %s", string(lastGetRaw))

		Expect(gotErr).NotTo(BeEmpty(),
			"a failed turn must carry the provider/engine error cause on error")
	})

	It("GET /turns/{unknown_id} returns 404", func() {
		setup([]provider.StreamChunk{{Done: true}}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		// Use a well-formed UUID that the registry has never seen.
		unknown := "00000000-0000-4000-8000-000000000000"
		st, _, _ := getTurn(sess.ID, unknown)
		Expect(st).To(Equal(http.StatusNotFound),
			"GET /turns/{turn_id} must 404 when the registry has never seen the id; pre-restart turn_ids are explicitly out-of-scope at v1")
	})

	It("MessagesAdded excludes the user message that triggered the turn", func() {
		setup([]provider.StreamChunk{
			{Content: "reply"},
			{Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "hello-user-trigger")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Wait for the turn to reach a terminal state so all
		// engine-emitted rows are accumulated.
		var msgs []any
		Eventually(func() string {
			_, getBody, _ := getTurn(sess.ID, turnID)
			msgs, _ = getBody["messages"].([]any)
			s, _ := getBody["status"].(string)
			return s
		}, "5s", "30ms").Should(Equal("completed"))

		for _, m := range msgs {
			row, ok := m.(map[string]any)
			if !ok {
				continue
			}
			Expect(row["role"]).NotTo(Equal("user"),
				"Turn.MessagesAdded MUST exclude the user message that triggered the turn — the user row lives in the POST snapshot, not the turn payload. Got row: %v", row)
			content, _ := row["content"].(string)
			Expect(content).NotTo(Equal("hello-user-trigger"),
				"the user-content trigger string MUST NOT appear in Turn.MessagesAdded")
		}
	})

	It("Concurrent POST during running turn returns HTTP 409", func() {
		// Slow drip so turn 1 stays Running while turn 2 fires. The
		// dispatcher's registry.Start is the gate: turn 2 sees a
		// Running entry for the sessionID and surfaces
		// dispatch.ErrTurnConflict, which the HTTP handler maps to 409.
		// This is the load-bearing predecessor pin for the ErrTurnConflict
		// → 409 mapping added in this commit.
		setup([]provider.StreamChunk{
			{Content: "slow-1"},
			{Content: "slow-2"},
			{Content: "slow-3"},
			{Done: true},
		}, 250*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		// Fire POST 1 — returns immediately (async POST per e4bf9632)
		// while the streamer continues to drip.
		st1, body1, _ := postMessage(sess.ID, "first")
		Expect(st1).To(Equal(http.StatusOK))
		turnID1, _ := body1["turn_id"].(string)
		Expect(turnID1).NotTo(BeEmpty())

		// Probe the registry: confirm turn 1 is Running before the
		// second POST fires, so the conflict-gate has something to
		// observe. Without this gate the race window can be empty
		// and POST 2 succeeds.
		Eventually(func() string {
			_, b, _ := getTurn(sess.ID, turnID1)
			s, _ := b["status"].(string)
			return s
		}, "2s", "10ms").Should(Equal("running"),
			"turn 1 must register as Running in the Turn registry before the conflict-POST fires; otherwise the conflict-gate has nothing to observe")

		// POST 2 — must surface 409 Conflict because turn 1 is
		// still Running. v1 supports ONE in-flight turn per session.
		st2, _, raw2 := postMessage(sess.ID, "second")
		Expect(st2).To(Equal(http.StatusConflict),
			"a second POST while turn 1 is StatusRunning must return HTTP 409 Conflict — dispatch.ErrTurnConflict must map to http.StatusConflict in handleSessionMessage. Got body: %s", string(raw2))
	})

	// §AC#13 absolute (active-send SLO). Phase 4 / Phase B re-verification
	// note: the original plan-text SLO was a Playwright spec asserting
	// "time-from-submit to first SSE chunk <500ms". Playwright runs against
	// a real backend with real provider TTFB (5-30s); the SLO is not
	// measurable at that surface. The bottleneck on the poll path is the
	// backend's chunk-to-broadcast latency, which dripStreamer reproduces
	// faithfully (in-process Go-only — the Vue commit phase is sub-16ms
	// and not on this critical path). Promoting the SLO to Go-level keeps
	// the mechanical guarantee while making the measurement deterministic
	// in CI. The §AC#13 budget: <600ms p95 from POST 200 to first
	// non-empty GET /turns/{id} messages[] payload, measured across 50
	// trials.
	//
	// Commit 2 of "Turn-Based Post-Then-Poll Architecture (May 2026)" —
	// once SSE / WS / broker are retired, long-poll is the sole live
	// channel. This spec pins the perceived-cadence SLO at the seam that
	// matters: the time between POST returning and the FE having a
	// renderable chunk via the poll path.
	It("§AC#13 — active-send first-chunk SLO: <600ms p95 from POST→first non-empty GET poll across 50 trials", func() {
		const trials = 50
		samples := make([]time.Duration, 0, trials)
		for i := 0; i < trials; i++ {
			// Fresh fixture per trial — dripStreamer state is per-instance
			// and a shared instance would emit each chunk only once across
			// the whole loop. 30ms interval is the canonical broker-side
			// "first chunk lands in ~30-130ms" shape from the production
			// envelope; the SLO targets the broadcast-to-visible window,
			// not the streamer's emit interval itself.
			setup([]provider.StreamChunk{
				{Content: "first"},
				{Content: "second"},
				{Done: true},
			}, 30*time.Millisecond)

			sess, err := mgr.CreateSession("default-assistant")
			Expect(err).NotTo(HaveOccurred())

			postStart := time.Now()
			status, body, _ := postMessage(sess.ID, fmt.Sprintf("trial-%d", i))
			Expect(status).To(Equal(http.StatusOK))
			turnID, _ := body["turn_id"].(string)
			Expect(turnID).NotTo(BeEmpty())

			// Tight cadence GET loop so the measurement reflects "as fast
			// as the FE could possibly observe a non-empty messages[]".
			// 5ms is well under the 30ms drip interval so the timing
			// resolution is dominated by the broadcast latency, not the
			// poll cadence.
			var firstChunkAt time.Time
			Eventually(func() bool {
				_, getBody, _ := getTurn(sess.ID, turnID)
				msgs, _ := getBody["messages"].([]any)
				if len(msgs) >= 1 {
					firstChunkAt = time.Now()
					return true
				}
				return false
			}, "3s", "5ms").Should(BeTrue(),
				"trial %d: poll path must surface a non-empty messages[] within 3s for a 30ms drip; otherwise the SLO measurement is meaningless", i)

			samples = append(samples, firstChunkAt.Sub(postStart))

			// Tear down the per-trial httptest.Server so the next trial's
			// setup() builds a fresh server bound to a fresh mux. Without
			// this each trial leaks a goroutine + listening socket.
			httpSrv.Close()
			httpSrv = nil
		}

		sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
		// p95 index: ceil(0.95 * N) - 1 on a sorted slice. For N=50:
		// ceil(47.5) = 48, index 47 — the 48th-smallest sample.
		p95 := samples[47]
		// Surface the distribution for the post-task report. Logged via
		// GinkgoWriter so it shows under `--v` without polluting normal
		// runs.
		GinkgoWriter.Printf("§AC#13 active-send first-chunk SLO: p50=%v p95=%v p100=%v across %d trials\n",
			samples[trials/2], p95, samples[trials-1], trials)
		Expect(p95).To(BeNumerically("<", 600*time.Millisecond),
			"§AC#13 SLO: time from POST /messages 200 to first non-empty GET /turns/{id} messages[] must be <600ms at p95 across %d trials. Got p95=%v, p100=%v",
			trials, p95, samples[trials-1])
	})

	// §AC#11 absolute (reattach SLO). The original plan-text SLO was a
	// Playwright spec asserting "open SSE against an in-flight session
	// → first chunk within 1.5s". Promoted to Go-level for the same
	// reason as §AC#13 above. The reattach surface in the post-Commit-2
	// world is: GET /turns/{id} against an in-flight Turn started ~500ms
	// ago — the response MUST surface the accumulated messages[] within
	// 1.5s p95 across 50 trials. This pins the refresh-mid-stream UX:
	// a fresh page nav while a turn is running must render the partial
	// reply within a perceptible window.
	It("§AC#11 — reattach first-chunk SLO: <1.5s p95 from in-flight reattach GET to first non-empty messages[] across 50 trials", func() {
		const trials = 50
		samples := make([]time.Duration, 0, trials)
		for i := 0; i < trials; i++ {
			// Long enough turn that the reattach GET fires WHILE the turn
			// is still Running. 200ms drip with 3 chunks = ~600ms total
			// turn duration; the reattach lands at the ~500ms mark when
			// at least one chunk has accumulated AND the turn is still
			// Running. Done:true is intentionally last so terminal-state
			// short-circuit on getTurnLongPoll is exercised only as a
			// fallback.
			setup([]provider.StreamChunk{
				{Content: "alpha"},
				{Content: "beta"},
				{Content: "gamma"},
				{Done: true},
			}, 200*time.Millisecond)

			sess, err := mgr.CreateSession("default-assistant")
			Expect(err).NotTo(HaveOccurred())

			_, body, _ := postMessage(sess.ID, fmt.Sprintf("trial-%d", i))
			turnID, _ := body["turn_id"].(string)
			Expect(turnID).NotTo(BeEmpty())

			// Wait 500ms — this is the "user refreshed the page mid-stream"
			// simulacrum. At ~500ms the dripStreamer has emitted ~2 chunks,
			// the Turn is still Running, and a fresh GET represents the
			// reattach surface.
			time.Sleep(500 * time.Millisecond)

			reattachStart := time.Now()
			var firstReattachAt time.Time
			Eventually(func() bool {
				_, getBody, _ := getTurn(sess.ID, turnID)
				msgs, _ := getBody["messages"].([]any)
				if len(msgs) >= 1 {
					firstReattachAt = time.Now()
					return true
				}
				return false
			}, "3s", "5ms").Should(BeTrue(),
				"trial %d: reattach GET must surface a non-empty messages[] within 3s", i)

			samples = append(samples, firstReattachAt.Sub(reattachStart))

			// Wait for the turn to finish before closing — otherwise the
			// closed httptest.Server pulls the rug out from under the
			// in-flight Publish goroutine and produces spurious test-only
			// teardown noise that doesn't affect production semantics but
			// pollutes the run output.
			Eventually(func() string {
				_, b, _ := getTurn(sess.ID, turnID)
				s, _ := b["status"].(string)
				return s
			}, "5s", "30ms").Should(Or(Equal("completed"), Equal("failed")))

			httpSrv.Close()
			httpSrv = nil
		}

		sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
		p95 := samples[47]
		GinkgoWriter.Printf("§AC#11 reattach first-chunk SLO: p50=%v p95=%v p100=%v across %d trials\n",
			samples[trials/2], p95, samples[trials-1], trials)
		Expect(p95).To(BeNumerically("<", 1500*time.Millisecond),
			"§AC#11 SLO: reattach GET against an in-flight turn started ~500ms ago must surface a non-empty messages[] within 1.5s at p95 across %d trials. Got p95=%v, p100=%v",
			trials, p95, samples[trials-1])
	})
})

// Phase-4-Commit-1 RED gate per "Turn-Based Post-Then-Poll Architecture
// (May 2026)" §4d Commit 1. Two surfaces ship in this slice:
//
//   - GET /api/v1/sessions exposes `activeTurnId` on each Summary
//     during a Running turn (populated via registry.FindActiveBySession
//     at handleListV1Sessions). Empty for idle sessions.
//   - GET /api/v1/sessions/{id}/turns/{turn_id} carries `phase` +
//     `token_count` fields the engine's `events.EventStreamingHeartbeat`
//     bus subscription writes onto the Turn via registry.SetHeartbeat.
//     `phase` transitions across polls; `token_count` grows monotonically.
//
// The new bus subscriber sits ALONGSIDE the existing SSE-side bridge
// at internal/api/event_bridge.go:46 — Commit 2 retires that bridge,
// not Commit 1. Tests here exercise the polling-side wiring only.
var _ = Describe("Phase-4-Commit-1 — activeTurnId + heartbeat-on-turn", func() {
	var (
		drip    *dripStreamer
		mgr     *session.Manager
		bus     *eventbus.EventBus
		srv     *api.Server
		httpSrv *httptest.Server
		reg     *agent.Registry
	)

	setup := func(chunks []provider.StreamChunk, interval time.Duration) {
		drip = &dripStreamer{chunks: chunks, emitInterval: interval}
		mgr = session.NewManager(drip)
		bus = eventbus.NewEventBus()
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})
		srv = api.NewServer(
			drip,
			reg,
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(mgr),
			api.WithEventBus(bus),
		)
		httpSrv = httptest.NewServer(srv.Handler())
	}

	AfterEach(func() {
		if httpSrv != nil {
			httpSrv.Close()
			httpSrv = nil
		}
	})

	postMessage := func(sessionID, content string) (int, map[string]any, []byte) {
		resp, err := http.Post( //nolint:noctx
			httpSrv.URL+"/api/v1/sessions/"+sessionID+"/messages",
			"application/json",
			strings.NewReader(`{"content":"`+content+`"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		if len(raw) > 0 && raw[0] == '{' {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out, raw
	}

	getTurn := func(sessionID, turnID string) (int, map[string]any, []byte) {
		resp, err := http.Get( //nolint:noctx
			httpSrv.URL + "/api/v1/sessions/" + sessionID + "/turns/" + turnID,
		)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		if len(raw) > 0 && raw[0] == '{' {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out, raw
	}

	listSessions := func() (int, []map[string]any, []byte) {
		resp, err := http.Get(httpSrv.URL + "/api/v1/sessions") //nolint:noctx
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		var out []map[string]any
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out, raw
	}

	It("GET /api/v1/sessions exposes activeTurnId matching the in-flight turn's UUID mid-stream", func() {
		// 250ms drip keeps the turn Running across the POST → list
		// window. Tool-call mid-stream forces the accumulator to flush
		// so we know the engine pipeline is mid-turn (same shape the
		// Phase 2 RED specs use to observe a Running turn).
		setup([]provider.StreamChunk{
			{ToolCall: &provider.ToolCall{Name: "search", Arguments: map[string]any{"q": "hi"}}},
			{Content: "tail"},
			{Done: true},
		}, 250*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		st, body, _ := postMessage(sess.ID, "hi")
		Expect(st).To(Equal(http.StatusOK))
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Poll the list endpoint until the summary surfaces activeTurnId.
		// The handler reads via registry.FindActiveBySession at L1138.
		var (
			gotActive  string
			lastListRaw []byte
		)
		Eventually(func() string {
			listStatus, summaries, raw := listSessions()
			lastListRaw = raw
			if listStatus != http.StatusOK {
				return ""
			}
			for _, sum := range summaries {
				if sum["id"] != sess.ID {
					continue
				}
				v, _ := sum["activeTurnId"].(string)
				gotActive = v
				return v
			}
			return ""
		}, "3s", "20ms").Should(Equal(turnID),
			"GET /api/v1/sessions must surface activeTurnId matching the POST-returned turn UUID while the turn is Running; last list response: %s", string(lastListRaw))
		Expect(gotActive).To(Equal(turnID))
	})

	It("GET /api/v1/sessions exposes activeTurnId as empty string for idle sessions", func() {
		setup([]provider.StreamChunk{{Done: true}}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		// No POST — session is idle. The list response's summary must
		// surface activeTurnId="" (NOT missing key). The wire contract
		// gates polling on `if (snapshot.activeTurnId)` so an absent
		// key is functionally equivalent — the camelCase key MUST be
		// present even when empty.
		listStatus, summaries, raw := listSessions()
		Expect(listStatus).To(Equal(http.StatusOK))

		var seenIdle bool
		for _, sum := range summaries {
			if sum["id"] != sess.ID {
				continue
			}
			seenIdle = true
			// The Summary's IsStreaming preserves backward-compat —
			// activeTurnId is the new sibling. Both fields must be
			// present on the wire.
			Expect(sum).To(HaveKey("activeTurnId"),
				"the field must appear on the wire for every summary, even when empty — clients reading sum.activeTurnId must see a defined string, not undefined; got: %s", string(raw))
			v, _ := sum["activeTurnId"].(string)
			Expect(v).To(BeEmpty(),
				"idle session — no running turn — activeTurnId must be \"\"; got: %q. Raw: %s", v, string(raw))
		}
		Expect(seenIdle).To(BeTrue(), "session must appear in the list; raw: %s", string(raw))
	})

	It("GET /turns/{turn_id} surfaces phase + token_count populated by the engine-bus heartbeat subscriber", func() {
		// dripStreamer with a slow emit so the Turn stays Running
		// across multiple heartbeat publications. The new bus
		// subscriber wired in handleSessionMessage / NewServer reads
		// turnID via registry.FindActiveBySession(sessionID) on every
		// EventStreamingHeartbeat and calls registry.SetHeartbeat.
		setup([]provider.StreamChunk{
			{Content: "step-1"},
			{Content: "step-2"},
			{Done: true},
		}, 250*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		st, body, raw := postMessage(sess.ID, "go")
		Expect(st).To(Equal(http.StatusOK))
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty(), "POST must return turn_id; raw: %s", string(raw))

		// Publish heartbeats on the bus directly. In production the
		// engine's runStreamingHeartbeat ticker fires these — the
		// engine wiring is out of scope for the api package; we drive
		// the bus from the test to exercise the polling-side wiring
		// (the new subscriber) deterministically.
		//
		// Two ticks at different (phase, tokenCount) values prove the
		// fields are last-write-wins on Get and that tokenCount can
		// grow across polls (monotonic semantics).
		bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
			SessionID:  sess.ID,
			AgentID:    "default-assistant",
			Phase:      "thinking",
			TokenCount: 100,
		}))

		var (
			gotPhase   string
			gotTokens  float64
			lastGetRaw []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			p, _ := gbody["phase"].(string)
			t, _ := gbody["token_count"].(float64) // json numbers decode as float64
			gotPhase = p
			gotTokens = t
			return gotPhase == "thinking" && gotTokens == 100
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface phase=\"thinking\" and token_count=100 after the bus heartbeat publishes; the polling-side subscriber must call registry.SetHeartbeat. Last GET response: %s", string(lastGetRaw))

		// Second heartbeat — different phase, higher token count.
		// Both fields must update; token_count grows monotonically.
		bus.Publish(events.EventStreamingHeartbeat, events.NewStreamingHeartbeatEvent(events.StreamingHeartbeatEventData{
			SessionID:  sess.ID,
			AgentID:    "default-assistant",
			Phase:      "generating",
			TokenCount: 250,
		}))

		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			p, _ := gbody["phase"].(string)
			t, _ := gbody["token_count"].(float64)
			gotPhase = p
			gotTokens = t
			return gotPhase == "generating" && gotTokens == 250
		}, "3s", "20ms").Should(BeTrue(),
			"the second heartbeat must overwrite both fields — phase=\"generating\", token_count=250. Polling-side wire must let the chat UI's chip + live counter chrome tick on every poll without an SSE side-channel. Last GET response: %s", string(lastGetRaw))

		Expect(gotTokens).To(BeNumerically(">", float64(100)),
			"token_count must grow monotonically across heartbeats — the chat UI's tokens-per-second computation relies on this")
	})

	// Phase-5 §1c-α: the turnResponse wire surface now exposes
	// `current_provider` + `current_model` so the FE's poll loop can pivot
	// the toolbar chip on the live (provider, model) pair without waiting
	// for the SSE side-channel. The dispatcher's wrapWithTurnLifecycle taps
	// `model_active` and `provider_changed` chunks and calls
	// registry.SetProviderModel; the long-poll Get path surfaces those
	// fields verbatim on every snapshot.
	It("GET /turns/{turn_id} exposes current_provider + current_model after model_active lands", func() {
		setup([]provider.StreamChunk{
			{
				EventType:  "model_active",
				ModelID:    "claude-opus-4-7",
				ProviderID: "anthropic",
			},
			{Content: "ack", ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
			{Done: true, ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		st, body, raw := postMessage(sess.ID, "go")
		Expect(st).To(Equal(http.StatusOK), "POST raw: %s", string(raw))
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		var (
			gotProvider string
			gotModel    string
			lastGetRaw  []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			p, _ := gbody["current_provider"].(string)
			m, _ := gbody["current_model"].(string)
			gotProvider = p
			gotModel = m
			return p == "anthropic" && m == "claude-opus-4-7"
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface current_provider=anthropic + current_model=claude-opus-4-7 after the dispatcher's chunk-tap fires SetProviderModel — Phase-5 §1c-α wire contract. Last GET response: %s", string(lastGetRaw))

		Expect(gotProvider).To(Equal("anthropic"))
		Expect(gotModel).To(Equal("claude-opus-4-7"))
	})

	// Phase-5 §1c-β: the turnResponse wire surface now also exposes
	// `context_usage` and `provider_quotas` so the FE's poll loop can pivot
	// the context-usage chip and the provider-quota chip on the live figure
	// / per-partition snapshot without an SSE side-channel.
	It("GET /turns/{turn_id} exposes context_usage after a context_usage chunk lands", func() {
		setup([]provider.StreamChunk{
			{
				EventType: "context_usage",
				Content:   `{"input_tokens":1234,"output_reserve":8192,"limit":200000,"percentage":1,"provider":"anthropic","model":"claude-opus-4-7"}`,
			},
			{Content: "ack", ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
			{Done: true, ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		st, body, raw := postMessage(sess.ID, "go")
		Expect(st).To(Equal(http.StatusOK), "POST raw: %s", string(raw))
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		var (
			gotInputTokens float64
			gotLimit       float64
			lastGetRaw     []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			cu, ok := gbody["context_usage"].(map[string]interface{})
			if !ok {
				return false
			}
			inputTokens, _ := cu["input_tokens"].(float64)
			limit, _ := cu["limit"].(float64)
			gotInputTokens = inputTokens
			gotLimit = limit
			return inputTokens == 1234 && limit == 200000
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface context_usage{input_tokens=1234, limit=200000} after the dispatcher's chunk-tap fires SetContextUsage — Phase-5 §1c-β wire contract. Last GET response: %s", string(lastGetRaw))

		Expect(gotInputTokens).To(Equal(float64(1234)))
		Expect(gotLimit).To(Equal(float64(200000)))
	})

	It("GET /turns/{turn_id} exposes provider_quotas after two provider_quota chunks (same-key replaces, different-key appends)", func() {
		setup([]provider.StreamChunk{
			{
				EventType: "provider_quota",
				Content:   `{"provider":"anthropic","account_hash":"acc-1","model":"claude-opus-4-7","observed_at":"2026-05-19T00:00:00Z","variant":"token_spend","token_spend":{"spent_minor":1000,"spent_currency":"USD","period":"monthly","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-31T23:59:59Z","threshold_amber":70,"threshold_red":90}}`,
			},
			{
				EventType: "provider_quota",
				Content:   `{"provider":"anthropic","account_hash":"acc-1","model":"claude-opus-4-7","observed_at":"2026-05-19T00:00:01Z","variant":"token_spend","token_spend":{"spent_minor":2500,"spent_currency":"USD","period":"monthly","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-31T23:59:59Z","threshold_amber":70,"threshold_red":90}}`,
			},
			{
				EventType: "provider_quota",
				Content:   `{"provider":"zai","account_hash":"acc-z","model":"glm-4.6","observed_at":"2026-05-19T00:00:02Z","variant":"token_spend","token_spend":{"spent_minor":500,"spent_currency":"USD","period":"monthly","period_start":"2026-05-01T00:00:00Z","period_end":"2026-05-31T23:59:59Z","threshold_amber":70,"threshold_red":90}}`,
			},
			{Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "go")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		var (
			gotLen     int
			anthSpent  float64
			zaiSpent   float64
			lastGetRaw []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			rawList, ok := gbody["provider_quotas"].([]interface{})
			if !ok {
				return false
			}
			gotLen = len(rawList)
			if gotLen != 2 {
				return false
			}
			for _, raw := range rawList {
				entry, _ := raw.(map[string]interface{})
				provider, _ := entry["provider"].(string)
				ts, _ := entry["token_spend"].(map[string]interface{})
				if ts == nil {
					continue
				}
				sm, _ := ts["spent_minor"].(float64)
				if provider == "anthropic" {
					anthSpent = sm
				}
				if provider == "zai" {
					zaiSpent = sm
				}
			}
			return anthSpent == 2500 && zaiSpent == 500
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface provider_quotas with len=2 (same-key REPLACED, different-key APPENDED), anthropic.spent_minor=2500 (latest), zai.spent_minor=500. Phase-5 §1c-β wire contract. Last GET response: %s", string(lastGetRaw))

		Expect(gotLen).To(Equal(2))
		Expect(anthSpent).To(Equal(float64(2500)))
		Expect(zaiSpent).To(Equal(float64(500)))
	})

	It("GET /turns/{turn_id} reflects a mid-stream failover via provider_changed (current_provider pivots)", func() {
		setup([]provider.StreamChunk{
			{
				EventType:  "model_active",
				ModelID:    "claude-opus-4-7",
				ProviderID: "anthropic",
			},
			{Content: "primary-ack", ModelID: "claude-opus-4-7", ProviderID: "anthropic"},
			{
				EventType:  "provider_changed",
				ModelID:    "glm-4.6",
				ProviderID: "zai",
			},
			{Content: "fallback-ack", ModelID: "glm-4.6", ProviderID: "zai"},
			{Done: true, ModelID: "glm-4.6", ProviderID: "zai"},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "trigger-failover")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Wait for the FINAL pair to land — the wrap drains both
		// announcements in order, the registry's broadcast wakes long-poll
		// waiters on each transition.
		var lastGetRaw []byte
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			p, _ := gbody["current_provider"].(string)
			m, _ := gbody["current_model"].(string)
			return p == "zai" && m == "glm-4.6"
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must reflect the POST-FAILOVER provider/model — current_provider pivots from anthropic to zai when the engine emits provider_changed; the chip then pivots without an SSE side-channel. Last GET response: %s", string(lastGetRaw))
	})

	// Phase-5 §1c-γ: the turnResponse wire surface now also exposes
	// `compaction_events`, `gate_failures`, and `critical_error` so the
	// FE's poll loop can pivot the chip flash, GateFailureBanner, and
	// CriticalErrorBanner on bus-driven (compaction, gate_failed) and
	// chunk-driven (stream_critical) signals respectively. The
	// context_compacted + gate_failed subscribers wired in NewServer
	// project bus events onto AppendCompactionEvent / AppendGateFailure;
	// the dispatcher's chunk-tap handles critical errors via
	// SetCriticalError.
	It("GET /turns/{turn_id} exposes compaction_events after EventContextCompacted publishes", func() {
		// dripStreamer with a slow emit so the Turn stays Running
		// across the bus publication.
		setup([]provider.StreamChunk{
			{Content: "step-1"},
			{Content: "step-2"},
			{Done: true},
		}, 250*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		st, body, raw := postMessage(sess.ID, "go")
		Expect(st).To(Equal(http.StatusOK))
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty(), "POST must return turn_id; raw: %s", string(raw))

		// Publish a context_compacted on the bus — in production the
		// engine's L2 auto-compactor fires this. The new subscriber
		// reads session_id → turn_id via the registry and calls
		// AppendCompactionEvent.
		bus.Publish(events.EventContextCompacted, events.NewContextCompactedEvent(events.ContextCompactedEventData{
			SessionID:      sess.ID,
			AgentID:        "default-assistant",
			OriginalTokens: 10000,
			SummaryTokens:  2000,
			LatencyMS:      42,
			Trigger:        "ratio",
		}))

		var (
			gotLen     int
			gotOrig    float64
			gotSum     float64
			gotTrig    string
			lastGetRaw []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			rawList, ok := gbody["compaction_events"].([]interface{})
			if !ok {
				return false
			}
			gotLen = len(rawList)
			if gotLen == 0 {
				return false
			}
			entry, _ := rawList[0].(map[string]interface{})
			gotOrig, _ = entry["original_tokens"].(float64)
			gotSum, _ = entry["summary_tokens"].(float64)
			gotTrig, _ = entry["trigger"].(string)
			return gotOrig == 10000 && gotSum == 2000
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface compaction_events[0] with original_tokens=10000, summary_tokens=2000 after the bus subscriber appends — Phase-5 §1c-γ wire contract. Last GET response: %s", string(lastGetRaw))

		Expect(gotLen).To(Equal(1))
		Expect(gotOrig).To(Equal(float64(10000)))
		Expect(gotSum).To(Equal(float64(2000)))
		Expect(gotTrig).To(Equal("ratio"),
			"the Trigger discriminant must round-trip from bus → registry → wire so the FE chip's tooltip can render the cause")
	})

	It("GET /turns/{turn_id} exposes gate_failures after EventGateFailed publishes", func() {
		setup([]provider.StreamChunk{
			{Content: "step-1"},
			{Content: "step-2"},
			{Done: true},
		}, 250*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "go")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		bus.Publish(events.EventGateFailed, events.NewGateFailedEvent(events.GateEventData{
			SwarmID:        "a-team",
			SessionID:      sess.ID,
			Lifecycle:      "post-member",
			MemberID:       "member-1",
			GateName:       "relevance",
			GateKind:       "ext:relevance-gate",
			Reason:         "off-topic",
			Cause:          "runner exited non-zero",
			CoordStoreKeys: []string{"key-a", "key-b"},
		}))

		var (
			gotLen     int
			gotName    string
			gotReason  string
			gotKeys    []interface{}
			lastGetRaw []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			rawList, ok := gbody["gate_failures"].([]interface{})
			if !ok {
				return false
			}
			gotLen = len(rawList)
			if gotLen == 0 {
				return false
			}
			entry, _ := rawList[0].(map[string]interface{})
			gotName, _ = entry["gate_name"].(string)
			gotReason, _ = entry["reason"].(string)
			gotKeys, _ = entry["coord_store_keys"].([]interface{})
			return gotName == "relevance" && gotReason == "off-topic"
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface gate_failures[0] with gate_name=relevance after the bus subscriber appends — Phase-5 §1c-γ wire contract. Last GET response: %s", string(lastGetRaw))

		Expect(gotLen).To(Equal(1))
		Expect(gotName).To(Equal("relevance"))
		Expect(gotReason).To(Equal("off-topic"))
		Expect(gotKeys).To(HaveLen(2),
			"CoordStoreKeys must round-trip so the banner's 'what was checked?' expander has data to render")
	})

	It("GET /turns/{turn_id} exposes critical_error after the engine emits a SeverityCritical chunk.Error", func() {
		// The engine's classifier sees provider.ErrorTypeAuthFailure
		// as SeverityCritical (per severityFromProviderErrorType). The
		// dispatcher's chunk-tap mints the safeMsg + correlation_id
		// using the same logic internal/api/errors.go uses.
		critErr := &provider.Error{
			Provider:  "anthropic",
			ErrorType: provider.ErrorTypeAuthFailure,
			Message:   "401 unauthorized",
		}
		setup([]provider.StreamChunk{
			{Content: "partial"},
			{Error: critErr, Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "go")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		var (
			gotMsg     string
			gotCID     string
			gotSev     string
			lastGetRaw []byte
		)
		Eventually(func() bool {
			gst, gbody, graw := getTurn(sess.ID, turnID)
			lastGetRaw = graw
			if gst != http.StatusOK {
				return false
			}
			ce, ok := gbody["critical_error"].(map[string]interface{})
			if !ok {
				return false
			}
			gotMsg, _ = ce["message"].(string)
			gotCID, _ = ce["correlation_id"].(string)
			gotSev, _ = ce["severity"].(string)
			return gotMsg == "critical stream error" && gotCID != ""
		}, "3s", "20ms").Should(BeTrue(),
			"GET /turns/{turn_id} must surface critical_error.message='critical stream error' + a non-empty correlation_id after the chunk-tap classifies SeverityCritical — Phase-5 §1c-γ wire contract. Last GET response: %s", string(lastGetRaw))

		Expect(gotMsg).To(Equal("critical stream error"),
			"the safeMsg must be the same sanitised category text internal/api/errors.go uses — never the raw provider error")
		Expect(gotCID).To(MatchRegexp(`^[0-9a-f]{16}$`),
			"correlation_id must be 16 hex chars (8 random bytes) — matches the SSE wire shape so support tickets are indistinguishable across the two surfaces")
		Expect(gotSev).To(Equal("critical"))
	})
})

// Phase-4-Commit-1b RED gate per "Turn-Based Post-Then-Poll Architecture
// (May 2026)" §4d Commit 1b. The long-poll endpoint shape:
//
//   GET /api/v1/sessions/{id}/turns/{turn_id}?wait=true&since=N
//
// Replaces the FE's 250ms-cadence polling loop with a server-side hold
// so each chunk surfaces in the FE within the broadcast latency
// (sub-50ms perceived) instead of the ~125ms-average polling-window
// delay.
//
// Hold semantics:
//   - wait=true + since=N — hold until len(MessagesAdded) > N, OR
//     Phase / TokenCount move, OR Status leaves Running, OR 25s.
//   - wait absent / wait=false — return the current snapshot
//     immediately (backwards compat for any pre-1b client).
//
// Critical headers for nginx / proxy compat:
//   - X-Accel-Buffering: no
//   - Cache-Control: no-cache, no-store, must-revalidate
//
// Client-disconnect: handler watches r.Context().Done() — when the FE
// AbortController fires (session switch, page nav), the wait aborts
// cleanly without writing a stale body.
var _ = Describe("Phase-4-Commit-1b — long-poll Turn endpoint (wait=true)", func() {
	var (
		drip    *dripStreamer
		mgr     *session.Manager
		srv     *api.Server
		httpSrv *httptest.Server
		reg     *agent.Registry
	)

	setup := func(chunks []provider.StreamChunk, interval time.Duration) {
		drip = &dripStreamer{chunks: chunks, emitInterval: interval}
		mgr = session.NewManager(drip)
		reg = agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})
		srv = api.NewServer(
			drip,
			reg,
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(mgr),
		)
		httpSrv = httptest.NewServer(srv.Handler())
	}

	AfterEach(func() {
		if httpSrv != nil {
			httpSrv.Close()
			httpSrv = nil
		}
	})

	postMessage := func(sessionID, content string) (int, map[string]any, []byte) {
		resp, err := http.Post( //nolint:noctx
			httpSrv.URL+"/api/v1/sessions/"+sessionID+"/messages",
			"application/json",
			strings.NewReader(`{"content":"`+content+`"}`),
		)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		var out map[string]any
		if len(raw) > 0 && raw[0] == '{' {
			_ = json.Unmarshal(raw, &out)
		}
		return resp.StatusCode, out, raw
	}

	// getTurnLongPoll fires a GET with ?wait=true&since=N. Caller-provided
	// ctx lets specs cancel mid-wait to exercise the client-disconnect
	// branch. Returns the raw http.Response so headers (X-Accel-Buffering,
	// Cache-Control) can be asserted on the long-poll path.
	getTurnLongPoll := func(ctx context.Context, sessionID, turnID string, since int) (*http.Response, []byte, error) {
		url := fmt.Sprintf("%s/api/v1/sessions/%s/turns/%s?wait=true&since=%d",
			httpSrv.URL, sessionID, turnID, since)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, nil, err
		}
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp, raw, nil
	}

	It("wait=true holds until a mutation arrives, then returns the fresh snapshot (sub-50ms after broadcast)", func() {
		// Slow drip — turn stays Running well past the GET's start,
		// then a chunk lands and the wait must wake within
		// broadcast-latency (target <50ms perceived).
		setup([]provider.StreamChunk{
			{Content: "first"},
			{Content: "second"},
			{Done: true},
		}, 250*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "hi")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Issue the long-poll with since=0. The Turn has 0 engine-emitted
		// rows at this point (the first chunk arrives 250ms after POST).
		// The wait must hold, then wake when the accumulator appends the
		// first assistant row.
		start := time.Now()
		resp, raw, err := getTurnLongPoll(context.Background(), sess.ID, turnID, 0)
		Expect(err).NotTo(HaveOccurred())
		elapsed := time.Since(start)

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("X-Accel-Buffering")).To(Equal("no"),
			"long-poll responses must set X-Accel-Buffering: no so nginx / proxies don't buffer the body for an extra round-trip; got headers: %v", resp.Header)
		Expect(resp.Header.Get("Cache-Control")).To(ContainSubstring("no-cache"),
			"Cache-Control must include no-cache so intermediate proxies / browsers don't cache the snapshot — every long-poll response is uniquely-keyed by time-of-wake")

		var decoded map[string]any
		Expect(json.Unmarshal(raw, &decoded)).To(Succeed(),
			"body must decode as JSON; got: %s", string(raw))
		msgs, _ := decoded["messages"].([]any)
		Expect(len(msgs)).To(BeNumerically(">=", 1),
			"the wake-on-Append snapshot must carry at least one engine-emitted row — the wait predicate is len > since (0)")

		// Long-poll perceived-cadence pin: the wait must wake within
		// the broadcast-latency window after the chunk arrives, NOT
		// sit at the 250ms polling-loop boundary. The drip emits the
		// first chunk 250ms after Stream starts; the broadcast fires
		// inside Append; we should see the wake within ~50-150ms slack
		// (250ms drip delay + scheduler jitter + handler return cost).
		// We assert the elapsed time stays comfortably under the 25s
		// long-poll timeout to prove the wake came from the broadcast,
		// not the timeout.
		Expect(elapsed).To(BeNumerically("<", 2*time.Second),
			"the long-poll wake must arrive shortly after the first chunk's broadcast (~250ms drip + broadcast latency), NOT at the 25s timeout — elapsed was %v", elapsed)
	})

	It("wait=true returns the current snapshot at the 25s timeout when no mutation lands", func() {
		// No chunks emitted — POST mints a turn but the drip never
		// produces a chunk (interval > the test budget). The wait must
		// return at the test-shortened timeout with the current snapshot.
		//
		// We can't override the server-side longPollTimeout from the
		// outside; instead we abort the client AFTER a short window so
		// the test runs in bounded time without depending on the
		// production 25s constant. The handler's ctx-cancel branch
		// returns the zero snapshot — we assert that path separately
		// below ("client disconnects mid-wait").
		//
		// For the timeout-elapsed branch specifically, we'd need to
		// shrink longPollTimeout. The "wait returns idempotent
		// snapshot on a completed-before-wait turn" spec below covers
		// the equivalent fast-return path.
		Skip("server-side longPollTimeout is a constant; the registry-level WaitForChange timeout path is exercised in internal/turn/turn_test.go::WaitForChange::timeout-elapsed")
	})

	It("wait=true returns immediately (no hang) when the Turn already reached terminal state before the wait started", func() {
		// Single completing chunk so the Turn is Completed by the time
		// we issue the long-poll. The wait MUST surface the terminal
		// snapshot without sitting on the 25s timer.
		setup([]provider.StreamChunk{
			{Content: "done-immediately"},
			{Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "ping")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Wait for the turn to reach completed state via a fast probe.
		Eventually(func() string {
			resp, raw, _ := getTurnLongPoll(context.Background(), sess.ID, turnID, 0)
			if resp == nil || resp.StatusCode != http.StatusOK {
				return ""
			}
			var decoded map[string]any
			_ = json.Unmarshal(raw, &decoded)
			s, _ := decoded["status"].(string)
			return s
		}, "3s", "30ms").Should(Equal("completed"))

		// Now the Turn is terminal. A subsequent wait MUST return
		// immediately (no 25s hang) with the terminal snapshot. We
		// time this strictly.
		start := time.Now()
		resp, raw, err := getTurnLongPoll(context.Background(), sess.ID, turnID, 0)
		Expect(err).NotTo(HaveOccurred())
		elapsed := time.Since(start)
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var decoded map[string]any
		Expect(json.Unmarshal(raw, &decoded)).To(Succeed())
		Expect(decoded["status"]).To(Equal("completed"))
		Expect(elapsed).To(BeNumerically("<", 500*time.Millisecond),
			"a terminal-state Turn must surface its snapshot synchronously — anything past 500ms means the handler is waiting against a baseline check that doesn't short-circuit on Status != Running. Elapsed: %v", elapsed)
	})

	It("wait=true aborts cleanly when the client disconnects mid-wait", func() {
		// Slow drip so the turn stays Running well past our cancel.
		setup([]provider.StreamChunk{
			{Content: "slow"},
			{Done: true},
		}, 5*time.Second)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "go")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		ctx, cancel := context.WithCancel(context.Background())

		// Fire the long-poll in a goroutine, cancel after a short delay.
		// The wait must abort promptly — well under the 25s timeout.
		type result struct {
			err     error
			elapsed time.Duration
		}
		done := make(chan result, 1)
		go func() {
			start := time.Now()
			_, _, err := getTurnLongPoll(ctx, sess.ID, turnID, 0)
			done <- result{err: err, elapsed: time.Since(start)}
		}()

		time.Sleep(100 * time.Millisecond)
		cancel()

		var r result
		Eventually(done, "2s").Should(Receive(&r))
		// The client-side request errors with a context-cancelled
		// transport error; the server-side handler must have observed
		// r.Context().Done() and returned — we verify by elapsed time.
		Expect(r.elapsed).To(BeNumerically("<", 1*time.Second),
			"client disconnect must propagate to the server-side wait via r.Context().Done() — the handler should not hang on the 25s timer after the client is gone. Elapsed: %v, err: %v", r.elapsed, r.err)
	})

	It("wait=true with since=N returns immediately when len(MessagesAdded) already > N at call time", func() {
		// Fast drip so engine-emitted rows accumulate quickly.
		setup([]provider.StreamChunk{
			{Content: "alpha"},
			{Content: "beta"},
			{Content: "gamma"},
			{Done: true},
		}, 5*time.Millisecond)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "list")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Wait for the Turn to complete and accumulate at least one row.
		Eventually(func() int {
			resp, raw, _ := getTurnLongPoll(context.Background(), sess.ID, turnID, 0)
			if resp == nil || resp.StatusCode != http.StatusOK {
				return 0
			}
			var decoded map[string]any
			_ = json.Unmarshal(raw, &decoded)
			msgs, _ := decoded["messages"].([]any)
			return len(msgs)
		}, "3s", "30ms").Should(BeNumerically(">=", 1))

		// Now issue a long-poll with since=0. The Turn already has rows;
		// the wait must return synchronously (not block on the 25s
		// timer). The wait predicate hits "len > sinceCount" on entry
		// and exits without parking.
		start := time.Now()
		resp, raw, err := getTurnLongPoll(context.Background(), sess.ID, turnID, 0)
		Expect(err).NotTo(HaveOccurred())
		elapsed := time.Since(start)

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var decoded map[string]any
		Expect(json.Unmarshal(raw, &decoded)).To(Succeed())
		msgs, _ := decoded["messages"].([]any)
		Expect(len(msgs)).To(BeNumerically(">=", 1))
		Expect(elapsed).To(BeNumerically("<", 200*time.Millisecond),
			"len > since on entry must short-circuit the wait — anything past 200ms suggests the predicate isn't being evaluated synchronously on the first iteration. Elapsed: %v", elapsed)
	})

	It("wait absent OR wait=false preserves the legacy snapshot-read behaviour (no hang)", func() {
		// 5s drip — if the handler is wrongly long-polling on the
		// legacy path, the test would hang. We use a tight client-side
		// timeout to bound the test runtime.
		setup([]provider.StreamChunk{
			{Content: "slow"},
			{Done: true},
		}, 5*time.Second)

		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())

		_, body, _ := postMessage(sess.ID, "go")
		turnID, _ := body["turn_id"].(string)
		Expect(turnID).NotTo(BeEmpty())

		// Legacy URL — no ?wait, no ?since. Must return the current
		// snapshot synchronously (turn is Running but the handler
		// doesn't hold the request).
		start := time.Now()
		url := fmt.Sprintf("%s/api/v1/sessions/%s/turns/%s", httpSrv.URL, sess.ID, turnID)
		resp, err := http.Get(url) //nolint:noctx
		Expect(err).NotTo(HaveOccurred())
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		elapsed := time.Since(start)

		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(elapsed).To(BeNumerically("<", 500*time.Millisecond),
			"legacy snapshot-read MUST NOT hang — pre-1b clients on a Running turn always got an immediate response. Elapsed: %v, body: %s", elapsed, string(raw))

		// Legacy path also must NOT set the long-poll headers — those
		// are wait=true-specific so a CDN / browser caches the legacy
		// snapshot correctly. (Caching the snapshot is fine for legacy
		// because the FE re-polls on a fixed cadence; only the long-
		// poll surface needs no-cache.)
		Expect(resp.Header.Get("X-Accel-Buffering")).To(BeEmpty(),
			"the legacy snapshot path must NOT set X-Accel-Buffering — that header is long-poll-only")
	})
})

// Phase-4-Commit-2 negative-contract pin per "Turn-Based Post-Then-Poll
// Architecture (May 2026)". The session-scoped SSE and WebSocket
// surfaces are retired; long-poll on
// GET /api/v1/sessions/{id}/turns/{turn_id} is the sole live channel.
// This Describe pins that neither retired route can be matched by the
// mux — any future reintroduction must remove this spec deliberately.
//
// We probe both "GET" requests (the original verbs on the deleted
// routes) against a fresh server with a real session manager so the
// 404 doesn't get masked by the 501 "session manager not configured"
// branch — that branch was the previous handler's pre-flight and is
// gone with the handler. With the mux not matching the path, net/http
// falls through to the SPA index handler which 200s for "/" but
// redirects unknown /api/v1/* paths via the SPA shell. We assert the
// route is NOT one of the registered API endpoints (response is NOT
// 200 with a JSON content-type) which proves the mux didn't register
// it.
var _ = Describe("Phase-4-Commit-2 — retired SSE / WebSocket routes return 404", func() {
	var (
		mgr     *session.Manager
		srv     *api.Server
		httpSrv *httptest.Server
	)

	BeforeEach(func() {
		mgr = session.NewManager(&mockStreamer{chunks: []provider.StreamChunk{{Done: true}}})
		reg := agent.NewRegistry()
		reg.Register(&agent.Manifest{ID: "default-assistant", Name: "Default Assistant"})
		srv = api.NewServer(
			&mockStreamer{chunks: []provider.StreamChunk{}},
			reg,
			discovery.NewAgentDiscovery(nil),
			nil,
			api.WithSessionManager(mgr),
		)
		httpSrv = httptest.NewServer(srv.Handler())
	})

	AfterEach(func() {
		if httpSrv != nil {
			httpSrv.Close()
			httpSrv = nil
		}
	})

	// Client that does not follow redirects — the SPA index handler
	// 302s to /chat which would otherwise produce a redirect loop.
	noRedirectClient := func() *http.Client {
		return &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}

	It("GET /api/v1/sessions/{id}/stream is not registered on the mux", func() {
		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())
		req, _ := http.NewRequestWithContext(context.Background(),
			http.MethodGet, httpSrv.URL+"/api/v1/sessions/"+sess.ID+"/stream", nil)
		resp, err := noRedirectClient().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		// A retired API route must NOT serve a successful JSON
		// response. The mux either returns 404 directly or falls
		// through to the SPA-index handler which serves the HTML
		// shell. Either way: NOT a JSON API response, and not the
		// SSE wire (text/event-stream).
		ct := resp.Header.Get("Content-Type")
		Expect(ct).NotTo(ContainSubstring("application/json"),
			"GET /api/v1/sessions/{id}/stream must be retired (not a registered JSON API endpoint). Got Content-Type=%q, status=%d", ct, resp.StatusCode)
		Expect(ct).NotTo(ContainSubstring("text/event-stream"),
			"GET /api/v1/sessions/{id}/stream must be retired (not an SSE endpoint). Got Content-Type=%q, status=%d", ct, resp.StatusCode)
	})

	It("DELETE /api/v1/sessions/{id}/stream is not registered on the mux", func() {
		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())
		req, _ := http.NewRequestWithContext(context.Background(),
			http.MethodDelete, httpSrv.URL+"/api/v1/sessions/"+sess.ID+"/stream", nil)
		resp, err := noRedirectClient().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		// The handleCancelStream handler is gone. The mux either 404s
		// or matches the SPA-index handler (which would reject the
		// non-GET method). Critical contract: the canonical 204
		// "in-flight turn cancelled" response is no longer produced.
		Expect(resp.StatusCode).NotTo(Equal(http.StatusNoContent),
			"DELETE /api/v1/sessions/{id}/stream must be retired — the 204 cancel response is gone. Got status=%d", resp.StatusCode)
	})

	It("GET /api/v1/sessions/{id}/ws is not registered on the mux", func() {
		sess, err := mgr.CreateSession("default-assistant")
		Expect(err).NotTo(HaveOccurred())
		req, _ := http.NewRequestWithContext(context.Background(),
			http.MethodGet, httpSrv.URL+"/api/v1/sessions/"+sess.ID+"/ws", nil)
		resp, err := noRedirectClient().Do(req)
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()
		// WebSocket upgrade handshakes return 101 Switching Protocols
		// on success. A retired WS route must NOT return 101.
		Expect(resp.StatusCode).NotTo(Equal(http.StatusSwitchingProtocols),
			"GET /api/v1/sessions/{id}/ws must be retired — no Switching Protocols upgrade. Got status=%d", resp.StatusCode)
		ct := resp.Header.Get("Content-Type")
		Expect(ct).NotTo(ContainSubstring("application/json"),
			"GET /api/v1/sessions/{id}/ws must be retired (not a registered JSON API endpoint). Got Content-Type=%q, status=%d", ct, resp.StatusCode)
	})

	It("source-level: server.go no longer registers /stream or /ws routes", func() {
		// Belt-and-braces source-level pin — if the mux registration
		// is re-added inadvertently, the source-level scan catches it
		// even if the runtime behaviour above slips through (e.g. a
		// future SPA-shell change).
		_, thisFile, _, ok := runtime.Caller(0)
		Expect(ok).To(BeTrue())
		serverPath := filepath.Join(filepath.Dir(thisFile), "server.go")
		src, err := os.ReadFile(serverPath)
		Expect(err).NotTo(HaveOccurred())
		body := string(src)
		Expect(body).NotTo(ContainSubstring("/api/v1/sessions/{id}/stream"),
			"server.go must not register the retired /stream route — Phase-4-Commit-2 of 'Turn-Based Post-Then-Poll Architecture (May 2026)' retired session-scoped SSE")
		Expect(body).NotTo(ContainSubstring("/api/v1/sessions/{id}/ws"),
			"server.go must not register the retired /ws route — Phase-4-Commit-2 retired session-scoped WebSocket")
		Expect(body).NotTo(ContainSubstring("handleSessionStream"),
			"server.go must not define handleSessionStream — Phase-4-Commit-2 retired the SSE bridge")
		Expect(body).NotTo(ContainSubstring("handleSessionWebSocket"),
			"server.go must not define handleSessionWebSocket — Phase-4-Commit-2 retired the WS handler")
		Expect(body).NotTo(ContainSubstring("handleCancelStream"),
			"server.go must not define handleCancelStream — Phase-4-Commit-2 retired the cancel-stream handler")
		Expect(body).NotTo(ContainSubstring("sessionBroker"),
			"server.go must not reference sessionBroker — Phase-4-Commit-2 retired the broker entirely")
	})
})
