package discovery_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/discovery"
)

var _ = Describe("AgentDiscovery", func() {
	var (
		ad        *discovery.AgentDiscovery
		manifests []agent.Manifest
	)

	Describe("Suggest", func() {
		Context("when manifests include a coder agent", func() {
			BeforeEach(func() {
				manifests = []agent.Manifest{
					{
						ID:   "coder-agent",
						Name: "Coder",
						Metadata: agent.Metadata{
							Role:      "software engineer",
							Goal:      "write clean maintainable code",
							WhenToUse: "writing code implementing features fixing bugs",
						},
						Capabilities: agent.Capabilities{
							Tools:  []string{"edit", "write", "bash"},
							Skills: []string{"golang", "testing"},
						},
					},
					{
						ID:   "researcher-agent",
						Name: "Researcher",
						Metadata: agent.Metadata{
							Role:      "technical researcher",
							Goal:      "investigate and analyse systems",
							WhenToUse: "researching exploring codebase understanding architecture",
						},
						Capabilities: agent.Capabilities{
							Tools:  []string{"read", "grep"},
							Skills: []string{"investigation", "analysis"},
						},
					},
				}
				ad = discovery.NewAgentDiscovery(manifests)
			})

			It("matches coding task to coder agent", func() {
				suggestions := ad.Suggest("write a function to implement user authentication")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].AgentID).To(Equal("coder-agent"))
				Expect(suggestions[0].Confidence).To(BeNumerically(">=", 0.3))
				Expect(suggestions[0].Reason).To(ContainSubstring("WhenToUse"))
			})

			It("matches research task to researcher agent", func() {
				suggestions := ad.Suggest("research how the authentication system works")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].AgentID).To(Equal("researcher-agent"))
				Expect(suggestions[0].Confidence).To(BeNumerically(">=", 0.3))
				Expect(suggestions[0].Reason).To(ContainSubstring("WhenToUse"))
			})
		})

		Context("when message does not match any agent", func() {
			BeforeEach(func() {
				manifests = []agent.Manifest{
					{
						ID:   "coder-agent",
						Name: "Coder",
						Metadata: agent.Metadata{
							Role:      "software engineer",
							Goal:      "write code",
							WhenToUse: "writing code implementing features",
						},
					},
				}
				ad = discovery.NewAgentDiscovery(manifests)
			})

			It("returns empty suggestions for unrelated message", func() {
				suggestions := ad.Suggest("make me a sandwich")

				Expect(suggestions).To(BeEmpty())
			})
		})

		Context("when multiple agents match", func() {
			BeforeEach(func() {
				manifests = []agent.Manifest{
					{
						ID:   "general-coder",
						Name: "General Coder",
						Metadata: agent.Metadata{
							Role:      "developer",
							Goal:      "write code",
							WhenToUse: "writing code",
						},
					},
					{
						ID:   "go-specialist",
						Name: "Go Specialist",
						Metadata: agent.Metadata{
							Role:      "Go expert engineer",
							Goal:      "write idiomatic Go code",
							WhenToUse: "writing Go code implementing Go features golang development",
						},
					},
				}
				ad = discovery.NewAgentDiscovery(manifests)
			})

			It("returns suggestions sorted by confidence descending", func() {
				suggestions := ad.Suggest("write Go code for a REST API")

				Expect(suggestions).To(HaveLen(2))
				Expect(suggestions[0].AgentID).To(Equal("go-specialist"))
				Expect(suggestions[1].AgentID).To(Equal("general-coder"))
				Expect(suggestions[0].Confidence).To(BeNumerically(">", suggestions[1].Confidence))
			})
		})

		Context("when manifests are empty", func() {
			BeforeEach(func() {
				ad = discovery.NewAgentDiscovery(nil)
			})

			It("returns empty suggestions", func() {
				suggestions := ad.Suggest("write some code")

				Expect(suggestions).To(BeEmpty())
			})
		})

		Context("when message is empty", func() {
			BeforeEach(func() {
				manifests = []agent.Manifest{
					{
						ID:   "coder-agent",
						Name: "Coder",
						Metadata: agent.Metadata{
							WhenToUse: "writing code",
						},
					},
				}
				ad = discovery.NewAgentDiscovery(manifests)
			})

			It("returns empty suggestions", func() {
				suggestions := ad.Suggest("")

				Expect(suggestions).To(BeEmpty())
			})
		})

		Context("when matching by Role field", func() {
			BeforeEach(func() {
				manifests = []agent.Manifest{
					{
						ID:   "qa-agent",
						Name: "QA Engineer",
						Metadata: agent.Metadata{
							Role:      "quality assurance engineer tester",
							Goal:      "ensure software quality",
							WhenToUse: "reviewing test coverage",
						},
					},
				}
				ad = discovery.NewAgentDiscovery(manifests)
			})

			It("matches based on Role with appropriate weight", func() {
				suggestions := ad.Suggest("need a tester to check this")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].AgentID).To(Equal("qa-agent"))
				Expect(suggestions[0].Reason).To(ContainSubstring("Role"))
			})
		})

		Context("when matching by Goal field", func() {
			BeforeEach(func() {
				manifests = []agent.Manifest{
					{
						ID:   "security-agent",
						Name: "Security Engineer",
						Metadata: agent.Metadata{
							Role:      "specialist",
							Goal:      "identify vulnerabilities protect systems",
							WhenToUse: "audits",
						},
					},
				}
				ad = discovery.NewAgentDiscovery(manifests)
			})

			It("matches based on Goal with appropriate weight", func() {
				suggestions := ad.Suggest("identify vulnerabilities protect")

				Expect(suggestions).NotTo(BeEmpty())
				Expect(suggestions[0].AgentID).To(Equal("security-agent"))
				Expect(suggestions[0].Reason).To(ContainSubstring("Goal"))
			})
		})
	})
})
