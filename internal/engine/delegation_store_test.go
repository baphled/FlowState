package engine_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/tool"
)

// testStoreFactory implements engine.DelegateStoreFactory backed by a temp directory.
type testStoreFactory struct {
	sessionsDir string
}

func (f *testStoreFactory) CreateSessionStore(sessionID string) (*recall.FileContextStore, error) {
	path := filepath.Join(f.sessionsDir, sessionID+".json")
	return recall.NewFileContextStore(path, "")
}

// errorStoreFactory always returns an error from CreateSessionStore.
type errorStoreFactory struct{}

func (f *errorStoreFactory) CreateSessionStore(_ string) (*recall.FileContextStore, error) {
	return nil, errors.New("factory error")
}

var _ = Describe("DelegateTool store factory", func() {
	var (
		qaProvider *mockProvider
		qaEngine   *engine.Engine
		engines    map[string]*engine.Engine
		delegation agent.Delegation
	)

	BeforeEach(func() {
		qaProvider = &mockProvider{
			name: "qa-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "delegated response", Done: true},
			},
		}

		qaManifest := agent.Manifest{
			ID:                "qa-agent",
			Name:              "QA Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are QA."},
			ContextManagement: agent.DefaultContextManagement(),
		}

		qaEngine = engine.New(engine.Config{
			ChatProvider: qaProvider,
			Manifest:     qaManifest,
		})

		engines = map[string]*engine.Engine{
			"qa-agent": qaEngine,
		}

		delegation = agent.Delegation{
			CanDelegate: true,
		}
	})

	Describe("WithStoreFactory", func() {
		It("returns the DelegateTool for chaining", func() {
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
			factory := &testStoreFactory{sessionsDir: GinkgoT().TempDir()}

			result := delegateTool.WithStoreFactory(factory)

			Expect(result).To(Equal(delegateTool))
		})
	})

	Describe("when factory returns an error", func() {
		It("delegation still succeeds and does not attach a store to the engine", func() {
			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
			delegateTool.WithStoreFactory(&errorStoreFactory{})

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("delegated response"))
		})
	})

	Describe("executeSync with store factory", func() {
		It("creates a JSON file when store factory is set", func() {
			tmpDir := GinkgoT().TempDir()
			factory := &testStoreFactory{sessionsDir: tmpDir}

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
			delegateTool.WithStoreFactory(factory)

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("delegated response"))

			entries, err := os.ReadDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).NotTo(BeEmpty())

			var jsonFiles []string
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".json" {
					jsonFiles = append(jsonFiles, e.Name())
				}
			}
			Expect(jsonFiles).To(HaveLen(1))
		})

		It("does not create a file when no store factory is set", func() {
			tmpDir := GinkgoT().TempDir()

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "Run the tests",
				},
			}

			result, err := delegateTool.Execute(ctx, input)

			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("delegated response"))

			entries, err := os.ReadDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).To(BeEmpty())
		})
	})

	Describe("executeBackgroundTask with store factory", func() {
		It("creates a JSON file when store factory is set for background task", func() {
			tmpDir := GinkgoT().TempDir()
			factory := &testStoreFactory{sessionsDir: tmpDir}

			bgManager := engine.NewBackgroundTaskManager()
			delegateTool := engine.NewDelegateToolWithBackground(
				engines, delegation, "orchestrator", bgManager, nil,
			)
			delegateTool.WithStoreFactory(factory)

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type":     "qa-agent",
					"message":           "Run the tests in background",
					"run_in_background": true,
				},
			}

			result, err := delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring(`"status": "running"`))

			Eventually(func() string {
				tasks := bgManager.List()
				if len(tasks) == 0 {
					return ""
				}
				return tasks[0].Status.Load()
			}, "3s", "50ms").Should(Equal("completed"))

			entries, err := os.ReadDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			Expect(entries).NotTo(BeEmpty())

			var jsonFiles []string
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".json" {
					jsonFiles = append(jsonFiles, e.Name())
				}
			}
			Expect(jsonFiles).To(HaveLen(1))
		})
	})

	Describe("engine store lifecycle", func() {
		It("closes the previous store before setting a new one", func() {
			tmpDir := GinkgoT().TempDir()
			factory := &testStoreFactory{sessionsDir: tmpDir}

			delegateTool := engine.NewDelegateTool(engines, delegation, "orchestrator")
			delegateTool.WithStoreFactory(factory)

			ctx := context.Background()
			input := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "qa-agent",
					"message":       "first delegation",
				},
			}

			_, err := delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			qaProvider.streamChunks = []provider.StreamChunk{
				{Content: "second response", Done: true},
			}

			_, err = delegateTool.Execute(ctx, input)
			Expect(err).NotTo(HaveOccurred())

			entries, err := os.ReadDir(tmpDir)
			Expect(err).NotTo(HaveOccurred())
			var jsonFiles []string
			for _, e := range entries {
				if filepath.Ext(e.Name()) == ".json" {
					jsonFiles = append(jsonFiles, e.Name())
				}
			}
			Expect(jsonFiles).ToNot(BeEmpty())
		})
	})
})
