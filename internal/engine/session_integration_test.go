package engine_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
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
				// Chat-UI leak triage (May 2026): the raw underlying
				// error is no longer surfaced verbatim — it goes to the
				// server log paired with a correlation_id while the
				// caller sees a sanitised canonical message. See
				// engine.sanitiseTaskError + the Leak C specs in
				// background_output_test.go.
				Expect(result.Output).NotTo(ContainSubstring("task execution failed"),
					"raw error text must not leak into the tool-result payload")
				Expect(result.Output).To(ContainSubstring("correlation_id"),
					"sanitised payload must carry a correlation_id for support lookup")
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

var _ = Describe("DelegateTool.WithMessageAppender", func() {
	var (
		targetProvider *mockProvider
		targetManifest agent.Manifest
		targetEngine   *engine.Engine
	)

	BeforeEach(func() {
		targetProvider = &mockProvider{
			name: "target-provider",
			streamChunks: []provider.StreamChunk{
				{Content: "Delegated reply"},
				{Done: true},
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

	It("accumulates delegation stream chunks into the child session messages", func() {
		mgr := session.NewManager(targetEngine)
		mgr.RegisterSession("parent-sess", "orchestrator-agent")

		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"target-agent": targetEngine},
			agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
			"orchestrator-agent",
		)
		delegateTool.WithSessionCreator(mgr)
		delegateTool.WithMessageAppender(mgr)

		ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess")
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "target-agent",
				"message":       "Do something",
			},
		}
		_, err := delegateTool.Execute(ctx, input)
		Expect(err).NotTo(HaveOccurred())

		children, err := mgr.ChildSessions("parent-sess")
		Expect(err).NotTo(HaveOccurred())
		Expect(children).NotTo(BeEmpty())

		child := children[0]
		Eventually(func() int {
			sess, _ := mgr.GetSession(child.ID)
			if sess == nil {
				return 0
			}
			return len(sess.Messages)
		}).Should(BeNumerically(">", 0))

		sess, err := mgr.GetSession(child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(sess.Messages).NotTo(BeEmpty())
		// Child session history now opens with the parent's brief as a
		// user-role message, followed by the accumulated assistant reply.
		// See Bug Fixes/Delegation Brief Persistence (May 2026).
		Expect(sess.Messages[0].Role).To(Equal("user"))
		Expect(sess.Messages[0].Content).To(Equal("Do something"))
		var assistant *session.Message
		for i := range sess.Messages {
			if sess.Messages[i].Role == "assistant" {
				assistant = &sess.Messages[i]
				break
			}
		}
		Expect(assistant).NotTo(BeNil())
		Expect(assistant.Content).To(Equal("Delegated reply"))
	})

	It("accepts nil messageAppender without panicking", func() {
		mgr := session.NewManager(targetEngine)
		mgr.RegisterSession("parent-sess2", "orchestrator-agent")

		delegateTool := engine.NewDelegateTool(
			map[string]*engine.Engine{"target-agent": targetEngine},
			agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
			"orchestrator-agent",
		)
		delegateTool.WithSessionCreator(mgr)

		ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-sess2")
		input := tool.Input{
			Name: "delegate",
			Arguments: map[string]interface{}{
				"subagent_type": "target-agent",
				"message":       "Do something",
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

var _ = Describe("Session manager wiring", Label("integration"), func() {
	Describe("BackgroundTaskManager.WithSessionManager", func() {
		Context("when session manager is wired and a task completes", func() {
			It("injects a completion notification into the session", func() {
				targetProvider := &mockProvider{
					name:         "wiring-provider",
					streamChunks: []provider.StreamChunk{{Content: "wired", Done: true}},
				}
				eng := engine.New(engine.Config{
					ChatProvider: targetProvider,
					Manifest: agent.Manifest{
						ID:                "wiring-agent",
						Name:              "Wiring Agent",
						Instructions:      agent.Instructions{SystemPrompt: "You help with wiring tests."},
						ContextManagement: agent.DefaultContextManagement(),
					},
				})
				mgr := session.NewManager(eng)
				mgr.RegisterSession("wiring-parent-sess", "coordinator")

				taskManager := engine.NewBackgroundTaskManager()
				taskManager.WithSessionManager(mgr)

				ctx := context.WithValue(context.Background(), session.IDKey{}, "wiring-parent-sess")
				taskManager.Launch(ctx, "wiring-task", "wiring-agent", "wiring test",
					func(_ context.Context) (string, error) {
						return "wired result", nil
					},
				)

				Eventually(func() string {
					task, found := taskManager.Get("wiring-task")
					if !found {
						return ""
					}
					return task.Status.Load()
				}).Should(Equal("completed"))

				notifications, err := mgr.GetNotifications("wiring-parent-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(notifications).To(HaveLen(1))
				Expect(notifications[0].TaskID).To(Equal("wiring-task"))
				Expect(notifications[0].Status).To(Equal("completed"))
			})
		})

		Context("when session manager is nil", func() {
			It("does not crash when task completes with no session manager", func() {
				taskManager := engine.NewBackgroundTaskManager()

				ctx := context.Background()
				taskManager.Launch(ctx, "no-mgr-wiring", "some-agent", "no mgr test",
					func(_ context.Context) (string, error) {
						return "ok", nil
					},
				)

				Eventually(func() string {
					task, found := taskManager.Get("no-mgr-wiring")
					if !found {
						return ""
					}
					return task.Status.Load()
				}).Should(Equal("completed"))
			})
		})
	})

	Describe("DelegateTool.WithSessionManager child session creation", func() {
		Context("when sessionManager is set and parent is registered in context", func() {
			It("creates a child session via CreateWithParent", func() {
				targetProvider := &mockProvider{
					name:         "mgr-wiring-provider",
					streamChunks: []provider.StreamChunk{{Content: "mgr wired", Done: true}},
				}
				targetEng := engine.New(engine.Config{
					ChatProvider: targetProvider,
					Manifest: agent.Manifest{
						ID:                "mgr-wiring-agent",
						Name:              "Manager Wiring Agent",
						Instructions:      agent.Instructions{SystemPrompt: "You help wire the session manager."},
						ContextManagement: agent.DefaultContextManagement(),
					},
				})
				mgr := session.NewManager(targetEng)
				mgr.RegisterSession("mgr-parent-sess", "coordinator")

				delegateTool := engine.NewDelegateTool(
					map[string]*engine.Engine{"mgr-wiring-agent": targetEng},
					agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"mgr-wiring-agent"}},
					"coordinator",
				)
				delegateTool.WithSessionManager(mgr)

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "mgr-wiring-agent",
						"message":       "test wiring",
					},
				}
				ctx := context.WithValue(context.Background(), session.IDKey{}, "mgr-parent-sess")
				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				children, err := mgr.ChildSessions("mgr-parent-sess")
				Expect(err).NotTo(HaveOccurred())
				Expect(children).NotTo(BeEmpty())
				Expect(children[0].AgentID).To(Equal("mgr-wiring-agent"))
				Expect(children[0].ParentID).To(Equal("mgr-parent-sess"))
			})
		})

		Context("when sessionManager is set and no parent ID in context", func() {
			It("registers a synthetic session without panicking", func() {
				targetProvider := &mockProvider{
					name:         "mgr-synth-provider",
					streamChunks: []provider.StreamChunk{{Content: "synth response", Done: true}},
				}
				targetEng := engine.New(engine.Config{
					ChatProvider: targetProvider,
					Manifest: agent.Manifest{
						ID:                "mgr-synth-agent",
						Name:              "Synth Agent",
						Instructions:      agent.Instructions{SystemPrompt: "Synth agent."},
						ContextManagement: agent.DefaultContextManagement(),
					},
				})
				mgr := session.NewManager(targetEng)

				delegateTool := engine.NewDelegateTool(
					map[string]*engine.Engine{"mgr-synth-agent": targetEng},
					agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"mgr-synth-agent"}},
					"coordinator",
				)
				delegateTool.WithSessionManager(mgr)

				input := tool.Input{
					Name: "delegate",
					Arguments: map[string]interface{}{
						"subagent_type": "mgr-synth-agent",
						"message":       "synthetic session test",
					},
				}
				ctx := context.Background()
				_, err := delegateTool.Execute(ctx, input)
				Expect(err).NotTo(HaveOccurred())

				sessions := mgr.ListSessions()
				Expect(sessions).NotTo(BeEmpty())
			})
		})
	})

	// cross-session context isolation: two sessions sharing the same engine
	// must not see each other's conversation history in the messages sent to
	// the model. Reproduces a bug where the engine's process-wide
	// FileContextStore was used as the source of truth for context window
	// construction, causing session B's user/assistant turns to appear as a
	// prefix to session A's next provider request.
	//
	// Captured live on 2026-05-05 with sessions 611453fb (Alice/Zephyr) and
	// 4efe04d0 (Bob/Helios): after session B sent its turns, session A's
	// next provider.request event contained the entire B history before A's
	// new user message — and the model returned "Name: Bob, Project: Helios"
	// for an Alice-context probe.
	Describe("cross-session context isolation", Label("integration"), func() {
		It("does not leak session B history into session A's provider request", func() {
			recorder := &recordingChunkProvider{
				name: "recording",
				chunks: []provider.StreamChunk{
					{Content: "ok"},
					{Content: "", Done: true},
				},
			}

			store := recall.NewEmptyContextStore("test-model")
			eng := engine.New(engine.Config{
				ChatProvider: recorder,
				Manifest:     newSessionTestManifest(),
				Store:        store,
				TokenCounter: charCounter{},
			})
			mgr := session.NewManager(eng)

			sessA, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())
			sessB, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()

			// Session A turn 1.
			chA1, err := mgr.SendMessage(ctx, sessA.ID, "alice-fact: zephyr")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chA1)

			// Session A turn 2 — within session A only, history should be
			// {alice-fact, ok} preceding the new user turn.
			chA2, err := mgr.SendMessage(ctx, sessA.ID, "alice-followup")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chA2)

			// Session B intervenes with conflicting facts. The bug is that
			// these messages get appended to the engine's shared store and
			// become part of session A's next request.
			chB1, err := mgr.SendMessage(ctx, sessB.ID, "bob-fact: helios")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chB1)

			chB2, err := mgr.SendMessage(ctx, sessB.ID, "bob-followup")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chB2)

			// Session A turn 3 — the smoking-gun call. The provider request
			// must contain ONLY session A's history.
			chA3, err := mgr.SendMessage(ctx, sessA.ID, "alice-probe")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chA3)

			lastA := recorder.lastRequestForUser("alice-probe")
			Expect(lastA).NotTo(BeNil(), "no provider.request captured carrying alice-probe")

			contents := joinUserAssistantContent(lastA.Messages)

			// Session A's prior facts must be present.
			Expect(contents).To(ContainSubstring("alice-fact: zephyr"))
			Expect(contents).To(ContainSubstring("alice-followup"))

			// Session B's facts must NOT appear in session A's payload.
			Expect(contents).NotTo(ContainSubstring("bob-fact: helios"),
				"session B user message leaked into session A's provider request")
			Expect(contents).NotTo(ContainSubstring("bob-followup"),
				"session B follow-up leaked into session A's provider request")
		})

		// Turn 1 of a fresh session has zero prior messages. Earlier
		// fixes attached the per-session message slice to ctx only when
		// non-empty, which meant turn 1 silently fell through to the
		// shared-store path and inherited every other session's
		// accumulated history as a prefix. The fix attaches an empty
		// slice for fresh sessions so the engine still takes the
		// session-scoped path.
		It("does not leak prior session history into a fresh session's first turn", func() {
			recorder := &recordingChunkProvider{
				name: "recording",
				chunks: []provider.StreamChunk{
					{Content: "ok"},
					{Content: "", Done: true},
				},
			}

			store := recall.NewEmptyContextStore("test-model")
			eng := engine.New(engine.Config{
				ChatProvider: recorder,
				Manifest:     newSessionTestManifest(),
				Store:        store,
				TokenCounter: charCounter{},
			})
			mgr := session.NewManager(eng)

			// First session warms the engine's shared store with content
			// the second session must NOT see.
			sessFirst, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())
			ctx := context.Background()

			chF1, err := mgr.SendMessage(ctx, sessFirst.ID, "first-session-secret")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chF1)
			chF2, err := mgr.SendMessage(ctx, sessFirst.ID, "first-session-followup")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chF2)

			// Fresh second session — its turn 1 must be isolated.
			sessFresh, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			chN1, err := mgr.SendMessage(ctx, sessFresh.ID, "fresh-turn-one")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(chN1)

			lastFresh := recorder.lastRequestForUser("fresh-turn-one")
			Expect(lastFresh).NotTo(BeNil(), "no provider.request captured carrying fresh-turn-one")

			contents := joinUserAssistantContent(lastFresh.Messages)

			Expect(contents).To(ContainSubstring("fresh-turn-one"))
			Expect(contents).NotTo(ContainSubstring("first-session-secret"),
				"first session's user message leaked into fresh session's first turn payload")
			Expect(contents).NotTo(ContainSubstring("first-session-followup"),
				"first session's follow-up leaked into fresh session's first turn payload")
		})
	})

	// thinking-block continuity round-trip: an Anthropic extended-thinking
	// turn emits a thinking content block with an encrypted signature. The
	// session accumulator persists those structured ThinkingBlocks on the
	// assistant Message so a subsequent turn can replay them. The
	// session.Manager → engine → provider seam must propagate
	// session.Message.ThinkingBlocks (and StopReason) into the
	// provider.Message instances it constructs for the next-turn request.
	// Without this propagation, Anthropic silently disables extended
	// thinking from turn 2 onward — see provider.Message.ThinkingBlocks
	// and the Phase 3 #2 round-trip plumbing in the provider layer.
	//
	// This spec drives the full production path (session.SendMessage →
	// AccumulateStream → buildContextWindow → provider.Stream) so the
	// behaviour is asserted end-to-end at the same boundary that ships
	// to users, not at a unit-test seam.
	Describe("thinking-block continuity round-trip", Label("integration"), func() {
		It("propagates persisted ThinkingBlocks and StopReason into the next-turn provider request", func() {
			recorder := &recordingChunkProvider{
				name: "recording",
				// Turn 1 stream: a thinking block with a signature, then
				// content, then the upstream stop_reason, then Done. This
				// matches the chunk shape the Anthropic provider emits at
				// content_block_stop / message_delta / message_stop.
				chunks: []provider.StreamChunk{
					{Thinking: "weighing the request", Signature: "sig-encrypted-xyz"},
					{Content: "I have considered it."},
					{EventType: "stop_reason", StopReason: "end_turn"},
					{Content: "", Done: true},
				},
			}

			store := recall.NewEmptyContextStore("test-model")
			eng := engine.New(engine.Config{
				ChatProvider: recorder,
				Manifest:     newSessionTestManifest(),
				Store:        store,
				TokenCounter: charCounter{},
			})
			mgr := session.NewManager(eng)

			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()

			// Turn 1 — drive the stream so the accumulator persists
			// session.Message.ThinkingBlocks for the assistant turn.
			ch1, err := mgr.SendMessage(ctx, sess.ID, "thinking-probe-1")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(ch1)

			// Verify the accumulator captured the structured thinking
			// block on the persisted assistant Message. Without this
			// the rest of the spec is meaningless — we want a clear
			// failure if the upstream Phase 3 #2 plumbing regressed.
			persisted, err := mgr.GetSession(sess.ID)
			Expect(err).NotTo(HaveOccurred())
			var assistantBlocks []provider.ThinkingBlock
			var assistantStopReason string
			for _, m := range persisted.Messages {
				if m.Role == "assistant" {
					assistantBlocks = m.ThinkingBlocks
					assistantStopReason = m.StopReason
				}
			}
			Expect(assistantBlocks).To(HaveLen(1),
				"accumulator must persist the signed thinking block on the assistant message")
			Expect(assistantBlocks[0].Thinking).To(Equal("weighing the request"))
			Expect(assistantBlocks[0].Signature).To(Equal("sig-encrypted-xyz"))
			Expect(assistantStopReason).To(Equal("end_turn"))

			// Turn 2 — the captured provider request must carry the
			// assistant message back with ThinkingBlocks intact. This is
			// the user-visible behaviour: Anthropic only honours extended
			// thinking continuity when the original signature is replayed
			// verbatim on every subsequent turn.
			ch2, err := mgr.SendMessage(ctx, sess.ID, "thinking-probe-2")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(ch2)

			turn2Req := recorder.lastRequestForUser("thinking-probe-2")
			Expect(turn2Req).NotTo(BeNil(), "no provider.request captured for turn 2")

			var replayedAssistant *provider.Message
			for i := range turn2Req.Messages {
				if turn2Req.Messages[i].Role == "assistant" {
					replayedAssistant = &turn2Req.Messages[i]
					break
				}
			}
			Expect(replayedAssistant).NotTo(BeNil(),
				"turn 2 provider request must include the prior assistant turn")
			Expect(replayedAssistant.ThinkingBlocks).To(Equal(assistantBlocks),
				"turn 2 provider request must carry the prior turn's ThinkingBlocks byte-identical to what the accumulator persisted")
			Expect(replayedAssistant.StopReason).To(Equal("end_turn"),
				"turn 2 provider request must carry the prior turn's StopReason")
		})

		It("leaves ThinkingBlocks empty on next-turn requests when the prior turn produced none", func() {
			// Regression guard: messages without thinking blocks must
			// continue to round-trip cleanly. The propagation is
			// zero-safe — an empty source slice projects to an empty
			// destination slice (which marshals away under omitempty).
			recorder := &recordingChunkProvider{
				name: "recording",
				chunks: []provider.StreamChunk{
					{Content: "no thinking here"},
					{Content: "", Done: true},
				},
			}

			store := recall.NewEmptyContextStore("test-model")
			eng := engine.New(engine.Config{
				ChatProvider: recorder,
				Manifest:     newSessionTestManifest(),
				Store:        store,
				TokenCounter: charCounter{},
			})
			mgr := session.NewManager(eng)

			sess, err := mgr.CreateSession("test-agent")
			Expect(err).NotTo(HaveOccurred())

			ctx := context.Background()
			ch1, err := mgr.SendMessage(ctx, sess.ID, "plain-probe-1")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(ch1)

			ch2, err := mgr.SendMessage(ctx, sess.ID, "plain-probe-2")
			Expect(err).NotTo(HaveOccurred())
			drainStreamContent(ch2)

			turn2Req := recorder.lastRequestForUser("plain-probe-2")
			Expect(turn2Req).NotTo(BeNil())

			for _, m := range turn2Req.Messages {
				if m.Role == "assistant" {
					Expect(m.ThinkingBlocks).To(BeEmpty(),
						"non-thinking turn must not synthesise ThinkingBlocks")
					Expect(m.StopReason).To(BeEmpty(),
						"non-thinking turn must not synthesise a StopReason")
				}
			}
		})
	})
})

