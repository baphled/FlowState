package app_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

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
})
