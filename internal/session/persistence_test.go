package session_test

import (
	"os"
	"path/filepath"
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