// charCounter is a minimal TokenCounter for the cross-session isolation
// spec. The spec exercises the buildContextWindow path which requires a
// non-nil counter so the engine constructs a WindowBuilder; the actual
// token counts do not affect the assertion (the spec checks message
// content identity, not token math).
type charCounter struct{}

func (charCounter) Count(s string) int      { return len(s) }
func (charCounter) ModelLimit(_ string) int { return 1_000_000 }

// recordingChunkProvider is a stub Provider that records every Stream
// request it receives so the spec can assert on what messages reached
// the provider boundary. Distinct from streamSequenceProvider (which
// discards the request) and cachedProvider (which wraps a real call).
type recordingChunkProvider struct {
	name     string
	chunks   []provider.StreamChunk
	mu       sync.Mutex
	requests []provider.ChatRequest
}

func (p *recordingChunkProvider) Name() string { return p.name }

func (p *recordingChunkProvider) Stream(_ context.Context, req provider.ChatRequest) (<-chan provider.StreamChunk, error) {
	p.mu.Lock()
	// Deep-copy the messages slice — engine reuses underlying arrays
	// across calls, so a shallow copy would leave assertions racing
	// against later mutations.
	msgsCopy := make([]provider.Message, len(req.Messages))
	copy(msgsCopy, req.Messages)
	captured := req
	captured.Messages = msgsCopy
	p.requests = append(p.requests, captured)
	p.mu.Unlock()

	ch := make(chan provider.StreamChunk, len(p.chunks))
	go func() {
		defer close(ch)
		for _, c := range p.chunks {
			ch <- c
		}
	}()
	return ch, nil
}

