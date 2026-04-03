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
})
