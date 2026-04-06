package app

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
)

var _ = Describe("Shared BackgroundTaskManager", Label("integration"), func() {
	var (
		application *App
		providerReg *provider.Registry
	)

	BeforeEach(func() {
		providerReg = provider.NewRegistry()
		providerReg.Register(&mockProvider{name: "spy"})

		application = &App{
			Registry:         agent.NewRegistry(),
			providerRegistry: providerReg,
		}
	})

	Context("when wireDelegateToolIfEnabled is called multiple times", func() {
		var (
			firstManifest  agent.Manifest
			secondManifest agent.Manifest
			workerManifest agent.Manifest
			firstEngine    *engine.Engine
			secondEngine   *engine.Engine
			firstManager   *engine.BackgroundTaskManager
		)

		BeforeEach(func() {
			firstManifest = agent.Manifest{
				ID:   "coordinator-a",
				Name: "Coordinator A",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}

			secondManifest = agent.Manifest{
				ID:   "coordinator-b",
				Name: "Coordinator B",
				Delegation: agent.Delegation{
					CanDelegate: true,
				},
			}

			workerManifest = agent.Manifest{
				ID:   "worker",
				Name: "Worker Agent",
			}

			application.Registry.Register(&firstManifest)
			application.Registry.Register(&secondManifest)
			application.Registry.Register(&workerManifest)

			firstEngine = engine.New(engine.Config{
				Manifest:      firstManifest,
				AgentRegistry: application.Registry,
				Registry:      providerReg,
				ChatProvider:  &mockProvider{name: "spy"},
			})

			secondEngine = engine.New(engine.Config{
				Manifest:      secondManifest,
				AgentRegistry: application.Registry,
				Registry:      providerReg,
				ChatProvider:  &mockProvider{name: "spy"},
			})

			application.wireDelegateToolIfEnabled(firstEngine, firstManifest)
			firstManager = application.backgroundManager

			application.wireDelegateToolIfEnabled(secondEngine, secondManifest)
		})

		It("reuses the same manager instance across all wirings", func() {
			Expect(application.backgroundManager).To(BeIdenticalTo(firstManager),
				"the second wiring must reuse the manager from the first wiring, not create a new one")
		})

		It("makes tasks launched in the first wiring visible from the app-level manager", func() {
			firstManager.Launch(context.Background(), "task-from-first", "coordinator-a", "first delegation", func(_ context.Context) (string, error) {
				return "result-a", nil
			})

			Eventually(func() string {
				task, ok := application.backgroundManager.Get("task-from-first")
				if !ok {
					return ""
				}
				return task.Status.Load()
			}).Should(Equal("completed"))

			task, ok := application.backgroundManager.Get("task-from-first")
			Expect(ok).To(BeTrue(), "task launched via first wiring's manager must be found via app.backgroundManager")
			Expect(task.Result).To(Equal("result-a"))
		})

		It("makes tasks from both wirings visible through a single manager", func() {
			firstManager.Launch(context.Background(), "cross-1", "coordinator-a", "first", func(_ context.Context) (string, error) {
				return "result-1", nil
			})

			application.backgroundManager.Launch(context.Background(), "cross-2", "coordinator-b", "second", func(_ context.Context) (string, error) {
				return "result-2", nil
			})

			Eventually(func() bool {
				t1, ok1 := application.backgroundManager.Get("cross-1")
				t2, ok2 := application.backgroundManager.Get("cross-2")
				return ok1 && ok2 &&
					t1.Status.Load() == "completed" &&
					t2.Status.Load() == "completed"
			}).Should(BeTrue())

			t1, ok := application.backgroundManager.Get("cross-1")
			Expect(ok).To(BeTrue())
			Expect(t1.Result).To(Equal("result-1"))

			t2, ok := application.backgroundManager.Get("cross-2")
			Expect(ok).To(BeTrue())
			Expect(t2.Result).To(Equal("result-2"))
		})
	})
})
