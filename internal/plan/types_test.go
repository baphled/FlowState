package plan_test

import (
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"

	"github.com/baphled/flowstate/internal/plan"
)

var _ = Describe("Plan Types", func() {
	Describe("PlanFile", func() {
		var pf *plan.File

		BeforeEach(func() {
			pf = &plan.File{
				ID:          "plan-001",
				Title:       "Test Plan",
				Description: "A test plan for validation",
				Status:      "draft",
				CreatedAt:   time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
				Tasks: []plan.Task{
					{
						Title:       "Task 1",
						Description: "First task",
						Status:      "pending",
						AcceptanceCriteria: []string{
							"Must be completed",
							"Must pass tests",
						},
						Skills: []string{
							"golang",
							"testing",
						},
						Category: "implementation",
					},
					{
						Title:       "Task 2",
						Description: "Second task",
						Status:      "completed",
						Category:    "review",
					},
				},
			}
		})

		It("marshals to YAML and unmarshals back to equal struct", func() {
			yamlBytes, err := yaml.Marshal(pf)
			Expect(err).NotTo(HaveOccurred())
			Expect(yamlBytes).To(ContainSubstring("id: plan-001"))
			Expect(yamlBytes).To(ContainSubstring("title: Test Plan"))

			var unmarshalled plan.File
			err = yaml.Unmarshal(yamlBytes, &unmarshalled)
			Expect(err).NotTo(HaveOccurred())
			// YAML unmarshal converts empty slices to nil; verify key fields match
			Expect(unmarshalled.ID).To(Equal(pf.ID))
			Expect(unmarshalled.Title).To(Equal(pf.Title))
			Expect(unmarshalled.Description).To(Equal(pf.Description))
			Expect(unmarshalled.Status).To(Equal(pf.Status))
			Expect(unmarshalled.CreatedAt).To(Equal(pf.CreatedAt))
			Expect(unmarshalled.Tasks).To(HaveLen(len(pf.Tasks)))
		})

		It("marshals to JSON and unmarshals back to equal struct", func() {
			jsonBytes, err := json.Marshal(pf)
			Expect(err).NotTo(HaveOccurred())
			Expect(jsonBytes).To(ContainSubstring("plan-001"))
			Expect(jsonBytes).To(ContainSubstring("Test Plan"))

			var unmarshalled plan.File
			err = json.Unmarshal(jsonBytes, &unmarshalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(unmarshalled).To(Equal(*pf))
		})

		It("preserves task details through marshal/unmarshal cycles", func() {
			Expect(pf.Tasks).To(HaveLen(2))
			Expect(pf.Tasks[0].Title).To(Equal("Task 1"))
			Expect(pf.Tasks[0].AcceptanceCriteria).To(HaveLen(2))
			Expect(pf.Tasks[0].Skills).To(HaveLen(2))

			yamlBytes, _ := yaml.Marshal(pf)
			var unmarshalled plan.File
			yaml.Unmarshal(yamlBytes, &unmarshalled)

			Expect(unmarshalled.Tasks).To(HaveLen(2))
			Expect(unmarshalled.Tasks[0].Title).To(Equal("Task 1"))
			Expect(unmarshalled.Tasks[0].AcceptanceCriteria).To(Equal([]string{
				"Must be completed",
				"Must pass tests",
			}))
			Expect(unmarshalled.Tasks[0].Skills).To(Equal([]string{
				"golang",
				"testing",
			}))
		})

		It("handles timestamps correctly in YAML", func() {
			yamlBytes, _ := yaml.Marshal(pf)
			var unmarshalled plan.File
			yaml.Unmarshal(yamlBytes, &unmarshalled)

			Expect(unmarshalled.CreatedAt).To(Equal(pf.CreatedAt))
		})

		It("handles empty task list", func() {
			pf.Tasks = []plan.Task{}

			yamlBytes, _ := yaml.Marshal(pf)
			var unmarshalled plan.File
			yaml.Unmarshal(yamlBytes, &unmarshalled)

			Expect(unmarshalled.Tasks).To(BeEmpty())
		})
	})

	Describe("PlanTask", func() {
		var pt *plan.Task

		BeforeEach(func() {
			pt = &plan.Task{
				Title:       "Implement Feature",
				Description: "Add new feature to system",
				Status:      "in_progress",
				AcceptanceCriteria: []string{
					"Tests pass",
					"Documentation updated",
				},
				Skills: []string{
					"golang",
					"testing",
				},
				Category: "implementation",
			}
		})

		It("marshals to YAML and unmarshals back to equal struct", func() {
			yamlBytes, err := yaml.Marshal(pt)
			Expect(err).NotTo(HaveOccurred())

			var unmarshalled plan.Task
			err = yaml.Unmarshal(yamlBytes, &unmarshalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(unmarshalled).To(Equal(*pt))
		})

		It("marshals to JSON and unmarshals back to equal struct", func() {
			jsonBytes, err := json.Marshal(pt)
			Expect(err).NotTo(HaveOccurred())

			var unmarshalled plan.Task
			err = json.Unmarshal(jsonBytes, &unmarshalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(unmarshalled).To(Equal(*pt))
		})

		It("preserves criteria and skills through marshal cycles", func() {
			yamlBytes, _ := yaml.Marshal(pt)
			var unmarshalled plan.Task
			yaml.Unmarshal(yamlBytes, &unmarshalled)

			Expect(unmarshalled.AcceptanceCriteria).To(Equal([]string{
				"Tests pass",
				"Documentation updated",
			}))
			Expect(unmarshalled.Skills).To(Equal([]string{
				"golang",
				"testing",
			}))
		})

		It("handles tasks with empty criteria and skills", func() {
			pt.AcceptanceCriteria = []string{}
			pt.Skills = []string{}

			yamlBytes, _ := yaml.Marshal(pt)
			var unmarshalled plan.Task
			yaml.Unmarshal(yamlBytes, &unmarshalled)

			Expect(unmarshalled.AcceptanceCriteria).To(BeEmpty())
			Expect(unmarshalled.Skills).To(BeEmpty())
		})
	})

	Describe("PlanFrontmatter", func() {
		var pf *plan.Frontmatter

		BeforeEach(func() {
			pf = &plan.Frontmatter{
				ID:          "plan-001",
				Title:       "Test Plan",
				Description: "A test plan",
				Status:      "draft",
				CreatedAt:   time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC),
			}
		})

		It("marshals to YAML and unmarshals back to equal struct", func() {
			yamlBytes, err := yaml.Marshal(pf)
			Expect(err).NotTo(HaveOccurred())

			var unmarshalled plan.Frontmatter
			err = yaml.Unmarshal(yamlBytes, &unmarshalled)
			Expect(err).NotTo(HaveOccurred())
			Expect(unmarshalled).To(Equal(*pf))
		})

		It("preserves all fields through YAML marshal/unmarshal", func() {
			yamlBytes, _ := yaml.Marshal(pf)
			var unmarshalled plan.Frontmatter
			yaml.Unmarshal(yamlBytes, &unmarshalled)

			Expect(unmarshalled.ID).To(Equal("plan-001"))
			Expect(unmarshalled.Title).To(Equal("Test Plan"))
			Expect(unmarshalled.Description).To(Equal("A test plan"))
			Expect(unmarshalled.Status).To(Equal("draft"))
			Expect(unmarshalled.CreatedAt).To(Equal(pf.CreatedAt))
		})
	})
})
