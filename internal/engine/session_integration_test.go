package engine_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/tool"
)

type cachedProvider struct {
	inner     provider.Provider
	cache     []provider.StreamChunk
	mu        sync.Mutex
	callCount int64
}

func newCachedProvider(inner provider.Provider) *cachedProvider {
	return &cachedProvider{inner: inner}
}

func (c *cachedProvider) Name() string { return c.inner.Name() }

func (c *cachedProvider) Stream(ctx context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	c.mu.Lock()
	cached := c.cache
	c.mu.Unlock()

	if cached != nil {
		return c.replayFromCache(cached), nil
	}

	innerCh, err := c.inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	return c.recordAndForward(innerCh), nil
}

func (c *cachedProvider) recordAndForward(innerCh <-chan provider.StreamChunk) <-chan provider.StreamChunk {
	atomic.AddInt64(&c.callCount, 1)
	out := make(chan provider.StreamChunk, 16)
	go func() {
		defer close(out)
		var recorded []provider.StreamChunk
		for chunk := range innerCh {
			recorded = append(recorded, chunk)
			out <- chunk
		}
		c.mu.Lock()
		c.cache = recorded
		c.mu.Unlock()
	}()
	return out
}

func (c *cachedProvider) replayFromCache(cached []provider.StreamChunk) <-chan provider.StreamChunk {
	ch := make(chan provider.StreamChunk, len(cached))
	for i := range cached {
		ch <- cached[i]
	}
	close(ch)
	return ch
}

func (c *cachedProvider) CallCount() int64 {
	return atomic.LoadInt64(&c.callCount)
}

func (c *cachedProvider) Chat(ctx context.Context, req provider.ChatRequest) (provider.ChatResponse, error) {
	return c.inner.Chat(ctx, req)
}

func (c *cachedProvider) Embed(ctx context.Context, req provider.EmbedRequest) ([]float64, error) {
	return c.inner.Embed(ctx, req)
}

func (c *cachedProvider) Models() ([]provider.Model, error) {
	return c.inner.Models()
}

func drainStreamContent(ch <-chan provider.StreamChunk) (string, []provider.StreamChunk) {
	var content string
	var chunks []provider.StreamChunk
	for chunk := range ch {
		chunks = append(chunks, chunk)
		content += chunk.Content
	}
	return content, chunks
}