func (p *recordingChunkProvider) Chat(_ context.Context, _ provider.ChatRequest) (provider.ChatResponse, error) {
	return provider.ChatResponse{}, nil
}

func (p *recordingChunkProvider) Embed(_ context.Context, _ provider.EmbedRequest) ([]float64, error) {
	return []float64{0.0}, nil
}

func (p *recordingChunkProvider) Models() ([]provider.Model, error) { return nil, nil }

// lastRequestForUser returns the most recent captured request whose
// final user message content matches the given marker, or nil if none.
// Used by the spec to disambiguate which request belongs to which turn
// without relying on call ordering across concurrent SendMessage paths.
func (p *recordingChunkProvider) lastRequestForUser(marker string) *provider.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := len(p.requests) - 1; i >= 0; i-- {
		req := p.requests[i]
		for j := len(req.Messages) - 1; j >= 0; j-- {
			m := req.Messages[j]
			if m.Role == "user" && strings.Contains(m.Content, marker) {
				return &req
			}
		}
	}
	return nil
}

// joinUserAssistantContent collapses the user and assistant message
// contents from a request into a single string so the spec can use
// substring matchers without re-implementing per-role iteration.
func joinUserAssistantContent(msgs []provider.Message) string {
	var parts []string
	for _, m := range msgs {
		if m.Role == "user" || m.Role == "assistant" {
			parts = append(parts, m.Content)
		}
	}
	return strings.Join(parts, "\n")
}
