package session_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
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
		})
	})
})
