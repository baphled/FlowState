package app_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
)

var _ = Describe("Config Generator", func() {
	Describe("DefaultHarnessConfig", func() {
		It("returns Enabled as true", func() {
			cfg := app.DefaultHarnessConfig()

			Expect(cfg.Enabled).To(BeTrue())
		})

		It("returns CriticEnabled as false", func() {
			cfg := app.DefaultHarnessConfig()

			Expect(cfg.CriticEnabled).To(BeFalse())
		})

		It("returns VotingEnabled as false", func() {
			cfg := app.DefaultHarnessConfig()

			Expect(cfg.VotingEnabled).To(BeFalse())
		})

		It("sets ProjectRoot to the current working directory", func() {
			cfg := app.DefaultHarnessConfig()

			Expect(cfg.ProjectRoot).NotTo(BeEmpty())
		})
	})

	Describe("HarnessConfigYAML", func() {
		It("returns valid YAML containing enabled: true", func() {
			yamlStr, err := app.HarnessConfigYAML()

			Expect(err).NotTo(HaveOccurred())
			Expect(yamlStr).To(ContainSubstring("enabled: true"))
		})

		It("contains critic_enabled field", func() {
			yamlStr, err := app.HarnessConfigYAML()

			Expect(err).NotTo(HaveOccurred())
			Expect(yamlStr).To(ContainSubstring("critic_enabled: false"))
		})

		It("contains voting_enabled field", func() {
			yamlStr, err := app.HarnessConfigYAML()

			Expect(err).NotTo(HaveOccurred())
			Expect(yamlStr).To(ContainSubstring("voting_enabled: false"))
		})
	})

	Describe("DefaultHarnessConfigForAgent", func() {
		Context("when delegation table contains plan-reviewer", func() {
			It("returns CriticEnabled as false", func() {
				manifest := agent.Manifest{
					ID: "planning-coordinator",
					Delegation: agent.Delegation{
						CanDelegate: true,
						DelegationTable: map[string]string{
							"plan-reviewer": "plan-reviewer",
						},
					},
				}

				cfg := app.DefaultHarnessConfigForAgent(manifest)

				Expect(cfg.CriticEnabled).To(BeFalse())
			})
		})

		Context("when delegation table does not contain plan-reviewer", func() {
			It("returns CriticEnabled as true", func() {
				manifest := agent.Manifest{
					ID: "simple-agent",
					Delegation: agent.Delegation{
						CanDelegate: true,
						DelegationTable: map[string]string{
							"explorer": "explorer",
						},
					},
				}

				cfg := app.DefaultHarnessConfigForAgent(manifest)

				Expect(cfg.CriticEnabled).To(BeTrue())
			})
		})

		Context("when delegation table is empty", func() {
			It("returns CriticEnabled as true", func() {
				manifest := agent.Manifest{
					ID: "no-delegation-agent",
					Delegation: agent.Delegation{
						CanDelegate:     false,
						DelegationTable: map[string]string{},
					},
				}

				cfg := app.DefaultHarnessConfigForAgent(manifest)

				Expect(cfg.CriticEnabled).To(BeTrue())
			})
		})

		Context("when delegation is disabled", func() {
			It("returns CriticEnabled as true", func() {
				manifest := agent.Manifest{
					ID: "no-delegation-agent",
					Delegation: agent.Delegation{
						CanDelegate:     false,
						DelegationTable: nil,
					},
				}

				cfg := app.DefaultHarnessConfigForAgent(manifest)

				Expect(cfg.CriticEnabled).To(BeTrue())
			})
		})

		It("returns Enabled as true regardless of delegation table", func() {
			manifest := agent.Manifest{
				ID: "test-agent",
				Delegation: agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"plan-reviewer": "plan-reviewer",
					},
				},
			}

			cfg := app.DefaultHarnessConfigForAgent(manifest)

			Expect(cfg.Enabled).To(BeTrue())
		})

		It("returns VotingEnabled as false regardless of delegation table", func() {
			manifest := agent.Manifest{
				ID: "test-agent",
				Delegation: agent.Delegation{
					CanDelegate: true,
					DelegationTable: map[string]string{
						"plan-reviewer": "plan-reviewer",
					},
				},
			}

			cfg := app.DefaultHarnessConfigForAgent(manifest)

			Expect(cfg.VotingEnabled).To(BeFalse())
		})
	})
})