func newSessionTestManifest() agent.Manifest {
	return agent.Manifest{
		ID:   "test-agent",
		Name: "Test Agent",
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful assistant.",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}
}

func realisticChunks() []provider.StreamChunk {
	return []provider.StreamChunk{
		{Content: "Hello"},
		{Content: "! How can I help you today?"},
		{Content: "", Done: true},
	}
}

var _ = Describe("Session Integration", Label("integration"), func() {
	Describe("session lifecycle with engine", func() {
		var (
			innerProvider *workingStreamProvider
			cached        *cachedProvider
			eng           *engine.Engine
			mgr           *session.Manager
		)

		BeforeEach(func() {
			innerProvider = &workingStreamProvider{
				name:   "test-provider",
				chunks: realisticChunks(),
			}
			cached = newCachedProvider(innerProvider)
			eng = engine.New(engine.Config{
				ChatProvider: cached,
				Manifest:     newSessionTestManifest(),
			})
			mgr = session.NewManager(eng)
		})

		Context("when creating a session and sending a message", func() {
			It("returns response content from the provider", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())

				content, chunks := drainStreamContent(ch)

				Expect(content).To(ContainSubstring("Hello"))
				Expect(content).To(ContainSubstring("How can I help you today?"))

				hasError := false
				for _, chunk := range chunks {
					if chunk.Error != nil {
						hasError = true
					}
				}
				Expect(hasError).To(BeFalse())
			})

			It("records the user message in session history", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello there")
				Expect(err).NotTo(HaveOccurred())

				for v := range ch {
					_ = v
				}

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Messages).NotTo(BeEmpty())
				Expect(sess.Messages[0].Role).To(Equal("user"))
				Expect(sess.Messages[0].Content).To(Equal("hello there"))
			})

			It("uses cached response on second call", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch1, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())
				content1, _ := drainStreamContent(ch1)

				ch2, err := mgr.SendMessage(ctx, sess.ID, "hello again")
				Expect(err).NotTo(HaveOccurred())
				content2, _ := drainStreamContent(ch2)

				Expect(content1).To(ContainSubstring("Hello"))
				Expect(content2).To(ContainSubstring("Hello"))
				Expect(cached.CallCount()).To(Equal(int64(1)))
			})
		})

		Context("when closing a session", func() {
			It("marks the session as completed", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				err = mgr.CloseSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Status).To(Equal("completed"))
			})
		})
	})

	Describe("session resumption", func() {
		var (
			seqProvider *streamSequenceProvider
			eng         *engine.Engine
			mgr         *session.Manager
		)

		BeforeEach(func() {
			seqProvider = &streamSequenceProvider{
				name: "seq-provider",
				sequences: [][]provider.StreamChunk{
					{
						{Content: "Hello! How can I help?"},
						{Content: "", Done: true},
					},
					{
						{Content: "2+2 equals 4."},
						{Content: "", Done: true},
					},
				},
			}
			eng = engine.New(engine.Config{
				ChatProvider: seqProvider,
				Manifest:     newSessionTestManifest(),
			})
			mgr = session.NewManager(eng)
		})

		Context("when sending a second message in the same session", func() {
			It("preserves conversation history", func() {
				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()

				ch1, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range ch1 {
					_ = v
				}

				ch2, err := mgr.SendMessage(ctx, sess.ID, "what is 2+2?")
				Expect(err).NotTo(HaveOccurred())
				for v := range ch2 {
					_ = v
				}

				sess, err = mgr.GetSession(sess.ID)
				Expect(err).NotTo(HaveOccurred())
				Expect(sess.Messages).To(HaveLen(4))
				Expect(sess.Messages[0].Content).To(Equal("hello"))
				Expect(sess.Messages[0].Role).To(Equal("user"))
				Expect(sess.Messages[1].Role).To(Equal("assistant"))
				Expect(sess.Messages[2].Content).To(Equal("what is 2+2?"))
				Expect(sess.Messages[2].Role).To(Equal("user"))
				Expect(sess.Messages[3].Role).To(Equal("assistant"))
			})
		})
	})

	Describe("error isolation", func() {
		Context("when background_output is called with invalid task ID", func() {
			It("returns a tool error without crashing", func() {
				taskManager := engine.NewBackgroundTaskManager()
				outputTool := engine.NewBackgroundOutputTool(taskManager)

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": "nonexistent-task-id",
					},
				}

				_, err := outputTool.Execute(context.Background(), input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("task not found"))
			})
		})

		Context("when background_output is called with empty task ID", func() {
			It("returns a validation error", func() {
				taskManager := engine.NewBackgroundTaskManager()
				outputTool := engine.NewBackgroundOutputTool(taskManager)

				input := tool.Input{
					Name:      "background_output",
					Arguments: map[string]interface{}{},
				}

				_, err := outputTool.Execute(context.Background(), input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("task_id is required"))
			})
		})

		Context("when background_cancel is called with all=true and no tasks exist", func() {
			It("returns empty cancelled list without error", func() {
				taskManager := engine.NewBackgroundTaskManager()
				cancelTool := engine.NewBackgroundCancelTool(taskManager)

				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"all": true,
					},
				}

				result, err := cancelTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring(`"cancelled"`))
				Expect(result.Output).To(ContainSubstring("[]"))
			})
		})

		Context("when background_cancel is called with no arguments", func() {
			It("returns a validation error", func() {
				taskManager := engine.NewBackgroundTaskManager()
				cancelTool := engine.NewBackgroundCancelTool(taskManager)

				input := tool.Input{
					Name:      "background_cancel",
					Arguments: map[string]interface{}{},
				}

				_, err := cancelTool.Execute(context.Background(), input)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("must provide either task_id or all=true"))
			})
		})

		Context("when evictCompletedBackgroundTasks runs with no delegate tool", func() {
			It("completes streaming without error", func() {
				simpleProvider := &workingStreamProvider{
					name: "simple-provider",
					chunks: []provider.StreamChunk{
						{Content: "response without tools"},
						{Content: "", Done: true},
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: simpleProvider,
					Manifest:     newSessionTestManifest(),
				})

				Expect(eng.HasTool("delegate")).To(BeFalse())

				ctx := context.Background()
				ch, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				content, _ := drainStreamContent(ch)
				Expect(content).To(ContainSubstring("response without tools"))
			})
		})
	})

	Describe("stream error handling", func() {
		Context("when provider sends an error chunk", func() {
			It("the error chunk has Done=true and channel closes", func() {
				errProvider := &asyncFailProvider{
					name: "error-provider",
				}

				eng := engine.New(engine.Config{
					ChatProvider: errProvider,
					Manifest:     newSessionTestManifest(),
				})

				ctx := context.Background()
				ch, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).NotTo(HaveOccurred())

				var lastChunk provider.StreamChunk
				var chunkCount int
				for chunk := range ch {
					lastChunk = chunk
					chunkCount++
				}

				Expect(lastChunk.Error).To(HaveOccurred())
				Expect(lastChunk.Done).To(BeTrue())
				Expect(chunkCount).To(BeNumerically(">=", 1))
			})
		})

		Context("when provider returns error on Stream() call", func() {
			It("returns the error without a channel", func() {
				failProvider := &syncFailStreamProvider{
					name: "sync-fail-provider",
				}

				eng := engine.New(engine.Config{
					ChatProvider: failProvider,
					Manifest:     newSessionTestManifest(),
				})

				ctx := context.Background()
				ch, err := eng.Stream(ctx, "test-agent", "hello")
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("connection refused"))
				Expect(ch).To(BeNil())
			})
		})

		Context("when provider returns error through session manager", func() {
			It("propagates the sync error from SendMessage", func() {
				failProvider := &syncFailStreamProvider{
					name: "sync-fail-provider",
				}

				eng := engine.New(engine.Config{
					ChatProvider: failProvider,
					Manifest:     newSessionTestManifest(),
				})
				mgr := session.NewManager(eng)

				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).To(HaveOccurred())
				Expect(ch).To(BeNil())
			})
		})
	})

	Describe("background task lifecycle", func() {
		Context("when a task is launched and queried", func() {
			It("reports the task result after completion", func() {
				taskManager := engine.NewBackgroundTaskManager()
				outputTool := engine.NewBackgroundOutputTool(taskManager)

				taskManager.Launch(
					context.Background(),
					"task-1",
					"test-agent",
					"test task",
					func(_ context.Context) (string, error) {
						return "task completed successfully", nil
					},
				)

				Eventually(func() string {
					task, found := taskManager.Get("task-1")
					if !found {
						return ""
					}
					return task.Status.Load()
				}).Should(Equal("completed"))

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": "task-1",
					},
				}

				result, err := outputTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("completed"))
				Expect(result.Output).To(ContainSubstring("task completed successfully"))
			})
		})

		Context("when a task fails", func() {
			It("reports the failed status", func() {
				taskManager := engine.NewBackgroundTaskManager()
				outputTool := engine.NewBackgroundOutputTool(taskManager)

				taskManager.Launch(
					context.Background(),
					"task-fail",
					"test-agent",
					"failing task",
					func(_ context.Context) (string, error) {
						return "", errors.New("task execution failed")
					},
				)

				Eventually(func() string {
					task, found := taskManager.Get("task-fail")
					if !found {
						return ""
					}
					return task.Status.Load()
				}).Should(Equal("failed"))

				input := tool.Input{
					Name: "background_output",
					Arguments: map[string]interface{}{
						"task_id": "task-fail",
					},
				}

				result, err := outputTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("failed"))
				Expect(result.Output).To(ContainSubstring("task execution failed"))
			})
		})

		Context("when a running task is cancelled", func() {
			It("transitions to cancelled status", func() {
				taskManager := engine.NewBackgroundTaskManager()
				cancelTool := engine.NewBackgroundCancelTool(taskManager)

				started := make(chan struct{})
				taskManager.Launch(
					context.Background(),
					"task-cancel",
					"test-agent",
					"long running task",
					func(ctx context.Context) (string, error) {
						close(started)
						<-ctx.Done()
						return "", ctx.Err()
					},
				)

				Eventually(func() string {
					task, found := taskManager.Get("task-cancel")
					if !found {
						return ""
					}
					return task.Status.Load()
				}).Should(Equal("running"))

				<-started

				input := tool.Input{
					Name: "background_cancel",
					Arguments: map[string]interface{}{
						"task_id": "task-cancel",
					},
				}

				result, err := cancelTool.Execute(context.Background(), input)
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Output).To(ContainSubstring("task-cancel"))

				Eventually(func() string {
					task, found := taskManager.Get("task-cancel")
					if !found {
						return ""
					}
					return task.Status.Load()
				}).Should(Equal("cancelled"))
			})
		})
	})

	Describe("session context propagation", func() {
		Context("when SendMessage is called through the session manager", func() {
			It("injects session ID into the context", func() {
				var capturedSessionID string
				var capturedOk bool

				ctxCapturingProvider := &contextCapturingProvider{
					name: "ctx-provider",
					chunks: []provider.StreamChunk{
						{Content: "ok", Done: true},
					},
					captureFn: func(ctx context.Context) {
						val, ok := ctx.Value(session.IDKey{}).(string)
						capturedSessionID = val
						capturedOk = ok
					},
				}

				eng := engine.New(engine.Config{
					ChatProvider: ctxCapturingProvider,
					Manifest:     newSessionTestManifest(),
				})
				mgr := session.NewManager(eng)

				sess, err := mgr.CreateSession("test-agent")
				Expect(err).NotTo(HaveOccurred())

				ctx := context.Background()
				ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
				Expect(err).NotTo(HaveOccurred())
				for v := range ch {
					_ = v
				}

				Expect(capturedOk).To(BeTrue())
				Expect(capturedSessionID).To(Equal(sess.ID))
			})
		})
	})
})

