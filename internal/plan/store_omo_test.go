package plan_test

import (
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("PlanStore OMO-Style Sections", func() {
	var store *plan.PlanStore
	var tmpDir string

	BeforeEach(func() {
		var err error
		tmpDir, err = os.MkdirTemp("", "plan-store-omo-test-*")
		Expect(err).NotTo(HaveOccurred())

		store, err = plan.NewPlanStore(tmpDir)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		if tmpDir != "" {
			os.RemoveAll(tmpDir)
		}
	})

	Describe("Round-trip with OMO-style sections", func() {
		It("preserves all new OMO fields through create-get cycle", func() {
			original := plan.File{
				ID:                   "omo-test",
				Title:                "OMO Test Plan",
				Description:          "Testing OMO sections",
				Status:               "draft",
				CreatedAt:            time.Now(),
				TLDR:                 "Quick summary of the plan",
				VerificationStrategy: "Run all tests and lint",
				Context: plan.Context{
					OriginalRequest:  "Implement the feature",
					InterviewSummary: "Discussed requirements with team",
					ResearchFindings: "Found relevant documentation",
				},
				WorkObjectives: plan.WorkObjectives{
					CoreObjective:    "Deliver working feature",
					Deliverables:     []string{"Code", "Tests", "Docs"},
					DefinitionOfDone: []string{"Tests pass", "Code reviewed"},
					MustHave:         []string{"Tests"},
					MustNotHave:      []string{"Debug code"},
				},
				Reviews: []plan.ReviewResult{
					{
						Reviewer:    "senior-engineer",
						Verdict:     "approved",
						Confidence:  0.9,
						Issues:      []string{},
						Suggestions: []string{"Consider adding more tests"},
					},
				},
				Tasks: []plan.Task{
					{
						Title:       "Task 1",
						Description: "First task",
						Status:      "pending",
						AcceptanceCriteria: []string{
							"Must be completed",
						},
						Category:    "implementation",
						FileChanges: []string{"internal/types.go", "internal/types_test.go"},
						Evidence:    "All tests pass with race detector",
					},
				},
			}

			err := store.Create(original)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := store.Get("omo-test")
			Expect(err).NotTo(HaveOccurred())

			Expect(retrieved.TLDR).To(Equal("Quick summary of the plan"))
			Expect(retrieved.VerificationStrategy).To(Equal("Run all tests and lint"))
			Expect(retrieved.Context.OriginalRequest).To(Equal("Implement the feature"))
			Expect(retrieved.Context.InterviewSummary).To(Equal("Discussed requirements with team"))
			Expect(retrieved.Context.ResearchFindings).To(Equal("Found relevant documentation"))
			Expect(retrieved.WorkObjectives.CoreObjective).To(Equal("Deliver working feature"))
			Expect(retrieved.WorkObjectives.Deliverables).To(Equal([]string{"Code", "Tests", "Docs"}))
			Expect(retrieved.WorkObjectives.DefinitionOfDone).To(Equal([]string{"Tests pass", "Code reviewed"}))
			Expect(retrieved.WorkObjectives.MustHave).To(Equal([]string{"Tests"}))
			Expect(retrieved.WorkObjectives.MustNotHave).To(Equal([]string{"Debug code"}))
			Expect(retrieved.Reviews).To(HaveLen(1))
			Expect(retrieved.Reviews[0].Reviewer).To(Equal("senior-engineer"))
			Expect(retrieved.Reviews[0].Verdict).To(Equal("approved"))
			Expect(retrieved.Reviews[0].Confidence).To(Equal(0.9))
			Expect(retrieved.Reviews[0].Suggestions).To(HaveLen(1))

			Expect(retrieved.Tasks).To(HaveLen(1))
			Expect(retrieved.Tasks[0].FileChanges).To(Equal([]string{"internal/types.go", "internal/types_test.go"}))
			Expect(retrieved.Tasks[0].Evidence).To(Equal("All tests pass with race detector"))
		})

		It("handles plans without OMO sections (backwards compatibility)", func() {
			original := plan.File{
				ID:          "legacy-test",
				Title:       "Legacy Plan",
				Description: "Old plan without OMO fields",
				Status:      "draft",
				CreatedAt:   time.Now(),
				Tasks: []plan.Task{
					{
						Title:       "Task 1",
						Description: "First task",
						Status:      "pending",
					},
				},
			}

			err := store.Create(original)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := store.Get("legacy-test")
			Expect(err).NotTo(HaveOccurred())

			Expect(retrieved.TLDR).To(BeEmpty())
			Expect(retrieved.VerificationStrategy).To(BeEmpty())
			Expect(retrieved.Context).To(BeZero())
			Expect(retrieved.WorkObjectives).To(BeZero())
			Expect(retrieved.Reviews).To(BeNil())

			Expect(retrieved.Tasks).To(HaveLen(1))
			Expect(retrieved.Tasks[0].FileChanges).To(BeEmpty())
			Expect(retrieved.Tasks[0].Evidence).To(BeEmpty())
		})

		It("writes TLDR and VerificationStrategy to YAML frontmatter", func() {
			original := plan.File{
				ID:                   "frontmatter-test",
				Title:                "Frontmatter Test",
				Description:          "Test frontmatter fields",
				Status:               "draft",
				CreatedAt:            time.Now(),
				TLDR:                 "Short summary",
				VerificationStrategy: "Run tests",
				Tasks:                []plan.Task{},
			}

			err := store.Create(original)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(filepath.Join(tmpDir, "frontmatter-test.md"))
			Expect(err).NotTo(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("tldr: Short summary"))
			Expect(content).To(ContainSubstring("verification_strategy: Run tests"))
		})

		It("writes OMO sections to markdown body", func() {
			original := plan.File{
				ID:        "markdown-test",
				Title:     "Markdown Body Test",
				Status:    "draft",
				CreatedAt: time.Now(),
				Context: plan.Context{
					OriginalRequest: "Test request",
				},
				WorkObjectives: plan.WorkObjectives{
					CoreObjective: "Test objective",
					Deliverables:  []string{"Deliverable 1"},
				},
				Reviews: []plan.ReviewResult{
					{
						Reviewer: "reviewer-1",
						Verdict:  "approved",
					},
				},
				Tasks: []plan.Task{
					{
						Title: "Task 1",
					},
				},
			}

			err := store.Create(original)
			Expect(err).NotTo(HaveOccurred())

			data, err := os.ReadFile(filepath.Join(tmpDir, "markdown-test.md"))
			Expect(err).NotTo(HaveOccurred())

			content := string(data)
			Expect(content).To(ContainSubstring("## Context"))
			Expect(content).To(ContainSubstring("**Original Request**: Test request"))
			Expect(content).To(ContainSubstring("## Work Objectives"))
			Expect(content).To(ContainSubstring("**Core Objective**: Test objective"))
			Expect(content).To(ContainSubstring("**Deliverables**:"))
			Expect(content).To(ContainSubstring("- Deliverable 1"))
			Expect(content).To(ContainSubstring("## Reviews"))
			Expect(content).To(ContainSubstring("**Reviewer**: reviewer-1"))
			Expect(content).To(ContainSubstring("**Verdict**: approved"))
		})

		It("handles empty optional sections gracefully", func() {
			original := plan.File{
				ID:        "empty-sections-test",
				Title:     "Empty Sections Test",
				Status:    "draft",
				CreatedAt: time.Now(),
				Context: plan.Context{
					OriginalRequest: "Test",
				},
				WorkObjectives: plan.WorkObjectives{
					CoreObjective: "Test",
				},
				Tasks: []plan.Task{
					{
						Title: "Task 1",
					},
				},
			}

			err := store.Create(original)
			Expect(err).NotTo(HaveOccurred())

			retrieved, err := store.Get("empty-sections-test")
			Expect(err).NotTo(HaveOccurred())

			Expect(retrieved.Context.InterviewSummary).To(BeEmpty())
			Expect(retrieved.Context.ResearchFindings).To(BeEmpty())
			Expect(retrieved.WorkObjectives.Deliverables).To(BeEmpty())
			Expect(retrieved.WorkObjectives.DefinitionOfDone).To(BeEmpty())
		})
	})
})
