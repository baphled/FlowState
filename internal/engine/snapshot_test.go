package engine_test

import (
	"context"
	"flag"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/skill"
)

var update = flag.Bool("update", false, "update snapshot files")

type snapshotMockProvider struct {
	name string
}

func (m *snapshotMockProvider) Name() string { return m.name }

func (m *snapshotMockProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk)
	close(ch)
	return ch, nil
}

func (m *snapshotMockProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (m *snapshotMockProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (m *snapshotMockProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

var _ = Describe("Agent System Prompts", func() {
	var (
		chatProvider *snapshotMockProvider
		testdataDir  string
	)

	BeforeEach(func() {
		chatProvider = &snapshotMockProvider{
			name: "test-chat-provider",
		}

		testdataDir = "testdata"
	})

	DescribeTable("BuildSystemPrompt snapshots",
		func(agentFile, snapshotName string) {
			agentPath := filepath.Join("..", "..", "agents", agentFile)
			manifest, err := agent.LoadManifest(agentPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(manifest).NotTo(BeNil())

			eng := engine.New(engine.Config{
				ChatProvider: chatProvider,
				Manifest:     *manifest,
				Skills:       []skill.Skill{},
			})

			prompt := eng.BuildSystemPrompt()

			snapshotPath := filepath.Join(testdataDir, snapshotName)

			if *update {
				err := os.WriteFile(snapshotPath, []byte(prompt), 0o600)
				Expect(err).NotTo(HaveOccurred())
			} else {
				expected, err := os.ReadFile(snapshotPath)
				Expect(err).NotTo(HaveOccurred())
				Expect(prompt).To(Equal(string(expected)))
			}
		},
		Entry("worker agent", "worker.json", "snapshot_worker.txt"),
		Entry("researcher agent", "researcher.json", "snapshot_researcher.txt"),
		Entry("general agent", "general.json", "snapshot_general.txt"),
		Entry("coder agent", "coder.json", "snapshot_coder.txt"),
		Entry("importer agent", "Importer.md", "snapshot_importer.txt"),
	)
})
