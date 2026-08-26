package plan_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("Parse helpers", func() {
	It("round-trips parsed tasks through store persistence", func() {
		tmpDir, err := os.MkdirTemp("", "plan-parse-test-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		store, err := plan.NewStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())

		now := time.Now()
		original := plan.File{
			ID:        "demo",
			Title:     "Demo",
			Status:    "draft",
			CreatedAt: now,
			Tasks: []plan.Task{{
				Title:       "Task One",
				Description: "Task description",
				Skills:      []string{"go", "testing"},
			}},
		}

		Expect(store.Create(original)).To(Succeed())

		roundTrip, err := store.Get("demo")
		Expect(err).NotTo(HaveOccurred())
		Expect(roundTrip.ID).To(Equal("demo"))
		Expect(roundTrip.Title).To(Equal("Demo"))
		Expect(roundTrip.Tasks).To(HaveLen(1))
		Expect(roundTrip.Tasks[0].Title).To(Equal("Task One"))
		Expect(roundTrip.Tasks[0].Description).To(Equal("Task description"))
		Expect(roundTrip.Tasks[0].Skills).To(Equal([]string{"go", "testing"}))
	})

	It("round-trips a plan file with frontmatter and body", func() {
		tmpDir, err := os.MkdirTemp("", "plan-parse-file-*")
		Expect(err).NotTo(HaveOccurred())
		defer os.RemoveAll(tmpDir)

		store, err := plan.NewStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())

		err = os.WriteFile(filepath.Join(tmpDir, "demo.md"), []byte("---\nid: demo\ntitle: Demo\ncreated_at: "+time.Now().Format(time.RFC3339Nano)+"\n---\n## Task One\nTask body\n"), 0o600)
		Expect(err).NotTo(HaveOccurred())

		file, err := store.Get("demo")
		Expect(err).NotTo(HaveOccurred())
		Expect(file.ID).To(Equal("demo"))
		Expect(file.Title).To(Equal("Demo"))
		Expect(file.Tasks).NotTo(BeEmpty())
	})
})
