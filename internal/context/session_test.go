package context_test

import (
	"os"
	"path/filepath"

	"github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/provider"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("SessionPersistence", func() {
	var (
		tmpDir       string
		sessionsDir  string
		contextDir   string
		sessionStore *context.FileSessionStore
	)

	BeforeEach(func() {
		tmpDir = GinkgoT().TempDir()
		sessionsDir = filepath.Join(tmpDir, "sessions")
		contextDir = filepath.Join(tmpDir, "context")
		var err error
		sessionStore, err = context.NewFileSessionStore(sessionsDir)
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("NewFileSessionStore", func() {
		It("returns non-nil store", func() {
			Expect(sessionStore).NotTo(BeNil())
		})

		It("creates the base directory if it does not exist", func() {
			newDir := filepath.Join(tmpDir, "nested", "sessions")
			store, err := context.NewFileSessionStore(newDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(store).NotTo(BeNil())

			info, err := os.Stat(newDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})
	})

	Describe("Save and Load round-trip", func() {
		It("preserves messages", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"text-embedding-ada-002",
			)
			Expect(err).NotTo(HaveOccurred())

			ctxStore.Append(provider.Message{Role: "user", Content: "Hello"})
			ctxStore.Append(provider.Message{Role: "assistant", Content: "Hi there"})

			err = sessionStore.Save("session-1", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			loadedStore, err := sessionStore.Load("session-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(loadedStore.Count()).To(Equal(2))

			messages := loadedStore.AllMessages()
			Expect(messages[0].Role).To(Equal("user"))
			Expect(messages[0].Content).To(Equal("Hello"))
			Expect(messages[1].Role).To(Equal("assistant"))
			Expect(messages[1].Content).To(Equal("Hi there"))
		})

		It("preserves embeddings", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"text-embedding-ada-002",
			)
			Expect(err).NotTo(HaveOccurred())

			ctxStore.Append(provider.Message{Role: "user", Content: "Test message"})
			msgID := ctxStore.GetMessageID(0)
			ctxStore.StoreEmbedding(msgID, []float64{0.1, 0.2, 0.3}, "text-embedding-ada-002", 3)

			err = sessionStore.Save("session-embed", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			loadedStore, err := sessionStore.Load("session-embed")
			Expect(err).NotTo(HaveOccurred())

			results := loadedStore.Search([]float64{0.1, 0.2, 0.3}, 10)
			Expect(results).To(HaveLen(1))
			Expect(results[0].Message.Content).To(Equal("Test message"))
		})
	})

	Describe("Load", func() {
		It("returns error for non-existent session", func() {
			_, err := sessionStore.Load("non-existent-session")
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("List", func() {
		It("returns empty slice when no sessions exist", func() {
			sessions := sessionStore.List()
			Expect(sessions).To(BeEmpty())
		})

		It("returns one entry after Save", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"text-embedding-ada-002",
			)
			Expect(err).NotTo(HaveOccurred())
			ctxStore.Append(provider.Message{Role: "user", Content: "Hello"})

			err = sessionStore.Save("my-session", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			sessions := sessionStore.List()
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal("my-session"))
		})

		It("returns correct MessageCount", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"text-embedding-ada-002",
			)
			Expect(err).NotTo(HaveOccurred())
			ctxStore.Append(provider.Message{Role: "user", Content: "One"})
			ctxStore.Append(provider.Message{Role: "assistant", Content: "Two"})
			ctxStore.Append(provider.Message{Role: "user", Content: "Three"})

			err = sessionStore.Save("count-session", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			sessions := sessionStore.List()
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].MessageCount).To(Equal(3))
		})
	})

	Describe("Save", func() {
		It("writes atomically (file exists after save)", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"text-embedding-ada-002",
			)
			Expect(err).NotTo(HaveOccurred())
			ctxStore.Append(provider.Message{Role: "user", Content: "Test"})

			err = sessionStore.Save("atomic-session", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			sessionPath := filepath.Join(sessionsDir, "atomic-session.json")
			_, err = os.Stat(sessionPath)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Describe("Model mismatch", func() {
		It("clears embeddings on Load when model differs", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"old-model",
			)
			Expect(err).NotTo(HaveOccurred())

			ctxStore.Append(provider.Message{Role: "user", Content: "Test"})
			msgID := ctxStore.GetMessageID(0)
			ctxStore.StoreEmbedding(msgID, []float64{0.1, 0.2}, "old-model", 2)

			err = sessionStore.Save("model-mismatch", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			loadedStore, err := sessionStore.LoadWithModel("model-mismatch", "new-model")
			Expect(err).NotTo(HaveOccurred())

			results := loadedStore.Search([]float64{0.1, 0.2}, 10)
			Expect(results).To(BeEmpty())
		})
	})

	Describe("DefaultSessionStore", func() {
		It("returns non-nil store", func() {
			store, err := context.DefaultSessionStore()
			Expect(err).NotTo(HaveOccurred())
			Expect(store).NotTo(BeNil())
		})
	})

	Describe("SessionFile round-trip with SystemPrompt and LoadedSkills", func() {
		It("preserves SystemPrompt and LoadedSkills through marshal/unmarshal", func() {
			ctxStore, err := context.NewFileContextStore(
				filepath.Join(contextDir, "ctx.json"),
				"text-embedding-ada-002",
			)
			Expect(err).NotTo(HaveOccurred())
			ctxStore.Append(provider.Message{Role: "user", Content: "Test"})

			err = sessionStore.Save("roundtrip-session", ctxStore, context.SessionMetadata{})
			Expect(err).NotTo(HaveOccurred())

			sessions := sessionStore.List()
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].SystemPrompt).To(Equal(""))
			Expect(sessions[0].LoadedSkills).To(BeEmpty())
		})
	})

	Describe("Backward compatibility with old session files", func() {
		It("loads sessions without system_prompt and loaded_skills fields", func() {
			oldSessionJSON := `{
  "session_id": "old-session",
  "title": "Old Session",
  "agent_id": "",
  "embedding_model": "text-embedding-ada-002",
  "last_active": "2024-01-01T00:00:00Z",
  "messages": [
    {
      "id": "msg-1",
      "role": "user",
      "content": "Hello",
      "timestamp": "2024-01-01T00:00:00Z"
    }
  ],
  "embeddings": []
}`
			sessionPath := filepath.Join(sessionsDir, "old-session.json")
			err := os.WriteFile(sessionPath, []byte(oldSessionJSON), 0o600)
			Expect(err).NotTo(HaveOccurred())

			sessions := sessionStore.List()
			Expect(sessions).To(HaveLen(1))
			Expect(sessions[0].ID).To(Equal("old-session"))
			Expect(sessions[0].SystemPrompt).To(Equal(""))
			Expect(sessions[0].LoadedSkills).To(BeNil())
		})
	})
})
