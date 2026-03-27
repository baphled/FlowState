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
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/skill"
	todo "github.com/baphled/flowstate/internal/tool/todo"
)

type mockStreamer struct {
	chunks []provider.StreamChunk
	err    error
}

func (m *mockStreamer) Stream(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan provider.StreamChunk, len(m.chunks))
	for _, c := range m.chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
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
	})

	Describe("GET /", func() {
		It("returns HTML response", func() {
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			server.Handler().ServeHTTP(recorder, req)

			Expect(recorder.Code).To(Equal(http.StatusOK))
			Expect(recorder.Header().Get("Content-Type")).To(Equal("text/html; charset=utf-8"))
			Expect(recorder.Body.String()).To(ContainSubstring("<!DOCTYPE html>"))
			Expect(recorder.Body.String()).To(ContainSubstring("<textarea"))
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
