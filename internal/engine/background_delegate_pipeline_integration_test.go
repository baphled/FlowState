package engine_test

import (
	"context"
	"encoding/json"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

var _ = Describe("DelegateTool Async Background Pipeline", Label("integration"), func() {
	var (
		targetProvider *asyncDelegatePipelineProvider
		targetEngine   *engine.Engine
		backgroundMgr  *engine.BackgroundTaskManager
		delegateTool   *engine.DelegateTool
		notifCh        chan streaming.CompletionNotificationEvent
		mgr            *session.Manager
	)

	BeforeEach(func() {
		targetProvider = &asyncDelegatePipelineProvider{
			name: "target-provider",
			chunks: []provider.StreamChunk{
				{Content: "async delegation result", Done: false},
				{Content: "", Done: true},
			},
		}

		targetEngine = engine.New(engine.Config{
			ChatProvider: targetProvider,
			Manifest: agent.Manifest{
				ID:                "target-agent",
				Name:              "Target Agent",
				Instructions:      agent.Instructions{SystemPrompt: "You are the target agent."},
				ContextManagement: agent.DefaultContextManagement(),
			},
		})

		backgroundMgr = engine.NewBackgroundTaskManager()

		notifCh = make(chan streaming.CompletionNotificationEvent, 64)
		backgroundMgr.SetCompletionSubscriber(notifCh)

		delegateTool = engine.NewDelegateToolWithBackground(
			map[string]*engine.Engine{"target-agent": targetEngine},
			agent.Delegation{
				CanDelegate:         true,
				DelegationAllowlist: []string{"target-agent"},
			},
			"orchestrator",
			backgroundMgr,
			nil,
		)

		streamerForMgr := &fakeStreamer{}
		mgr = session.NewManager(streamerForMgr)
		backgroundMgr.WithSessionManager(mgr)
		mgr.RegisterSession("delegate-pipeline-sess", "orchestrator")
	})

	Describe("executeAsync: DelegateTool launches background task", func() {
		Context("when run_in_background is true and manager is configured", func() {
			It("returns a task ID immediately without blocking", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "delegate-pipeline-sess")

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "target-agent",
						"message":           "investigate the codebase",
						"run_in_background": true,
					},
				}

				result, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("task_id"))
				Expect(result.Output).To(ContainSubstring("running"))
			})

			It("increments ActiveCount after launch", func() {
				slow := &slowDelegateProvider{name: "slow-target"}
				slowEngine := engine.New(engine.Config{
					ChatProvider: slow,
					Manifest: agent.Manifest{
						ID:                "slow-agent",
						Name:              "Slow Agent",
						Instructions:      agent.Instructions{SystemPrompt: "You are slow."},
						ContextManagement: agent.DefaultContextManagement(),
					},
				})

				slowBgMgr := engine.NewBackgroundTaskManager()
				slowDelegateTool := engine.NewDelegateToolWithBackground(
					map[string]*engine.Engine{"slow-agent": slowEngine},
					agent.Delegation{
						CanDelegate:         true,
						DelegationAllowlist: []string{"slow-agent"},
					},
					"orchestrator",
					slowBgMgr,
					nil,
				)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "slow-agent",
						"message":           "long running task",
						"run_in_background": true,
					},
				}

				_, err := slowDelegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() int {
					return slowBgMgr.ActiveCount()
				}, 2*time.Second, 10*time.Millisecond).Should(BeNumerically(">", 0))

				slow.release()
			})

			Context("when parent context is cancelled after launch", func() {
				It("background task completes and is NOT cancelled", func() {
					ctx, cancel := context.WithCancel(context.WithValue(context.Background(), session.IDKey{}, "delegate-pipeline-sess"))
					DeferCleanup(cancel)

					input := tool.Input{
						Name: "delegate",
						Arguments: map[string]interface{}{
							"subagent_type":     "target-agent",
							"message":           "cancel after launch",
							"run_in_background": true,
						},
					}

					result, err := delegateTool.Execute(ctx, input)
					Expect(err).NotTo(HaveOccurred())

					var launched struct {
						TaskID string `json:"task_id"`
					}
					Expect(json.Unmarshal([]byte(result.Output), &launched)).To(Succeed())
					Expect(launched.TaskID).NotTo(BeEmpty())

					cancel()

					Eventually(func() string {
						task, found := backgroundMgr.Get(launched.TaskID)
						if !found {
							return ""
						}
						return task.Status.Load()
					}, 2*time.Second, 10*time.Millisecond).Should(Equal("completed"))
				})
			})
		})

		Context("when run_in_background is true and background manager is nil", func() {
			It("returns an error indicating background mode is disabled", func() {
				noBackgroundTool := engine.NewDelegateTool(
					map[string]*engine.Engine{"target-agent": targetEngine},
					agent.Delegation{
						CanDelegate:         true,
						DelegationAllowlist: []string{"target-agent"},
					},
					"orchestrator",
				)

				ctx := context.Background()
				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "target-agent",
						"message":           "should fail",
						"run_in_background": true,
					},
				}

				_, err := noBackgroundTool.Execute(ctx, input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("background mode disabled"))
			})
		})
	})

	Describe("async pipeline: launch → complete → notification delivered", func() {
		Context("when an async delegation task completes successfully", func() {
			It("delivers a completion notification on the subscriber channel", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "delegate-pipeline-sess")

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "target-agent",
						"message":           "investigate the pipeline",
						"run_in_background": true,
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				var notif streaming.CompletionNotificationEvent
				Eventually(notifCh, 5*time.Second, 10*time.Millisecond).Should(Receive(&notif))

				Expect(notif.Agent).To(Equal("target-agent"))
				Expect(notif.Status).To(Equal("completed"))
				Expect(notif.Duration).To(BeNumerically(">=", 0))
			})
		})

		Context("when an async delegation task completes and the session manager is wired", func() {
			It("stores the notification in the parent session for retrieval", func() {
				ctx := context.WithValue(context.Background(), session.IDKey{}, "delegate-pipeline-sess")

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type":     "target-agent",
						"message":           "store notification test",
						"run_in_background": true,
					},
				}

				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				Eventually(func() string {
					tasks := backgroundMgr.List()
					if len(tasks) == 0 {
						return ""
					}
					return tasks[0].Status.Load()
				}, 5*time.Second, 10*time.Millisecond).Should(Equal("completed"))

				notifications, err := mgr.GetNotifications("delegate-pipeline-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).NotTo(BeEmpty())
				Expect(notifications[0].Status).To(Equal("completed"))
				Expect(notifications[0].Agent).To(Equal("target-agent"))
			})
		})
	})

	Describe("EvictCompleted clears terminal tasks", func() {
		Context("when tasks are completed and EvictCompleted is called", func() {
			It("removes completed tasks from the list but leaves pending/running tasks", func() {
				manager := engine.NewBackgroundTaskManager()

				manager.Launch(
					context.Background(),
					"evict-done-1",
					"test-agent",
					"completed task",
					func(_ context.Context) (string, error) {
						return "done", nil
					},
				)
				manager.Launch(
					context.Background(),
					"evict-done-2",
					"test-agent",
					"another completed task",
					func(_ context.Context) (string, error) {
						return "done", nil
					},
				)

				Eventually(func() string {
					t, found := manager.Get("evict-done-1")
					if !found {
						return ""
					}
					return t.Status.Load()
				}, 2*time.Second, 10*time.Millisecond).Should(Equal("completed"))

				Eventually(func() string {
					t, found := manager.Get("evict-done-2")
					if !found {
						return ""
					}
					return t.Status.Load()
				}, 2*time.Second, 10*time.Millisecond).Should(Equal("completed"))

				manager.EvictCompleted()

				Expect(manager.List()).To(BeEmpty())
				Expect(manager.ActiveCount()).To(Equal(0))
			})
		})

		Context("when a mix of terminal and active tasks exist", func() {
			It("only removes terminal tasks and preserves active ones", func() {
				manager := engine.NewBackgroundTaskManager()

				release := make(chan struct{})
				manager.Launch(
					context.Background(),
					"evict-active",
					"test-agent",
					"still running",
					func(ctx context.Context) (string, error) {
						<-release
						return "done", nil
					},
				)

				manager.Launch(
					context.Background(),
					"evict-terminal",
					"test-agent",
					"finishes quickly",
					func(_ context.Context) (string, error) {
						return "done fast", nil
					},
				)

				DeferCleanup(func() {
					select {
					case <-release:
					default:
						close(release)
					}
				})

				Eventually(func() string {
					t, found := manager.Get("evict-terminal")
					if !found {
						return ""
					}
					return t.Status.Load()
				}, 2*time.Second, 10*time.Millisecond).Should(Equal("completed"))

				Eventually(func() string {
					t, found := manager.Get("evict-active")
					if !found {
						return ""
					}
					return t.Status.Load()
				}, 2*time.Second, 10*time.Millisecond).Should(Equal("running"))

				manager.EvictCompleted()

				_, foundTerminal := manager.Get("evict-terminal")
				Expect(foundTerminal).To(BeFalse())

				_, foundActive := manager.Get("evict-active")
				Expect(foundActive).To(BeTrue())

				close(release)
			})
		})
	})

	Describe("BackgroundTaskManager.Get returns a snapshot copy", func() {
		Context("when a task completes after Get is called", func() {
			It("the snapshot reflects state at time of call, not later mutations", func() {
				manager := engine.NewBackgroundTaskManager()

				started := make(chan struct{})
				release := make(chan struct{})

				manager.Launch(
					context.Background(),
					"snapshot-task",
					"test-agent",
					"snapshot test",
					func(ctx context.Context) (string, error) {
						close(started)
						<-release
						return "snapshot result", nil
					},
				)

				<-started

				Eventually(func() string {
					t, found := manager.Get("snapshot-task")
					if !found {
						return ""
					}
					return t.Status.Load()
				}, 2*time.Second, 10*time.Millisecond).Should(Equal("running"))

				snapshot, found := manager.Get("snapshot-task")
				Expect(found).To(BeTrue())
				Expect(snapshot.Status.Load()).To(Equal("running"))

				close(release)

				Eventually(func() string {
					t, found := manager.Get("snapshot-task")
					if !found {
						return ""
					}
					return t.Status.Load()
				}, 2*time.Second, 10*time.Millisecond).Should(Equal("completed"))

				Expect(snapshot.Status.Load()).To(Equal("running"))
			})
		})
	})
})

