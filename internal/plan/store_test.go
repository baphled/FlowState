package plan_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("PlanStore", func() {
	var store *plan.PlanStore
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "plan-store-test-*")
		Expect(err).NotTo(HaveOccurred())

		store, err = plan.NewPlanStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if tmpDir != "" {
			os.RemoveAll(tmpDir)
		}
	})

	Describe("NewPlanStore", func() {
		It("creates a new store pointing to the directory", func() {
			testStore, err := plan.NewPlanStore(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(testStore).NotTo(BeNil())
		})

		It("creates the directory if it does not exist", func() {
			newDir := filepath.Join(tmpDir, "nested", "dir")
			_, err := plan.NewPlanStore(newDir)
			Expect(err).NotTo(HaveOccurred())

			info, err := os.Stat(newDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeTrue())
		})
	})

	Describe("Create", func() {
		It("writes a plan file to disk", func() {
			now := time.Now()
			f := plan.File{
				ID:          "test-plan",
				Title:       "Test Plan",
				Description: "A test plan",
				Status:      "draft",
				CreatedAt:   now,
				Tasks: []plan.Task{
					{
						Title:              "Task 1",
						Description:        "First task",
						Status:             "pending",
						AcceptanceCriteria: []string{"Criterion 1", "Criterion 2"},
						Skills:             []string{"golang", "testing"},
					},
				},
			}

			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			filePath := filepath.Join(tmpDir, "test-plan.md")
			info, err := os.Stat(filePath)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.IsDir()).To(BeFalse())
		})

		It("includes YAML frontmatter in the file", func() {
			f := plan.File{
				ID:          "yml-test",
				Title:       "YAML Test",
				Description: "Test YAML serialization",
				Status:      "ready",
				CreatedAt:   time.Now(),
				Tasks:       []plan.Task{},
			}

			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(filepath.Join(tmpDir, "yml-test.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("---"))
			Expect(string(data)).To(ContainSubstring("id: yml-test"))
			Expect(string(data)).To(ContainSubstring("title: YAML Test"))
		})

		It("includes tasks in the markdown body", func() {
			f := plan.File{
				ID:    "task-test",
				Title: "Task Test",
				Tasks: []plan.Task{
					{
						Title:       "Do Something",
						Description: "This is what to do",
					},
				},
				CreatedAt: time.Now(),
			}

			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(filepath.Join(tmpDir, "task-test.md"))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).To(ContainSubstring("## Do Something"))
			Expect(string(data)).To(ContainSubstring("This is what to do"))
		})

		It("overwrites existing files", func() {
			f1 := plan.File{
				ID:        "existing",
				Title:     "Original Title",
				Status:    "draft",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err := store.Create(f1)
			Expect(err).NotTo(HaveOccurred())

			f2 := plan.File{
				ID:        "existing",
				Title:     "Updated Title",
				Status:    "ready",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err = store.Create(f2)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := store.Get("existing")
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved.Title).To(Equal("Updated Title"))
		})
	})

	Describe("List", func() {
		It("returns an empty list when no plans exist", func() {
			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(BeEmpty())
		})

		It("returns summaries of all plans", func() {
			f1 := plan.File{
				ID:        "plan-1",
				Title:     "First Plan",
				Status:    "draft",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err := store.Create(f1)
			Expect(err).NotTo(HaveOccurred())

			f2 := plan.File{
				ID:        "plan-2",
				Title:     "Second Plan",
				Status:    "ready",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err = store.Create(f2)
			Expect(err).NotTo(HaveOccurred())

			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(HaveLen(2))
		})

		It("includes correct metadata in summaries", func() {
			now := time.Now()
			f := plan.File{
				ID:        "metadata-plan",
				Title:     "Metadata Test",
				Status:    "in-progress",
				CreatedAt: now,
				Tasks:     []plan.Task{},
			}
			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(HaveLen(1))

			summary := summaries[0]
			Expect(summary.ID).To(Equal("metadata-plan"))
			Expect(summary.Title).To(Equal("Metadata Test"))
			Expect(summary.Status).To(Equal("in-progress"))
		})

		It("skips invalid markdown files", func() {
			filePath := filepath.Join(tmpDir, "invalid.md")
			err := os.WriteFile(filePath, []byte("not valid frontmatter"), 0o600)
			Expect(err).NotTo(HaveOccurred())

			f := plan.File{
				ID:        "valid-plan",
				Title:     "Valid Plan",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err = store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(HaveLen(1))
			Expect(summaries[0].ID).To(Equal("valid-plan"))
		})
	})

	Describe("Get", func() {
		It("retrieves a plan by ID", func() {
			f := plan.File{
				ID:          "get-test",
				Title:       "Get Test Plan",
				Description: "Testing retrieval",
				Status:      "draft",
				CreatedAt:   time.Now(),
				Tasks:       []plan.Task{},
			}
			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := store.Get("get-test")
			Expect(err).NotTo(HaveOccurred())
			Expect(retrieved).NotTo(BeNil())
			Expect(retrieved.ID).To(Equal("get-test"))
			Expect(retrieved.Title).To(Equal("Get Test Plan"))
		})

		Context("when the plan contains tasks", func() {
			It("returns the tasks in the File", func() {
				now := time.Now()
				f := plan.File{
					ID:          "tasks-test",
					Title:       "Plan with Tasks",
					Description: "Testing task parsing",
					Status:      "draft",
					CreatedAt:   now,
					Tasks: []plan.Task{
						{
							Title:              "Task 1",
							Description:        "First task description",
							Status:             "pending",
							AcceptanceCriteria: []string{"Criterion 1", "Criterion 2"},
							Skills:             []string{"golang", "testing"},
						},
						{
							Title:       "Task 2",
							Description: "Second task description",
							Status:      "pending",
							Skills:      []string{"ginkgo"},
						},
					},
				}
				err := store.Create(f)
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := store.Get("tasks-test")
				Expect(err).NotTo(HaveOccurred())
				Expect(retrieved.Tasks).To(HaveLen(2))

				Expect(retrieved.Tasks[0].Title).To(Equal("Task 1"))
				Expect(retrieved.Tasks[0].Description).To(Equal("First task description"))
				Expect(retrieved.Tasks[0].AcceptanceCriteria).To(HaveLen(2))
				Expect(retrieved.Tasks[0].AcceptanceCriteria[0]).To(Equal("Criterion 1"))
				Expect(retrieved.Tasks[0].Skills).To(HaveLen(2))
				Expect(retrieved.Tasks[0].Skills[0]).To(Equal("golang"))

				Expect(retrieved.Tasks[1].Title).To(Equal("Task 2"))
				Expect(retrieved.Tasks[1].Description).To(Equal("Second task description"))
				Expect(retrieved.Tasks[1].Skills).To(HaveLen(1))
			})
		})

		It("returns error for nonexistent plan", func() {
			_, err := store.Get("nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("preserves all plan metadata", func() {
			now := time.Now()
			original := plan.File{
				ID:          "metadata-test",
				Title:       "Metadata Preservation",
				Description: "Check all fields survive roundtrip",
				Status:      "ready",
				CreatedAt:   now,
				Tasks:       []plan.Task{},
			}
			err := store.Create(original)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := store.Get("metadata-test")
			Expect(err).NotTo(HaveOccurred())

			Expect(retrieved.ID).To(Equal(original.ID))
			Expect(retrieved.Title).To(Equal(original.Title))
			Expect(retrieved.Description).To(Equal(original.Description))
			Expect(retrieved.Status).To(Equal(original.Status))
		})
	})

	Describe("Delete", func() {
		It("removes a plan file from disk", func() {
			f := plan.File{
				ID:        "delete-test",
				Title:     "Plan to Delete",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			err = store.Delete("delete-test")
			Expect(err).NotTo(HaveOccurred())

			filePath := filepath.Join(tmpDir, "delete-test.md")
			_, err = os.Stat(filePath)
			Expect(err).To(HaveOccurred())
		})

		It("returns error when deleting nonexistent plan", func() {
			err := store.Delete("nonexistent")
			Expect(err).To(HaveOccurred())
		})

		It("removes plan from list after deletion", func() {
			f := plan.File{
				ID:        "list-delete-test",
				Title:     "Plan",
				CreatedAt: time.Now(),
				Tasks:     []plan.Task{},
			}
			err := store.Create(f)
			Expect(err).NotTo(HaveOccurred())

			err = store.Delete("list-delete-test")
			Expect(err).NotTo(HaveOccurred())

			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(BeEmpty())
		})
	})

	Describe("CRUD Roundtrip", func() {
		It("survives create-list-get-delete cycle", func() {
			plans := []plan.File{
				{
					ID:          "plan-a",
					Title:       "Plan A",
					Description: "First plan",
					Status:      "draft",
					CreatedAt:   time.Now(),
					Tasks: []plan.Task{
						{
							Title:              "Task A1",
							Description:        "First task of plan A",
							AcceptanceCriteria: []string{"A1 done", "A1 tested"},
							Skills:             []string{"golang"},
						},
					},
				},
				{
					ID:          "plan-b",
					Title:       "Plan B",
					Description: "Second plan",
					Status:      "ready",
					CreatedAt:   time.Now(),
					Tasks: []plan.Task{
						{
							Title:       "Task B1",
							Description: "First task of plan B",
						},
					},
				},
			}

			for _, p := range plans {
				err := store.Create(p)
				Expect(err).NotTo(HaveOccurred())
			}

			summaries, err := store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(HaveLen(2))

			for _, p := range plans {
				retrieved, err := store.Get(p.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(retrieved.ID).To(Equal(p.ID))
				Expect(retrieved.Title).To(Equal(p.Title))
			}

			err = store.Delete("plan-a")
			Expect(err).NotTo(HaveOccurred())

			summaries, err = store.List()
			Expect(err).NotTo(HaveOccurred())
			Expect(summaries).To(HaveLen(1))
			Expect(summaries[0].ID).To(Equal("plan-b"))
		})
	})

	Describe("FileStruct", func() {
		Context("with validation metadata fields", func() {
			It("preserves validation metadata in roundtrip", func() {
				f := plan.File{
					ID:               "validation-test",
					Title:            "Validation Test",
					Description:      "Testing validation fields",
					Status:           "draft",
					CreatedAt:        time.Now(),
					ValidationStatus: "pending",
					AttemptCount:     3,
					Score:            0.85,
					ValidationErrors: []string{"error1", "error2"},
					Tasks:            []plan.Task{},
				}

				err := store.Create(f)
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := store.Get("validation-test")
				Expect(err).NotTo(HaveOccurred())

				Expect(retrieved.ValidationStatus).To(Equal("pending"))
				Expect(retrieved.AttemptCount).To(Equal(3))
				Expect(retrieved.Score).To(Equal(0.85))
				Expect(retrieved.ValidationErrors).To(HaveLen(2))
				Expect(retrieved.ValidationErrors[0]).To(Equal("error1"))
				Expect(retrieved.ValidationErrors[1]).To(Equal("error2"))
			})

			It("handles missing validation fields with zero values", func() {
				f := plan.File{
					ID:        "no-validation",
					Title:     "No Validation",
					Status:    "draft",
					CreatedAt: time.Now(),
					Tasks:     []plan.Task{},
				}

				err := store.Create(f)
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := store.Get("no-validation")
				Expect(err).NotTo(HaveOccurred())

				Expect(retrieved.ValidationStatus).To(Equal(""))
				Expect(retrieved.AttemptCount).To(Equal(0))
				Expect(retrieved.Score).To(Equal(0.0))
				Expect(retrieved.ValidationErrors).To(BeEmpty())
			})
		})

		Context("with task metadata fields", func() {
			It("preserves task dependencies, effort, and wave", func() {
				f := plan.File{
					ID:        "task-metadata-test",
					Title:     "Task Metadata Test",
					Status:    "draft",
					CreatedAt: time.Now(),
					Tasks: []plan.Task{
						{
							Title:           "Task with metadata",
							Description:     "Task description",
							Dependencies:    []string{"task-1", "task-2"},
							EstimatedEffort: "medium",
							Wave:            2,
						},
					},
				}

				err := store.Create(f)
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := store.Get("task-metadata-test")
				Expect(err).NotTo(HaveOccurred())

				Expect(retrieved.Tasks).To(HaveLen(1))
				task := retrieved.Tasks[0]
				Expect(task.Dependencies).To(HaveLen(2))
				Expect(task.Dependencies[0]).To(Equal("task-1"))
				Expect(task.Dependencies[1]).To(Equal("task-2"))
				Expect(task.EstimatedEffort).To(Equal("medium"))
				Expect(task.Wave).To(Equal(2))
			})

			It("handles missing task metadata fields with zero values", func() {
				f := plan.File{
					ID:        "task-no-metadata",
					Title:     "Task No Metadata",
					Status:    "draft",
					CreatedAt: time.Now(),
					Tasks: []plan.Task{
						{
							Title:       "Simple task",
							Description: "No metadata",
						},
					},
				}

				err := store.Create(f)
				Expect(err).NotTo(HaveOccurred())

				retrieved, err := store.Get("task-no-metadata")
				Expect(err).NotTo(HaveOccurred())

				Expect(retrieved.Tasks).To(HaveLen(1))
				task := retrieved.Tasks[0]
				Expect(task.Dependencies).To(BeEmpty())
				Expect(task.EstimatedEffort).To(Equal(""))
				Expect(task.Wave).To(Equal(0))
			})
		})
	})
})
