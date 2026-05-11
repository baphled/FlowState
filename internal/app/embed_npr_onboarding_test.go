package app_test

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/swarm"
)

var _ = Describe("Embedded NPR onboarding assets", func() {
	It("ships the NPR onboarding agents, swarm, and profile schema", func() {
		agentsDir, err := fs.Sub(app.EmbeddedAgentsFS(), "agents")
		Expect(err).NotTo(HaveOccurred())
		for _, name := range []string{
			"npr-onboarding-lead.md",
			"npr-profile-synthesizer.md",
			"npr-quality-reviewer.md",
		} {
			body, err := fs.ReadFile(agentsDir, name)
			Expect(err).NotTo(HaveOccurred(), name)
			Expect(string(body)).To(ContainSubstring("model_policy: \"strict\""))
		}

		swarmsDir, err := fs.Sub(app.EmbeddedSwarmsFS(), "swarms")
		Expect(err).NotTo(HaveOccurred())
		body, err := fs.ReadFile(swarmsDir, "npr-onboarding.yml")
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(ContainSubstring("id: npr-onboarding"))

		schemasDir, err := fs.Sub(app.EmbeddedSchemasFS(), "schemas")
		Expect(err).NotTo(HaveOccurred())
		schemaBody, err := fs.ReadFile(schemasDir, "npr-profile-v01.json")
		Expect(err).NotTo(HaveOccurred())
		var schema map[string]any
		Expect(json.Unmarshal(schemaBody, &schema)).To(Succeed())
		Expect(schema).To(HaveKeyWithValue("title", "NPRProfile schema v0.1"))
	})

	It("registers @npr-onboarding against the bundled agent set", func() {
		swarmDest, err := os.MkdirTemp("", "embed-swarms-npr-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(swarmDest) })
		Expect(app.SeedSwarmsDir(app.EmbeddedSwarmsFS(), swarmDest)).To(Succeed())

		agentDest, err := os.MkdirTemp("", "embed-agents-npr-*")
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = os.RemoveAll(agentDest) })
		Expect(app.SeedAgentsDir(app.EmbeddedAgentsFS(), agentDest)).To(Succeed())

		agentReg := agent.NewRegistry()
		Expect(agentReg.Discover(filepath.Clean(agentDest))).To(Succeed())
		adapter := app.NewSwarmAgentRegistryAdapterForTest(agentReg)

		swarmReg, err := swarm.NewRegistryFromDir(swarmDest, adapter)
		Expect(err).NotTo(HaveOccurred())

		loaded, ok := swarmReg.Get("npr-onboarding")
		Expect(ok).To(BeTrue(), "expected @npr-onboarding to resolve in the swarm registry")
		Expect(loaded.Lead).To(Equal("npr-onboarding-lead"))
		Expect(loaded.Members).To(ConsistOf("npr-profile-synthesizer", "npr-quality-reviewer"))
	})
})
