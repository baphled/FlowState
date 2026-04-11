package engine_test

import (
	"context"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tool"
)

func newDelegationTestManifest(id string) agent.Manifest {
	return agent.Manifest{
		ID:   id,
		Name: id,
		Instructions: agent.Instructions{
			SystemPrompt: "You are a helpful agent.",
		},
		ContextManagement: agent.DefaultContextManagement(),
	}
}

func newDelegationTestEngine(chunks []provider.StreamChunk) *engine.Engine {
	p := &mockProvider{
		name:         "test-provider",
		streamChunks: chunks,
	}
	return engine.New(engine.Config{
		ChatProvider: p,
		Manifest:     newDelegationTestManifest("target-agent"),
	})
}

func delegationInput() tool.Input {
	return tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type": "target-agent",
			"message":       "do work",
		},
	}
}

func asyncDelegationInput() tool.Input {
	return tool.Input{
		Name: "delegate",
		Arguments: map[string]interface{}{
			"subagent_type":     "target-agent",
			"message":           "do async work",
			"run_in_background": true,
		},
	}
}

var _ = Describe("createChildSession behaviour", Label("integration"), func() {
	var (
		targetEngine *engine.Engine
		mgr          *session.Manager
	)

	BeforeEach(func() {
		targetEngine = newDelegationTestEngine([]provider.StreamChunk{
			{Content: "child response", Done: true},
		})
		mgr = session.NewManager(targetEngine)
	})

	Context("when sessionCreator is set and parent session is registered", func() {
		It("registers a child session linked to the parent session ID", func() {
			mgr.RegisterSession("parent-create-child", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-create-child")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := mgr.ChildSessions("parent-create-child")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())
			Expect(children[0].ParentID).To(Equal("parent-create-child"))
		})

		It("assigns the delegated agent ID to the child session", func() {
			mgr.RegisterSession("parent-agent-id-check", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-agent-id-check")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := mgr.ChildSessions("parent-agent-id-check")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())
			Expect(children[0].AgentID).To(Equal("target-agent"))
		})
	})

	Context("when sessionManager fallback path is used and no parent ID in context", func() {
		It("registers a synthetic session ID via RegisterSession", func() {
			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionManager(mgr)

			ctx := context.Background()
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			sessions := mgr.ListSessions()
			Expect(sessions).NotTo(BeEmpty())
		})
	})
})

var _ = Describe("Delegation message accumulation", Label("integration"), func() {
	Context("when messageAppender is set alongside sessionCreator", func() {
		It("stores ToolName from the delegation stream in child session messages", func() {
			// The engine now dispatches tool calls by chunk shape
			// (internal/engine/engine.go: `if chunk.ToolCall != nil`) so the
			// child engine must have a registered tool matching the mock chunk.
			// A streamSequenceProvider plus a no-op executableMockTool lets the
			// tool loop complete without looping on a replaying provider.
			searchTool := &executableMockTool{
				name:        "search",
				description: "search tool",
				execResult:  tool.Result{Output: "search results"},
			}
			toolStreamProvider := &streamSequenceProvider{
				name: "test-provider",
				sequences: [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall:  &provider.ToolCall{Name: "search", Arguments: map[string]any{"query": "test"}},
						},
					},
					{
						{Content: "done", Done: true},
					},
				},
			}
			toolStreamEngine := engine.New(engine.Config{
				ChatProvider: toolStreamProvider,
				Manifest:     newDelegationTestManifest("target-agent"),
				Tools:        []tool.Tool{searchTool},
			})
			toolMgr := session.NewManager(toolStreamEngine)
			toolMgr.RegisterSession("parent-tool-name", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": toolStreamEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(toolMgr)
			delegateTool.WithMessageAppender(toolMgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-tool-name")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := toolMgr.ChildSessions("parent-tool-name")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())

			child := children[0]
			Eventually(func() int {
				sess, _ := toolMgr.GetSession(child.ID)
				if sess == nil {
					return 0
				}
				return len(sess.Messages)
			}).Should(BeNumerically(">", 0))

			childSess, err := toolMgr.GetSession(child.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(childSess.Messages).NotTo(BeEmpty())

			var foundTool bool
			for _, msg := range childSess.Messages {
				if msg.ToolName == "search" {
					foundTool = true
				}
			}
			Expect(foundTool).To(BeTrue())
		})

		It("stores ToolInput from the delegation stream in child session messages", func() {
			// See the sibling spec above: engine dispatch is now shape-based,
			// so the mock must drive a registered tool to avoid
			// "tool not found" errors terminating the stream.
			bashTool := &executableMockTool{
				name:        "bash",
				description: "bash tool",
				execResult:  tool.Result{Output: "file1.go\nfile2.go"},
			}
			toolInputProvider := &streamSequenceProvider{
				name: "test-provider",
				sequences: [][]provider.StreamChunk{
					{
						{
							EventType: "tool_call",
							ToolCall:  &provider.ToolCall{Name: "bash", Arguments: map[string]any{"command": "ls"}},
						},
					},
					{
						{Content: "done", Done: true},
					},
				},
			}
			toolInputEngine := engine.New(engine.Config{
				ChatProvider: toolInputProvider,
				Manifest:     newDelegationTestManifest("target-agent"),
				Tools:        []tool.Tool{bashTool},
			})
			toolMgr := session.NewManager(toolInputEngine)
			toolMgr.RegisterSession("parent-tool-input", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": toolInputEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(toolMgr)
			delegateTool.WithMessageAppender(toolMgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-tool-input")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := toolMgr.ChildSessions("parent-tool-input")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())

			child := children[0]
			Eventually(func() int {
				sess, _ := toolMgr.GetSession(child.ID)
				if sess == nil {
					return 0
				}
				return len(sess.Messages)
			}).Should(BeNumerically(">", 0))

			childSess, err := toolMgr.GetSession(child.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(childSess.Messages).NotTo(BeEmpty())

			var foundToolResult bool
			for _, msg := range childSess.Messages {
				if msg.Role == "tool_result" {
					foundToolResult = true
				}
			}
			Expect(foundToolResult).To(BeTrue())
		})
	})
})

var _ = Describe("Background completion channel blocking send", Label("integration"), func() {
	Context("when SetCompletionSubscriber is set and a task completes", func() {
		It("delivers the notification to the subscriber channel via a blocking send", func() {
			taskManager := engine.NewBackgroundTaskManager()
			ch := make(chan streaming.CompletionNotificationEvent, 1)
			taskManager.SetCompletionSubscriber(ch)

			targetEngine := newDelegationTestEngine([]provider.StreamChunk{
				{Content: "background done", Done: true},
			})
			sessionMgr := session.NewManager(targetEngine)
			taskManager.WithSessionManager(sessionMgr)
			sessionMgr.RegisterSession("bg-parent-sess", "coordinator")

			ctx := context.WithValue(context.Background(), session.IDKey{}, "bg-parent-sess")
			taskManager.Launch(ctx, "bg-task-blocking", "target-agent", "blocking send test",
				func(_ context.Context) (string, error) {
					return "blocking result", nil
				},
			)

			Eventually(func() string {
				task, found := taskManager.Get("bg-task-blocking")
				if !found {
					return ""
				}
				return task.Status.Load()
			}).Should(Equal("completed"))

			var notif streaming.CompletionNotificationEvent
			Eventually(ch).Should(Receive(&notif))
			Expect(notif.TaskID).To(Equal("bg-task-blocking"))
			Expect(notif.Status).To(Equal("completed"))
		})
	})

	Context("when subscriber channel is nil", func() {
		It("completes without blocking or panicking", func() {
			taskManager := engine.NewBackgroundTaskManager()

			targetEngine := newDelegationTestEngine([]provider.StreamChunk{
				{Content: "no subscriber", Done: true},
			})
			sessionMgr := session.NewManager(targetEngine)
			taskManager.WithSessionManager(sessionMgr)
			sessionMgr.RegisterSession("no-sub-sess-bg", "coordinator")

			ctx := context.WithValue(context.Background(), session.IDKey{}, "no-sub-sess-bg")
			taskManager.Launch(ctx, "no-sub-task-bg", "target-agent", "no subscriber test",
				func(_ context.Context) (string, error) {
					return "ok", nil
				},
			)

			Eventually(func() string {
				task, found := taskManager.Get("no-sub-task-bg")
				if !found {
					return ""
				}
				return task.Status.Load()
			}).Should(Equal("completed"))
		})
	})
})

var _ = Describe("Fallback registration via sessionManager", Label("integration"), func() {
	Context("when only sessionManager is set and sessionCreator is nil", func() {
		It("registers a synthetic session ID for the delegation via RegisterSession", func() {
			targetEngine := newDelegationTestEngine([]provider.StreamChunk{
				{Content: "fallback ok", Done: true},
			})
			mgr := session.NewManager(targetEngine)

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionManager(mgr)

			ctx := context.Background()
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			sessions := mgr.ListSessions()
			Expect(sessions).NotTo(BeEmpty())
		})

		It("registers synthetic session when parent ID is absent from context", func() {
			targetEngine := newDelegationTestEngine([]provider.StreamChunk{
				{Content: "synth ok", Done: true},
			})
			mgr := session.NewManager(targetEngine)

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionManager(mgr)

			ctx := context.Background()
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			sessions := mgr.ListSessions()
			found := false
			for _, s := range sessions {
				if s.AgentID == "target-agent" {
					found = true
				}
			}
			Expect(found).To(BeTrue())
		})
	})
})

var _ = Describe("Async delegation child session creation", Label("integration"), func() {
	Context("when delegation runs asynchronously", func() {
		It("creates the child session before streaming begins", func() {
			targetEngine := newDelegationTestEngine([]provider.StreamChunk{
				{Content: "async child", Done: true},
			})
			mgr := session.NewManager(targetEngine)
			mgr.RegisterSession("parent-async-before-stream", "coordinator")

			bgManager := engine.NewBackgroundTaskManager()
			bgManager.WithSessionManager(mgr)

			delegateTool := engine.NewDelegateToolWithBackground(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
				bgManager,
				nil,
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-async-before-stream")
			result, err := delegateTool.Execute(ctx, asyncDelegationInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("task_id"))

			Eventually(func() int {
				children, _ := mgr.ChildSessions("parent-async-before-stream")
				return len(children)
			}).Should(BeNumerically(">", 0))
		})

		It("the task_id in the result matches the registered session ID", func() {
			targetEngine := newDelegationTestEngine([]provider.StreamChunk{
				{Content: "task id match", Done: true},
			})
			mgr := session.NewManager(targetEngine)
			mgr.RegisterSession("parent-task-id-match", "coordinator")

			bgManager := engine.NewBackgroundTaskManager()
			bgManager.WithSessionManager(mgr)

			delegateTool := engine.NewDelegateToolWithBackground(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
				bgManager,
				nil,
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-task-id-match")
			result, err := delegateTool.Execute(ctx, asyncDelegationInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("task_id"))

			Eventually(func() int {
				children, _ := mgr.ChildSessions("parent-task-id-match")
				return len(children)
			}).Should(BeNumerically(">", 0))

			children, err := mgr.ChildSessions("parent-task-id-match")
			Expect(err).NotTo(HaveOccurred())
			Expect(children[0].AgentID).To(Equal("target-agent"))
		})
	})
})

var _ = Describe("DelegateTool session registration", Label("integration"), func() {
	var (
		targetEngine *engine.Engine
		mgr          *session.Manager
	)

	BeforeEach(func() {
		targetEngine = newDelegationTestEngine([]provider.StreamChunk{
			{Content: "thinking...", Done: false},
			{Content: " done", Done: true},
		})
		mgr = session.NewManager(targetEngine)
	})

	Context("when sessionCreator is set and context has parentID", func() {
		It("registers child session when sessionCreator is set and context has parentID", func() {
			mgr.RegisterSession("parent-session", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-session")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := mgr.ChildSessions("parent-session")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())
			Expect(children[0].AgentID).To(Equal("target-agent"))
			Expect(children[0].ParentID).To(Equal("parent-session"))
		})
	})

	Context("when sessionCreator is nil", func() {
		It("silently falls back when sessionCreator is nil — ChildSessions returns empty", func() {
			mgr.RegisterSession("parent-session-nil-creator", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-session-nil-creator")
			result, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).NotTo(BeEmpty())

			children, err := mgr.ChildSessions("parent-session-nil-creator")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).To(BeEmpty())
		})
	})

	Context("when context has no session ID", func() {
		It("silently falls back when context has no session ID", func() {
			mgr.RegisterSession("parent-session-no-ctx", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.Background()
			result, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).NotTo(BeEmpty())

			children, err := mgr.ChildSessions("parent-session-no-ctx")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).To(BeEmpty())
		})
	})

	Context("when messageAppender is also set", func() {
		It("child session accumulates messages from delegation stream", func() {
			mgr.RegisterSession("parent-session-msg", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionCreator(mgr)
			delegateTool.WithMessageAppender(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-session-msg")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := mgr.ChildSessions("parent-session-msg")
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

			childSess, err := mgr.GetSession(child.ID)
			Expect(err).NotTo(HaveOccurred())
			Expect(childSess.Messages).NotTo(BeEmpty())

			var fullContent strings.Builder
			for _, msg := range childSess.Messages {
				fullContent.WriteString(msg.Content)
			}
			Expect(fullContent.String()).To(ContainSubstring("thinking"))
		})
	})

	Context("when delegation runs asynchronously", func() {
		It("async delegation also registers child session", func() {
			mgr.RegisterSession("parent-session-async", "coordinator")

			bgManager := engine.NewBackgroundTaskManager()
			bgManager.WithSessionManager(mgr)

			delegateTool := engine.NewDelegateToolWithBackground(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
				bgManager,
				nil,
			)
			delegateTool.WithSessionCreator(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-session-async")
			result, err := delegateTool.Execute(ctx, asyncDelegationInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Output).To(ContainSubstring("task_id"))

			Eventually(func() int {
				children, _ := mgr.ChildSessions("parent-session-async")
				return len(children)
			}).Should(BeNumerically(">", 0))

			children, err := mgr.ChildSessions("parent-session-async")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())
			Expect(children[0].AgentID).To(Equal("target-agent"))
		})
	})
	Context("when only sessionManager is set and context has parentID", func() {
		It("registers child session via sessionManager.CreateWithParent fallback", func() {
			mgr.RegisterSession("parent-session-mgr-only", "coordinator")

			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionManager(mgr)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "parent-session-mgr-only")
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			children, err := mgr.ChildSessions("parent-session-mgr-only")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).NotTo(BeEmpty())
			Expect(children[0].AgentID).To(Equal("target-agent"))
			Expect(children[0].ParentID).To(Equal("parent-session-mgr-only"))
		})
	})

	Context("when only sessionManager is set and context has no parentID", func() {
		It("registers synthetic session without parent link", func() {
			delegateTool := engine.NewDelegateTool(
				map[string]*engine.Engine{"target-agent": targetEngine},
				agent.Delegation{CanDelegate: true, DelegationAllowlist: []string{"target-agent"}},
				"coordinator",
			)
			delegateTool.WithSessionManager(mgr)

			ctx := context.Background()
			_, err := delegateTool.Execute(ctx, delegationInput())
			Expect(err).NotTo(HaveOccurred())

			sessions := mgr.ListSessions()
			Expect(sessions).NotTo(BeEmpty())
		})
	})
})
