package session_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("Session persistence", func() {
	var sessionsDir string

	BeforeEach(func() {
		var err error
		sessionsDir, err = os.MkdirTemp("", "session-persistence-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(sessionsDir) })
	})

	Describe("PersistSession", func() {
		Context("when given a valid session and directory", func() {
			It("writes a .meta.json file at the expected path", func() {
				sess := &session.Session{
					ID:        "abc-123",
					ParentID:  "parent-456",
					AgentID:   "test-agent",
					Status:    "active",
					CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				}

				err := session.PersistSession(sessionsDir, sess)
				Expect(err).NotTo(HaveOccurred())

				expectedPath := filepath.Join(sessionsDir, "abc-123.meta.json")
				Expect(expectedPath).To(BeAnExistingFile())
			})

			It("writes valid JSON content", func() {
				sess := &session.Session{
					ID:      "abc-123",
					AgentID: "test-agent",
					Status:  "completed",
				}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "abc-123.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring(`"id":"abc-123"`))
				Expect(string(data)).To(ContainSubstring(`"agent_id":"test-agent"`))
				Expect(string(data)).To(ContainSubstring(`"status":"completed"`))
			})

			It("writes a durationMs field derived from session wall-clock", func() {
				sess := &session.Session{
					ID:        "duration-sess",
					AgentID:   "agent-x",
					Status:    "active",
					CreatedAt: time.Now().Add(-2 * time.Second),
				}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "duration-sess.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				var decoded map[string]any
				Expect(json.Unmarshal(data, &decoded)).To(Succeed())
				duration, ok := decoded["durationMs"]
				Expect(ok).To(BeTrue(),
					"the sidecar must carry durationMs so session analytics can show wall-clock length")
				Expect(duration.(float64)).To(BeNumerically(">=", 1000),
					"a session created two seconds ago must persist a duration of at least one second")
			})

			It("creates the directory when it does not exist", func() {
				nestedDir := filepath.Join(sessionsDir, "nested", "path")
				sess := &session.Session{ID: "new-sess", AgentID: "agent-x", Status: "active"}

				Expect(session.PersistSession(nestedDir, sess)).To(Succeed())
				Expect(filepath.Join(nestedDir, "new-sess.meta.json")).To(BeAnExistingFile())
			})

			It("persists CurrentAgentID so the user's last-selected agent survives restart", func() {
				sess := &session.Session{
					ID:             "agent-switch-sess",
					AgentID:        "default-assistant",
					CurrentAgentID: "code-reviewer",
					Status:         "active",
				}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "agent-switch-sess.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring(`"current_agent_id":"code-reviewer"`))
			})

			It("omits current_agent_id when the field is empty (backwards-compat with legacy on-disk files)", func() {
				sess := &session.Session{
					ID:      "no-current-agent",
					AgentID: "default-assistant",
					Status:  "active",
				}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "no-current-agent.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).NotTo(ContainSubstring("current_agent_id"))
			})

			It("leaves no staging file behind after an atomic write", func() {
				sess := &session.Session{ID: "atomic-sess", AgentID: "agent-x", Status: "active"}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				entries, err := os.ReadDir(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				for _, e := range entries {
					Expect(e.Name()).NotTo(HaveSuffix(".meta.json.tmp"))
				}
			})

			It("replaces a prior sidecar with a single valid JSON object on rewrite", func() {
				sess := &session.Session{ID: "rewrite-sess", AgentID: "agent-x", Status: "active"}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())
				sess.Status = "completed"
				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "rewrite-sess.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				var decoded map[string]any
				Expect(json.Unmarshal(data, &decoded)).To(Succeed())
				Expect(decoded).NotTo(BeEmpty())
			})
		})
	})

	Describe("LoadSessionsFromDirectory", func() {
		Context("when the directory is empty", func() {
			It("returns an empty slice without error", func() {
				sessions, err := session.LoadSessionsFromDirectory(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(BeEmpty())
			})
		})

		Context("when the directory does not exist", func() {
			It("returns an empty slice without error", func() {
				sessions, err := session.LoadSessionsFromDirectory(filepath.Join(sessionsDir, "nonexistent"))
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(BeEmpty())
			})
		})

		Context("when valid .meta.json files are present", func() {
			It("loads and returns all sessions", func() {
				first := &session.Session{
					ID:      "sess-1",
					AgentID: "agent-a",
					Status:  "active",
				}
				second := &session.Session{
					ID:       "sess-2",
					ParentID: "sess-1",
					AgentID:  "agent-b",
					Status:   "completed",
				}

				Expect(session.PersistSession(sessionsDir, first)).To(Succeed())
				Expect(session.PersistSession(sessionsDir, second)).To(Succeed())

				sessions, err := session.LoadSessionsFromDirectory(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(2))
			})
		})

		Context("when a corrupt .meta.json file is present alongside valid ones", func() {
			It("skips the corrupt file and returns the valid sessions", func() {
				valid := &session.Session{ID: "ok-sess", AgentID: "agent-ok", Status: "active"}
				Expect(session.PersistSession(sessionsDir, valid)).To(Succeed())

				corruptPath := filepath.Join(sessionsDir, "corrupt.meta.json")
				Expect(os.WriteFile(corruptPath, []byte("not json {{{"), 0o600)).To(Succeed())

				sessions, err := session.LoadSessionsFromDirectory(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(1))
				Expect(sessions[0].ID).To(Equal("ok-sess"))
			})
		})

		Context("round-trip: persist then load", func() {
			It("restores all fields from the persisted metadata", func() {
				createdAt := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
				original := &session.Session{
					ID:        "round-trip-id",
					ParentID:  "parent-id",
					AgentID:   "round-trip-agent",
					Status:    "completed",
					CreatedAt: createdAt,
				}

				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				sessions, err := session.LoadSessionsFromDirectory(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(1))

				restored := sessions[0]
				Expect(restored.ID).To(Equal(original.ID))
				Expect(restored.ParentID).To(Equal(original.ParentID))
				Expect(restored.AgentID).To(Equal(original.AgentID))
				Expect(restored.Status).To(Equal(original.Status))
				Expect(restored.CreatedAt.UTC()).To(BeTemporally("~", original.CreatedAt, time.Second))
			})

			It("restores persisted Messages so chat history survives a restart", func() {
				ts := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
				original := &session.Session{
					ID:        "msg-round-trip",
					AgentID:   "default-assistant",
					Status:    "active",
					CreatedAt: ts,
					Messages: []session.Message{
						{
							ID:        "msg-1",
							Role:      "user",
							Content:   "Hello there",
							Timestamp: ts,
						},
						{
							ID:        "msg-2",
							Role:      "assistant",
							Content:   "Hi! How can I help?",
							AgentID:   "default-assistant",
							Timestamp: ts.Add(time.Second),
						},
						{
							ID:        "msg-3",
							Role:      "tool",
							Content:   "result body",
							ToolName:  "read",
							ToolInput: `{"path":"foo"}`,
							Timestamp: ts.Add(2 * time.Second),
						},
					},
				}

				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				sessions, err := session.LoadSessionsFromDirectory(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(1))

				restored := sessions[0]
				Expect(restored.Messages).To(HaveLen(3))
				Expect(restored.Messages[0].ID).To(Equal("msg-1"))
				Expect(restored.Messages[0].Role).To(Equal("user"))
				Expect(restored.Messages[0].Content).To(Equal("Hello there"))
				Expect(restored.Messages[1].AgentID).To(Equal("default-assistant"))
				Expect(restored.Messages[1].Content).To(Equal("Hi! How can I help?"))
				Expect(restored.Messages[2].ToolName).To(Equal("read"))
				Expect(restored.Messages[2].ToolInput).To(Equal(`{"path":"foo"}`))
				Expect(restored.Messages[2].Timestamp.UTC()).To(BeTemporally("~", ts.Add(2*time.Second), time.Second))
			})

			It("loads persisted Messages via LoadSessionMetadata for a single session", func() {
				ts := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
				original := &session.Session{
					ID:        "single-load",
					AgentID:   "default-assistant",
					Status:    "active",
					CreatedAt: ts,
					Messages: []session.Message{
						{ID: "m1", Role: "user", Content: "ping", Timestamp: ts},
					},
				}
				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				restored, err := session.LoadSessionMetadata(sessionsDir, "single-load")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.Messages).To(HaveLen(1))
				Expect(restored.Messages[0].Content).To(Equal("ping"))
			})

			It("restores CurrentAgentID via LoadSessionMetadata so a single-session read sees the last-selected agent", func() {
				original := &session.Session{
					ID:             "current-agent-single-load",
					AgentID:        "default-assistant",
					CurrentAgentID: "code-reviewer",
					Status:         "active",
				}
				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				restored, err := session.LoadSessionMetadata(sessionsDir, "current-agent-single-load")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.CurrentAgentID).To(Equal("code-reviewer"))
			})

			It("restores CurrentAgentID via LoadSessionsFromDirectory for a directory scan", func() {
				original := &session.Session{
					ID:             "current-agent-dir-load",
					AgentID:        "default-assistant",
					CurrentAgentID: "writer",
					Status:         "active",
				}
				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				sessions, err := session.LoadSessionsFromDirectory(sessionsDir)
				Expect(err).NotTo(HaveOccurred())
				Expect(sessions).To(HaveLen(1))
				Expect(sessions[0].CurrentAgentID).To(Equal("writer"))
			})

			It("returns an empty CurrentAgentID for legacy on-disk files that predate the field", func() {
				legacyJSON := `{"id":"legacy-sess","agent_id":"default-assistant","status":"active","created_at":"2026-04-01T12:00:00Z"}`
				Expect(os.WriteFile(filepath.Join(sessionsDir, "legacy-sess.meta.json"), []byte(legacyJSON), 0o600)).To(Succeed())

				restored, err := session.LoadSessionMetadata(sessionsDir, "legacy-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.CurrentAgentID).To(BeEmpty())
				Expect(restored.AgentID).To(Equal("default-assistant"))
			})

			It("round-trips CurrentModelID and CurrentProviderID via LoadSessionMetadata", func() {
				original := &session.Session{
					ID:                "model-provider-round-trip",
					AgentID:           "default-assistant",
					CurrentModelID:    "claude-opus-4.7",
					CurrentProviderID: "anthropic",
					Status:            "active",
				}
				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "model-provider-round-trip.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring(`"current_model_id":"claude-opus-4.7"`))
				Expect(string(data)).To(ContainSubstring(`"current_provider_id":"anthropic"`))

				restored, err := session.LoadSessionMetadata(sessionsDir, "model-provider-round-trip")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.CurrentModelID).To(Equal("claude-opus-4.7"))
				Expect(restored.CurrentProviderID).To(Equal("anthropic"))
			})

			It("returns empty CurrentModelID and CurrentProviderID for legacy on-disk files that predate the fields", func() {
				legacyJSON := `{"id":"legacy-model-sess","agent_id":"default-assistant","status":"active","created_at":"2026-04-01T12:00:00Z"}`
				Expect(os.WriteFile(filepath.Join(sessionsDir, "legacy-model-sess.meta.json"), []byte(legacyJSON), 0o600)).To(Succeed())

				restored, err := session.LoadSessionMetadata(sessionsDir, "legacy-model-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.CurrentModelID).To(BeEmpty())
				Expect(restored.CurrentProviderID).To(BeEmpty())
			})

			It("round-trips EmbeddingModel via LoadSessionMetadata so the diagnostic survives process restart", func() {
				original := &session.Session{
					ID:             "embed-model-round-trip",
					AgentID:        "default-assistant",
					EmbeddingModel: "nomic-embed-text",
					Status:         "active",
				}
				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "embed-model-round-trip.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring(`"embedding_model":"nomic-embed-text"`))

				restored, err := session.LoadSessionMetadata(sessionsDir, "embed-model-round-trip")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.EmbeddingModel).To(Equal("nomic-embed-text"))
			})

			It("returns an empty EmbeddingModel for legacy on-disk files that predate the field", func() {
				// Legacy session JSON without the field MUST load cleanly —
				// no panic, no error, just an empty value. This is the
				// pre-schema diagnostic gap (sessions like
				// 3c5374fd-2835-4720-b543-0c3c95b028aa) where Recall
				// silent-zero failures were undiagnosable.
				legacyJSON := `{"id":"legacy-embed-sess","agent_id":"default-assistant","status":"active","created_at":"2026-04-01T12:00:00Z"}`
				Expect(os.WriteFile(filepath.Join(sessionsDir, "legacy-embed-sess.meta.json"), []byte(legacyJSON), 0o600)).To(Succeed())

				restored, err := session.LoadSessionMetadata(sessionsDir, "legacy-embed-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.EmbeddingModel).To(BeEmpty())
				Expect(restored.AgentID).To(Equal("default-assistant"))
			})

			It("omits embedding_model from the JSON when the field is empty (backwards-compat with legacy on-disk files)", func() {
				sess := &session.Session{
					ID:      "no-embed-model-sess",
					AgentID: "default-assistant",
					Status:  "active",
				}

				Expect(session.PersistSession(sessionsDir, sess)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "no-embed-model-sess.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).NotTo(ContainSubstring("embedding_model"))
			})

			It("round-trips when only CurrentModelID is set, leaving CurrentProviderID empty and omitted from JSON", func() {
				original := &session.Session{
					ID:             "model-only-sess",
					AgentID:        "default-assistant",
					CurrentModelID: "claude-opus-4.7",
					Status:         "active",
				}
				Expect(session.PersistSession(sessionsDir, original)).To(Succeed())

				data, err := os.ReadFile(filepath.Join(sessionsDir, "model-only-sess.meta.json"))
				Expect(err).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring(`"current_model_id":"claude-opus-4.7"`))
				Expect(string(data)).NotTo(ContainSubstring("current_provider_id"))

				restored, err := session.LoadSessionMetadata(sessionsDir, "model-only-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).NotTo(BeNil())
				Expect(restored.CurrentModelID).To(Equal("claude-opus-4.7"))
				Expect(restored.CurrentProviderID).To(BeEmpty())
			})
		})
	})

	Describe("PersistSwarmEvents", func() {
		refTime := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)

		Context("when events slice is empty", func() {
			It("does not create a file", func() {
				err := session.PersistSwarmEvents(sessionsDir, "sess-1", nil)
				Expect(err).NotTo(HaveOccurred())

				path := filepath.Join(sessionsDir, "sess-1.events.jsonl")
				Expect(path).NotTo(BeAnExistingFile())
			})
		})

		Context("when events are provided", func() {
			It("writes a .events.jsonl file", func() {
				events := []streaming.SwarmEvent{
					{
						ID:        "ev-1",
						Type:      streaming.EventDelegation,
						Status:    "started",
						Timestamp: refTime,
						AgentID:   "engineer",
					},
				}
				err := session.PersistSwarmEvents(sessionsDir, "sess-1", events)
				Expect(err).NotTo(HaveOccurred())

				path := filepath.Join(sessionsDir, "sess-1.events.jsonl")
				Expect(path).To(BeAnExistingFile())

				data, readErr := os.ReadFile(path)
				Expect(readErr).NotTo(HaveOccurred())
				Expect(string(data)).To(ContainSubstring("ev-1"))
				Expect(string(data)).To(ContainSubstring("delegation"))
			})

			It("creates the directory when it does not exist", func() {
				nestedDir := filepath.Join(sessionsDir, "deep", "nested")
				events := []streaming.SwarmEvent{
					{ID: "ev-2", Type: streaming.EventToolCall, Status: "completed", Timestamp: refTime, AgentID: "agent"},
				}
				Expect(session.PersistSwarmEvents(nestedDir, "sess-2", events)).To(Succeed())
				Expect(filepath.Join(nestedDir, "sess-2.events.jsonl")).To(BeAnExistingFile())
			})
		})
	})

	Describe("LoadSwarmEvents", func() {
		refTime := time.Date(2026, 4, 16, 10, 0, 0, 0, time.UTC)

		Context("when no events file exists", func() {
			It("returns nil without error", func() {
				events, err := session.LoadSwarmEvents(sessionsDir, "nonexistent")
				Expect(err).NotTo(HaveOccurred())
				Expect(events).To(BeNil())
			})
		})

		Context("round-trip: persist then load", func() {
			It("restores all events", func() {
				original := []streaming.SwarmEvent{
					{
						ID:        "ev-a",
						Type:      streaming.EventDelegation,
						Status:    "started",
						Timestamp: refTime,
						AgentID:   "engineer",
						Metadata:  map[string]interface{}{"source_agent": "orchestrator"},
					},
					{
						ID:        "ev-b",
						Type:      streaming.EventToolCall,
						Status:    "completed",
						Timestamp: refTime.Add(time.Second),
						AgentID:   "engineer",
						Metadata:  map[string]interface{}{"tool_name": "read"},
					},
				}

				Expect(session.PersistSwarmEvents(sessionsDir, "rt-sess", original)).To(Succeed())

				restored, err := session.LoadSwarmEvents(sessionsDir, "rt-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(restored).To(HaveLen(2))
				Expect(restored[0].ID).To(Equal("ev-a"))
				Expect(restored[0].Type).To(Equal(streaming.EventDelegation))
				Expect(restored[1].ID).To(Equal("ev-b"))
				Expect(restored[1].Metadata).To(HaveKeyWithValue("tool_name", "read"))
			})
		})
	})
})

// BenchmarkAppendSessionMessage measures the cost of appending one
// message to a large persisted session — the P0/P1 hot path where
// every append re-marshals the full history with fsync+rename. The
// bN loop uses b.StopTimer/StartTimer so the pre-fill (seeding
// history and writing the baseline sidecar) is excluded; only the
// per-append cost is measured.
func BenchmarkAppendSessionMessage(b *testing.B) {
	for _, size := range []int{100, 1000, 3000} {
		b.Run(fmt.Sprintf("history_%d", size), func(b *testing.B) {
			dir := b.TempDir()
			m := session.NewManager(nil)
			m.SetSessionsDir(dir)
			sess, err := m.CreateSession("bench-agent")
			if err != nil {
				b.Fatalf("CreateSession: %v", err)
			}
			for i := 0; i < size; i++ {
				m.AppendSessionMessageForTest(sess.ID, session.Message{
					ID:   fmt.Sprintf("seed-%d", i),
					Role: "user",
					Content: fmt.Sprintf(
						"seed message %d with a realistic amount of body text to approximate dogfood payloads",
						i,
					),
				})
			}
			sidecar := filepath.Join(dir, sess.ID+session.MetaFileSuffixForTest)
			if _, err := os.Stat(sidecar); err != nil {
				b.Fatalf("baseline sidecar missing: %v", err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.AppendSessionMessageForTest(sess.ID, session.Message{
					ID:      fmt.Sprintf("bench-%d", i),
					Role:    "user",
					Content: "benchmark append payload",
				})
			}
			b.StopTimer()
		})
	}
}

// persistCall timestamps one persistFn invocation so the debounce
// tests can assert when the sidecar write landed without wall-clock
// sleeps against the production 5s interval.
type persistCall struct {
	at    time.Time
	msgN  int
	agent string
}

func waitForPersists(calls chan persistCall, want int, timeout time.Duration) []persistCall {
	var got []persistCall
	deadline := time.After(timeout)
	for len(got) < want {
		select {
		case c := <-calls:
			got = append(got, c)
		case <-deadline:
			return got
		}
	}
	return got
}

var _ = Describe("Persist debounce flusher", func() {
	var (
		sessionsDir string
		mgr         *session.Manager
		persists    chan persistCall
	)

	BeforeEach(func() {
		var err error
		sessionsDir, err = os.MkdirTemp("", "persist-debounce-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { os.RemoveAll(sessionsDir) })

		persists = make(chan persistCall, 64)
		mgr = session.NewManager(nil)
		mgr.SetSessionsDir(sessionsDir)
		mgr.SetPersistFnForTest(func(dir string, s *session.Session) error {
			persists <- persistCall{at: time.Now(), msgN: len(s.Messages), agent: s.AgentID}
			return session.PersistSession(dir, s)
		})
	})

	Describe("time-driven flush within PersistDebounceInterval", func() {
		It("flushes a session with fewer than PersistMutationLimit appends without an explicit flush call", func() {
			Expect(mgr.SetPersistDebounceForTest(30 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("debounce-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())

			createCall := waitForPersists(persists, 1, 2*time.Second)
			Expect(createCall).To(HaveLen(1), "session creation persists synchronously")

			for i := 0; i < 3; i++ {
				mgr.AppendSessionMessageForTest(sess.ID, session.Message{
					ID:      debounceMsgID(i),
					Role:    "user",
					Content: "under the mutation limit so only the time sweeper can flush",
				})
			}

			sweepCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(sweepCalls).To(HaveLen(1),
				"the sweeper must flush a dirty session within the debounce interval")
			Expect(sweepCalls[0].msgN).To(BeNumerically(">=", 3),
				"the sweep write carries the buffered appends")
		})

		It("coalesces a burst of appends into a single sweep write", func() {
			Expect(mgr.SetPersistDebounceForTest(50 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("coalesce-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			for i := 0; i < session.PersistMutationLimit-1; i++ {
				mgr.AppendSessionMessageForTest(sess.ID, session.Message{
					ID:      debounceMsgID(i),
					Role:    "user",
					Content: "bursty turn coalesced by the debounce sweeper",
				})
			}

			sweepCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(sweepCalls).To(HaveLen(1),
				"a burst under the mutation limit must land as one sidecar write")
		})
	})

	Describe("Stop semantics", func() {
		It("performs a final flush on Stop and exits the sweeper goroutine", func() {
			Expect(mgr.SetPersistDebounceForTest(10 * time.Second)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("stop-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			mgr.AppendSessionMessageForTest(sess.ID, session.Message{
				ID:      "pre-stop-1",
				Role:    "user",
				Content: "dirty at shutdown; Stop must flush it",
			})

			Expect(mgr.Stop()).To(Succeed())
			finalCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(finalCalls).To(HaveLen(1), "Stop flushes the dirty session on the way down")
		})

		It("does not start a new sweeper after Stop when a session is marked dirty again", func() {
			Expect(mgr.SetPersistDebounceForTest(30 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("post-stop-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			Expect(mgr.Stop()).To(Succeed())
			drainPersists(persists)

			mgr.AppendSessionMessageForTest(sess.ID, session.Message{
				ID:      "post-stop-1",
				Role:    "user",
				Content: "must not lazily restart a sweeper after Stop",
			})

			Consistently(persists, 300*time.Millisecond, 50*time.Millisecond).ShouldNot(Receive(),
				"a post-Stop dirty-mark must not restart the sweeper goroutine")
		})
	})

	Describe("tool-anomaly softened append persistence regression (d96da3bd)", func() {
		It("marks the session dirty when a softened tool-anomaly message is appended", func() {
			Expect(mgr.SetPersistDebounceForTest(30 * time.Millisecond)).To(Succeed())
			sess, err := mgr.CreateSessionWithDefaults("anomaly-agent", "prov", "model")
			Expect(err).NotTo(HaveOccurred())
			waitForPersists(persists, 1, 2*time.Second)

			mgr.AppendSessionMessageForTest(sess.ID, session.Message{
				ID:      "anomaly-1",
				Role:    "assistant",
				Status:  "anomaly",
				Content: "softened tool-anomaly append must reach the sidecar",
			})

			sweepCalls := waitForPersists(persists, 1, 2*time.Second)
			Expect(sweepCalls).To(HaveLen(1),
				"the anomaly-fix dirty-mark path must flush via the sweeper")
		})
	})
})

// debounceMsgID generates a deterministic message ID for debounce tests.
func debounceMsgID(i int) string {
	return "debounce-msg-" + time.Now().Format("150405") + "-" + string(rune('a'+i))
}

// drainPersists empties any buffered persist calls without blocking.
func drainPersists(persists chan persistCall) {
	for {
		select {
		case <-persists:
		default:
			return
		}
	}
}