type asyncDelegatePipelineProvider struct {
	name   string
	chunks []provider.StreamChunk
}

func (p *asyncDelegatePipelineProvider) Name() string { return p.name }

func (p *asyncDelegatePipelineProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	ch := make(chan provider.StreamChunk, len(p.chunks))
	for i := range p.chunks {
		ch <- p.chunks[i]
	}
	close(ch)
	return ch, nil
}

func (p *asyncDelegatePipelineProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *asyncDelegatePipelineProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *asyncDelegatePipelineProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

type slowDelegateProvider struct {
	name      string
	releaseCh chan struct{}
}

func (p *slowDelegateProvider) Name() string { return p.name }

func (p *slowDelegateProvider) Stream(_ context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.releaseCh == nil {
		p.releaseCh = make(chan struct{})
	}
	ch := make(chan provider.StreamChunk, 2)
	go func() {
		<-p.releaseCh
		ch <- provider.StreamChunk{Content: "slow result", Done: false}
		ch <- provider.StreamChunk{Done: true}
		close(ch)
	}()
	return ch, nil
}

func (p *slowDelegateProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *slowDelegateProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *slowDelegateProvider) Models() ([]provider.Model, error) {
	return nil, nil
}

func (p *slowDelegateProvider) release() {
	if p.releaseCh != nil {
		select {
		case <-p.releaseCh:
		default:
			close(p.releaseCh)
		}
	}
}
