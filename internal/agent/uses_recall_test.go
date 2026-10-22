// Package agent_test exercises P13: per-agent opt-in to the RecallBroker.
// The Manifest must carry an explicit UsesRecall flag so the engine's
// context-assembly hook can skip querying the broker for agents that do
// not benefit from recall (tool-focused executors, router agents). The
// default is false — an omitted field means "no recall".
package agent_test

import (
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
)

var _ = Describe("Manifest UsesRecall (P13)", func() {
	Describe("YAML frontmatter parsing via LoadManifestMarkdown", func() {
		var tempDir string

		BeforeEach(func() {
			var err error
			tempDir, err = os.MkdirTemp("", "uses-recall-test")
			Expect(err).NotTo(HaveOccurred())
		})

		AfterEach(func() {
			os.RemoveAll(tempDir)
		})

		It("parses uses_recall: true from frontmatter", func() {
			mdPath := filepath.Join(tempDir, "recall-agent.md")
			mdContent := "---\nid: recall-agent\nname: Recall Agent\nuses_recall: true\n---\n# Agent\n"
			Expect(os.WriteFile(mdPath, []byte(mdContent), 0o600)).To(Succeed())

			m, err := agent.LoadManifest(mdPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.UsesRecall).To(BeTrue())
		})

		It("parses uses_recall: false from frontmatter", func() {
			mdPath := filepath.Join(tempDir, "norecall-agent.md")
			mdContent := "---\nid: norecall-agent\nname: No Recall Agent\nuses_recall: false\n---\n# Agent\n"
			Expect(os.WriteFile(mdPath, []byte(mdContent), 0o600)).To(Succeed())

			m, err := agent.LoadManifest(mdPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.UsesRecall).To(BeFalse())
		})

		It("defaults uses_recall to false when the field is missing", func() {
			mdPath := filepath.Join(tempDir, "default-agent.md")
			mdContent := "---\nid: default-agent\nname: Default Agent\n---\n# Agent\n"
			Expect(os.WriteFile(mdPath, []byte(mdContent), 0o600)).To(Succeed())

			m, err := agent.LoadManifest(mdPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(m.UsesRecall).To(BeFalse())
		})
	})

	Describe("JSON deserialisation", func() {
		It("parses uses_recall: true", func() {
			raw := `{"id":"x","name":"X","uses_recall":true}`
			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())
			Expect(m.UsesRecall).To(BeTrue())
		})

		It("defaults uses_recall to false when omitted", func() {
			raw := `{"id":"x","name":"X"}`
			var m agent.Manifest
			Expect(json.Unmarshal([]byte(raw), &m)).To(Succeed())
			Expect(m.UsesRecall).To(BeFalse())
		})
	})
})
