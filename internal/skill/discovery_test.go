package skill_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/skill"
)

var _ = Describe("SkillDiscovery", func() {
	var discovery *skill.SkillDiscovery

	Describe("Suggest", func() {
		Context("when skills are available", func() {
			BeforeEach(func() {
				skills := []skill.Skill{
					{
						Name:        "bdd-workflow",
						Description: "Behaviour-Driven Development",
						Category:    "testing",
						WhenToUse:   "Writing tests before implementation, BDD style specs",
					},
					{
						Name:        "golang",
						Description: "Go language expertise",
						Category:    "language",
						WhenToUse:   "Writing Go code, concurrency patterns",
					},
					{
						Name:        "security",
						Description: "Secure coding practices",
						Category:    "security",
						WhenToUse:   "Security audits, vulnerability assessment",
					},
				}
				discovery = skill.NewSkillDiscovery(skills)
			})

			It("suggests matching skill for relevant task description", func() {
				suggestions := discovery.Suggest("I need to write BDD tests")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].Name).To(Equal("bdd-workflow"))
				Expect(suggestions[0].Confidence).To(BeNumerically(">", 0.5))
				Expect(suggestions[0].Reason).NotTo(BeEmpty())
			})

			It("returns empty for unrelated task", func() {
				suggestions := discovery.Suggest("deploy to kubernetes production")

				Expect(suggestions).To(BeEmpty())
			})

			It("weights WhenToUse higher than Category", func() {
				skillsWithOverlap := []skill.Skill{
					{
						Name:        "testing-basics",
						Description: "Basic testing",
						Category:    "testing",
						WhenToUse:   "General test writing",
					},
					{
						Name:        "bdd-workflow",
						Description: "BDD testing",
						Category:    "methodology",
						WhenToUse:   "Writing BDD tests with Ginkgo, test-first development",
					},
				}
				discovery = skill.NewSkillDiscovery(skillsWithOverlap)

				suggestions := discovery.Suggest("BDD tests Ginkgo")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].Name).To(Equal("bdd-workflow"))
			})

			It("weights Category higher than Name", func() {
				skillsWithOverlap := []skill.Skill{
					{
						Name:        "security-scanner",
						Description: "Security scanning",
						Category:    "devops",
						WhenToUse:   "CI/CD pipeline scanning",
					},
					{
						Name:        "code-review",
						Description: "Code review practices",
						Category:    "security",
						WhenToUse:   "Reviewing code for quality",
					},
				}
				discovery = skill.NewSkillDiscovery(skillsWithOverlap)

				suggestions := discovery.Suggest("security audit")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].Name).To(Equal("code-review"))
			})

			It("returns multiple matches sorted by confidence", func() {
				suggestions := discovery.Suggest("testing Go code")

				Expect(len(suggestions)).To(BeNumerically(">=", 2))
				for i := 0; i < len(suggestions)-1; i++ {
					Expect(suggestions[i].Confidence).To(BeNumerically(">=", suggestions[i+1].Confidence))
				}
			})
		})

		Context("when skill set is empty", func() {
			BeforeEach(func() {
				discovery = skill.NewSkillDiscovery([]skill.Skill{})
			})

			It("handles empty skill set gracefully", func() {
				suggestions := discovery.Suggest("any task description")

				Expect(suggestions).To(BeEmpty())
			})
		})

		Context("when task description is empty", func() {
			BeforeEach(func() {
				skills := []skill.Skill{
					{
						Name:      "golang",
						WhenToUse: "Writing Go code",
					},
				}
				discovery = skill.NewSkillDiscovery(skills)
			})

			It("returns empty for empty task description", func() {
				suggestions := discovery.Suggest("")

				Expect(suggestions).To(BeEmpty())
			})
		})
	})
})