var _ = Describe("DelegateTool.WithSessionCreator", func() {
	var (
		targetProvider *mockProvider
		targetManifest agent.Manifest
		targetEngine   *engine.Engine
	)

	BeforeEach(func() {
		targetProvider = &mockProvider{
			name: "target-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Target response", Done: true},
			},
		}
		targetManifest = agent.Manifest{
			ID:                "target-agent",
			Name:              "Target Agent",
			Instructions:      agent.Instructions{SystemPrompt: "You are the target."},
			ContextManagement: agent.DefaultContextManagement(),
		}
		targetEngine = engine.New(engine.Config{
			ChatProvider: targetProvider,
			Manifest:     targetManifest,
		})
	})

	It("creates a child session via sessionCreator when parent is registered", func() {
		mgr := session.NewManager(targetEngine)
		mgr.RegisterSession("parent-session", "orchestrator-agent")

		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"target-agent": targetEngine},
			agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
			"orchestrator-agent",
		)
		delegateTool.WithSessionCreator(mgr)

		ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-session")
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "target-agent",
				"message":       "Hello target",
			},
		}
		_, err := delegateTool.Execute(ctx, input)
		Expect(err).NotTo(HaveOccurred())

		children, err := mgr.ChildSessions("parent-session")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).NotTo(BeEmpty())
		Expect(children[0].AgentID).To(Equal("target-agent"))
	})

	It("accepts nil sessionCreator without panicking", func() {
		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"target-agent": targetEngine},
			agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
			"orchestrator-agent",
		)

		ctx := context.Background()
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "target-agent",
				"message":       "Hello target",
			},
		}
		_, err := delegateTool.Execute(ctx, input)
		Expect(err).NotTo(HaveOccurred())
	})
})

type contextCapturingProvider struct {
	name      string
	chunks    []provider.StreamChunk
	captureFn func(ctx context.Context)
}

func (p *contextCapturingProvider) Name() string { return p.name }

func (p *contextCapturingProvider) Stream(ctx context.Context, _ provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	if p.captureFn != nil {
		p.captureFn(ctx)
	}
	ch := make(chan provider.StreamChunk, len(p.chunks))
	for i := range p.chunks {
		ch <- p.chunks[i]
	}
	close(ch)
	return ch, nil
}

func (p *contextCapturingProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *contextCapturingProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return nil, nil
}

func (p *contextCapturingProvider) Models() ([]provider.Model, error) {
	return nil, nil
}
