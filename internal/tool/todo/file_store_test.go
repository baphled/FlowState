package todo_test

import (
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
	todotool "github.com/baphled/flowstate/internal/tool/todo"
)

var _ = Describe("FileStore", func() {
	var (
		tmpDir string
		store  *todotool.FileStore
		sessID = "test-session"
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-todo-test-*")
		Expect(err).NotTo(HaveOccurred())
		store, err = todotool.NewFileStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	Describe("Set and Get", func() {
		It("persists todos and retrieves them", func() {
			items := []todotool.Item{
				{Content: "first task", Status: "pending", Priority: "high"},
				{Content: "second task", Status: "in_progress", Priority: "low"},
			}
			Expect(store.Set(sessID, items)).To(Succeed())

			got := store.Get(sessID)
			Expect(got).To(HaveLen(2))
			Expect(got[0].Content).To(Equal("first task"))
			Expect(got[1].Status).To(Equal("in_progress"))
		})

		It("replaces the entire list on Set", func() {
			Expect(store.Set(sessID, []todotool.Item{
				{Content: "old", Status: "pending", Priority: "low"},
			})).To(Succeed())
			Expect(store.Set(sessID, []todotool.Item{
				{Content: "new", Status: "completed", Priority: "high"},
			})).To(Succeed())

			got := store.Get(sessID)
			Expect(got).To(HaveLen(1))
			Expect(got[0].Content).To(Equal("new"))
		})

		It("returns a copy, not a reference to internal state", func() {
			Expect(store.Set(sessID, []todotool.Item{
				{Content: "original", Status: "pending", Priority: "low"},
			})).To(Succeed())

			got := store.Get(sessID)
			got[0].Content = "mutated"

			got2 := store.Get(sessID)
			Expect(got2[0].Content).To(Equal("original"))
		})
	})

	Describe("disk persistence", func() {
		It("survives store recreation (process restart simulation)", func() {
			items := []todotool.Item{
				{Content: "survive restart", Status: "pending", Priority: "high"},
			}
			Expect(store.Set(sessID, items)).To(Succeed())

			store2, err := todotool.NewFileStore(tmpDir)
			Expect(err).NotTo(HaveOccurred())

			got := store2.Get(sessID)
			Expect(got).To(HaveLen(1))
			Expect(got[0].Content).To(Equal("survive restart"))
		})

		It("writes a JSON file named after the session", func() {
			Expect(store.Set(sessID, []todotool.Item{
				{Content: "file check", Status: "pending", Priority: "medium"},
			})).To(Succeed())

			_, err := os.Stat(filepath.Join(tmpDir, sessID+".json"))
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns empty slice for a session that has never been set", func() {
			got := store.Get("never-seen")
			Expect(got).To(BeEmpty())
		})

		It("returns empty slice when the JSON file is missing on disk", func() {
			got := store.Get("no-file-session")
			Expect(got).To(BeEmpty())
		})
	})

	Describe("concurrent access", func() {
		It("handles concurrent Set calls for different sessions", func() {
			done := make(chan struct{})
			for i := 0; i < 10; i++ {
				go func(n int) {
					defer GinkgoRecover()
					sid := sessID + "-" + string(rune('A'+n))
					Expect(store.Set(sid, []todotool.Item{
						{Content: "concurrent", Status: "pending", Priority: "low"},
					})).To(Succeed())
					done <- struct{}{}
				}(i)
			}
			for i := 0; i < 10; i++ {
				<-done
			}
		})
	})
})

var _ = Describe("FileStore integration with tools", func() {
	var (
		tmpDir string
		store  todotool.Store
	)

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "flowstate-todo-int-*")
		Expect(err).NotTo(HaveOccurred())
		fs, err := todotool.NewFileStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())
		store = fs
	})

	AfterEach(func() {
		os.RemoveAll(tmpDir)
	})

	It("FileStore satisfies the Store interface", func() {
		var _ todotool.Store = store
	})

	It("works with the todowrite tool end-to-end", func() {
		t := todotool.New(store)
		ctx := context.WithValue(context.Background(), session.IDKey{}, "file-store-tool-test")
		result, err := t.Execute(ctx, tool.Input{
			Name: "todowrite",
			Arguments: map[string]interface{}{
				"todos": []interface{}{
					map[string]interface{}{
						"content":  "tool test",
						"status":   "pending",
						"priority": "high",
					},
				},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Output).To(ContainSubstring("tool test"))

		got := store.Get("file-store-tool-test")
		Expect(got).To(HaveLen(1))
		Expect(got[0].Content).To(Equal("tool test"))
	})
})
