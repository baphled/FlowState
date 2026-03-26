package app_test

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/app"
	"github.com/baphled/flowstate/internal/config"
	"github.com/baphled/flowstate/internal/learning"
	"github.com/baphled/flowstate/internal/streaming"
)

var _ = Describe("Harness wiring", func() {
	var tempDir string

	BeforeEach(func() {
		var err error
		tempDir, err = os.MkdirTemp("", "harness-wiring-test")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		os.RemoveAll(tempDir)
	})

	Describe("App.Streamer", func() {
		Context("after New() with valid configuration", func() {
			It("is non-nil", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-harness")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.Streamer).NotTo(BeNil())
			})

			It("is a HarnessStreamer wrapping the engine", func() {
				os.Setenv("OPENAI_API_KEY", "test-key-interface")
				DeferCleanup(func() { os.Unsetenv("OPENAI_API_KEY") })

				agentsDir := filepath.Join(tempDir, "agents")
				skillsDir := filepath.Join(tempDir, "skills")
				Expect(os.MkdirAll(agentsDir, 0o755)).To(Succeed())
				Expect(os.MkdirAll(skillsDir, 0o755)).To(Succeed())

				cfg := config.DefaultConfig()
				cfg.Providers.Default = "openai"
				cfg.DataDir = tempDir
				cfg.AgentDir = agentsDir
				cfg.SkillDir = skillsDir

				application, err := app.New(cfg)

				Expect(err).NotTo(HaveOccurred())
				Expect(application.Streamer).To(BeAssignableToTypeOf(&streaming.HarnessStreamer{}))
			})
		})
	})

	Describe("buildHookChain", func() {
		var learningStore *learning.JSONFileStore

		BeforeEach(func() {
			learningsPath := filepath.Join(tempDir, "learnings.json")
			learningStore = learning.NewJSONFileStore(learningsPath)
		})

		Context("when HarnessEnabled is false", func() {
			It("returns a chain without harness hooks", func() {
				manifestGetter := func() agent.Manifest {
					return agent.Manifest{HarnessEnabled: false}
				}

				chain := app.BuildHookChainForTest(learningStore, manifestGetter)

				Expect(chain).NotTo(BeNil())
				Expect(chain.Len()).To(Equal(4))
			})
		})

		Context("when HarnessEnabled is true", func() {
			It("includes PhaseDetectorHook before ContextInjectionHook", func() {
				manifestGetter := func() agent.Manifest {
					return agent.Manifest{HarnessEnabled: true}
				}

				chain := app.BuildHookChainForTest(learningStore, manifestGetter)

				Expect(chain).NotTo(BeNil())
				Expect(chain.Len()).To(Equal(6))
			})
		})
	})

	Describe("createHarnessStreamer with critic configuration", func() {
		Context("when CriticEnabled is false (default)", func() {
			It("accepts HarnessConfig and provider without error", func() {
				cfg := config.HarnessConfig{
					Enabled:       true,
					CriticEnabled: false,
					ProjectRoot:   tempDir,
				}

				registry := agent.NewRegistry()
				streamer := app.CreateHarnessStreamerForTest(nil, registry, cfg, nil)

				Expect(streamer).NotTo(BeNil())
				Expect(streamer).To(BeAssignableToTypeOf(&streaming.HarnessStreamer{}))
			})
		})

		Context("when CriticEnabled is true with valid provider", func() {
			It("accepts HarnessConfig and provider and creates streamer", func() {
				cfg := config.HarnessConfig{
					Enabled:       true,
					CriticEnabled: true,
					ProjectRoot:   tempDir,
				}

				registry := agent.NewRegistry()
				streamer := app.CreateHarnessStreamerForTest(nil, registry, cfg, nil)

				Expect(streamer).NotTo(BeNil())
				Expect(streamer).To(BeAssignableToTypeOf(&streaming.HarnessStreamer{}))
			})
		})
	})

	Describe("createHarnessStreamer with voting configuration", func() {
		Context("when VotingEnabled is false (default)", func() {
			It("creates streamer without voter", func() {
				cfg := config.HarnessConfig{
					Enabled:       true,
					VotingEnabled: false,
					ProjectRoot:   tempDir,
				}

				registry := agent.NewRegistry()
				streamer := app.CreateHarnessStreamerForTest(nil, registry, cfg, nil)

				Expect(streamer).NotTo(BeNil())
			})
		})

		Context("when VotingEnabled is true", func() {
			It("creates streamer with voter configured", func() {
				cfg := config.HarnessConfig{
					Enabled:       true,
					VotingEnabled: true,
					ProjectRoot:   tempDir,
				}

				registry := agent.NewRegistry()
				streamer := app.CreateHarnessStreamerForTest(nil, registry, cfg, nil)

				Expect(streamer).NotTo(BeNil())
			})
		})
	})

	Describe("createHarnessStreamer with incremental configuration", func() {
		Context("when IncrementalEnabled is false (default)", func() {
			It("creates streamer without incremental generator", func() {
				cfg := config.HarnessConfig{
					Enabled:            true,
					IncrementalEnabled: false,
					ProjectRoot:        tempDir,
				}

				registry := agent.NewRegistry()
				streamer := app.CreateHarnessStreamerForTest(nil, registry, cfg, nil)

				Expect(streamer).NotTo(BeNil())
			})
		})

		Context("when IncrementalEnabled is true", func() {
			It("creates streamer with incremental generator configured", func() {
				cfg := config.HarnessConfig{
					Enabled:            true,
					IncrementalEnabled: true,
					ProjectRoot:        tempDir,
				}

				registry := agent.NewRegistry()
				streamer := app.CreateHarnessStreamerForTest(nil, registry, cfg, nil)

				Expect(streamer).NotTo(BeNil())
			})
		})
	})
})
