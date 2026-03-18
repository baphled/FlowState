package learning_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/learning"
)

var _ = Describe("JSONFileStore", func() {
	var (
		store    learning.Store
		tempDir  string
		filePath string
	)

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "learning-test-*")
		Expect(err).NotTo(HaveOccurred())
		filePath = filepath.Join(tempDir, "learning.json")
		store = learning.NewJSONFileStore(filePath)
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("Capture", func() {
		It("captures a learning entry", func() {
			entry := learning.Entry{
				Timestamp:   time.Now(),
				AgentID:     "agent-001",
				UserMessage: "How do I write tests?",
				Response:    "Use Ginkgo and Gomega.",
				ToolsUsed:   []string{"editor", "terminal"},
				Outcome:     "success",
			}

			err := store.Capture(entry)
			Expect(err).NotTo(HaveOccurred())
		})

		It("creates the file on first write", func() {
			Expect(filePath).NotTo(BeAnExistingFile())

			entry := learning.Entry{
				Timestamp:   time.Now(),
				AgentID:     "agent-001",
				UserMessage: "Test message",
				Response:    "Test response",
				ToolsUsed:   []string{},
				Outcome:     "success",
			}

			err := store.Capture(entry)
			Expect(err).NotTo(HaveOccurred())
			Expect(filePath).To(BeAnExistingFile())
		})
	})

	Describe("Query", func() {
		Context("with matching entries", func() {
			BeforeEach(func() {
				entries := []learning.Entry{
					{
						Timestamp:   time.Now(),
						AgentID:     "agent-001",
						UserMessage: "How do I write tests in Go?",
						Response:    "Use Ginkgo framework.",
						ToolsUsed:   []string{"editor"},
						Outcome:     "success",
					},
					{
						Timestamp:   time.Now(),
						AgentID:     "agent-002",
						UserMessage: "Explain concurrency",
						Response:    "Use goroutines and channels.",
						ToolsUsed:   []string{"terminal"},
						Outcome:     "partial",
					},
					{
						Timestamp:   time.Now(),
						AgentID:     "agent-001",
						UserMessage: "What is BDD?",
						Response:    "Behaviour-Driven Development for testing.",
						ToolsUsed:   []string{},
						Outcome:     "success",
					},
				}

				for _, e := range entries {
					err := store.Capture(e)
					Expect(err).NotTo(HaveOccurred())
				}
			})

			It("returns entries matching UserMessage", func() {
				results := store.Query("tests")
				Expect(results).To(HaveLen(1))
				Expect(results[0].UserMessage).To(ContainSubstring("tests"))
			})

			It("returns entries matching Response", func() {
				results := store.Query("goroutines")
				Expect(results).To(HaveLen(1))
				Expect(results[0].Response).To(ContainSubstring("goroutines"))
			})

			It("returns entries matching Outcome", func() {
				results := store.Query("partial")
				Expect(results).To(HaveLen(1))
				Expect(results[0].Outcome).To(Equal("partial"))
			})

			It("returns multiple matches", func() {
				results := store.Query("success")
				Expect(results).To(HaveLen(2))
			})
		})

		Context("with no matching entries", func() {
			BeforeEach(func() {
				entry := learning.Entry{
					Timestamp:   time.Now(),
					AgentID:     "agent-001",
					UserMessage: "Sample question",
					Response:    "Sample answer",
					ToolsUsed:   []string{},
					Outcome:     "success",
				}
				err := store.Capture(entry)
				Expect(err).NotTo(HaveOccurred())
			})

			It("returns empty slice when no match found", func() {
				results := store.Query("nonexistent")
				Expect(results).To(BeEmpty())
			})
		})

		Context("with empty store", func() {
			It("returns empty slice", func() {
				results := store.Query("anything")
				Expect(results).To(BeEmpty())
			})
		})
	})
})
