package session_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("Session resumption fidelity", Label("integration"), func() {
	var (
		mgr        *session.Manager
		mockStream *mockStreamer
		ctx        context.Context
	)

	BeforeEach(func() {
		mockStream = newMockStreamer()
		mgr = session.NewManager(mockStream)
		ctx = context.Background()
	})

	It("create session → send messages → GetSession returns both messages in order", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		mockStream.addChunk(provider.StreamChunk{Content: "response one"})
		mockStream.addChunk(provider.StreamChunk{Done: true})
		ch, err := mgr.SendMessage(ctx, sess.ID, "first user message")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(ch)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(HaveLen(2))
		Expect(retrieved.Messages[0].Role).To(Equal("user"))
		Expect(retrieved.Messages[0].Content).To(Equal("first user message"))
		Expect(retrieved.Messages[1].Role).To(Equal("assistant"))
		Expect(retrieved.Messages[1].Content).To(Equal("response one"))
	})

	It("message roles (user, assistant, tool_result) preserved exactly", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		mockStream.addChunk(provider.StreamChunk{
			ToolCall: &provider.ToolCall{
				Name:      "bash",
				Arguments: map[string]any{"command": "ls"},
			},
		})
		mockStream.addChunk(provider.StreamChunk{
			ToolResult: &provider.ToolResultInfo{Content: "file.go"},
		})
		mockStream.addChunk(provider.StreamChunk{Done: true})

		ch, err := mgr.SendMessage(ctx, sess.ID, "run ls")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(ch)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())

		roles := make([]string, 0, len(retrieved.Messages))
		for _, m := range retrieved.Messages {
			roles = append(roles, m.Role)
		}
		Expect(roles).To(ContainElement("user"))
		Expect(roles).To(ContainElement("tool_result"))
	})

	It("ToolName and ToolInput fields preserved on retrieved messages", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		mockStream.addChunk(provider.StreamChunk{
			ToolCall: &provider.ToolCall{
				Name:      "bash",
				Arguments: map[string]any{"command": "echo hello"},
			},
		})
		mockStream.addChunk(provider.StreamChunk{
			ToolResult: &provider.ToolResultInfo{Content: "hello"},
		})
		mockStream.addChunk(provider.StreamChunk{Done: true})

		ch, err := mgr.SendMessage(ctx, sess.ID, "greet")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(ch)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())

		var toolMsg *session.Message
		for i := range retrieved.Messages {
			if retrieved.Messages[i].Role == "tool_result" {
				toolMsg = &retrieved.Messages[i]
				break
			}
		}
		Expect(toolMsg).NotTo(BeNil())
		Expect(toolMsg.ToolName).To(Equal("bash"))
		Expect(toolMsg.ToolInput).To(Equal("echo hello"))
	})

	It("AgentID on messages from different agents preserved correctly", func() {
		sessA, err := mgr.CreateSession("agent-alpha")
		Expect(err).NotTo(HaveOccurred())

		sessB, err := mgr.CreateSession("agent-beta")
		Expect(err).NotTo(HaveOccurred())

		mockStream.addChunk(provider.StreamChunk{Content: "alpha reply"})
		mockStream.addChunk(provider.StreamChunk{Done: true})
		chA, err := mgr.SendMessage(ctx, sessA.ID, "hello from alpha")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(chA)

		mockStream.addChunk(provider.StreamChunk{Content: "beta reply"})
		mockStream.addChunk(provider.StreamChunk{Done: true})
		chB, err := mgr.SendMessage(ctx, sessB.ID, "hello from beta")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(chB)

		retrievedA, err := mgr.GetSession(sessA.ID)
		Expect(err).NotTo(HaveOccurred())
		retrievedB, err := mgr.GetSession(sessB.ID)
		Expect(err).NotTo(HaveOccurred())

		for _, m := range retrievedA.Messages {
			Expect(m.AgentID).To(Equal("agent-alpha"))
		}
		for _, m := range retrievedB.Messages {
			Expect(m.AgentID).To(Equal("agent-beta"))
		}
	})

	It("message timestamps are non-zero after retrieval", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		mockStream.addChunk(provider.StreamChunk{Content: "reply"})
		mockStream.addChunk(provider.StreamChunk{Done: true})
		ch, err := mgr.SendMessage(ctx, sess.ID, "hello")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(ch)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).NotTo(BeEmpty())
		for _, m := range retrieved.Messages {
			Expect(m.Timestamp.IsZero()).To(BeFalse())
		}
	})

	It("multiple rounds of messages maintain exact ordering", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		mockStream.addChunk(provider.StreamChunk{Content: "reply one"})
		mockStream.addChunk(provider.StreamChunk{Done: true})
		ch, err := mgr.SendMessage(ctx, sess.ID, "message one")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(ch)

		mockStream.addChunk(provider.StreamChunk{Content: "reply two"})
		mockStream.addChunk(provider.StreamChunk{Done: true})
		ch, err = mgr.SendMessage(ctx, sess.ID, "message two")
		Expect(err).NotTo(HaveOccurred())
		drainChannel(ch)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(HaveLen(4))
		Expect(retrieved.Messages[0].Content).To(Equal("message one"))
		Expect(retrieved.Messages[1].Content).To(Equal("reply one"))
		Expect(retrieved.Messages[2].Content).To(Equal("message two"))
		Expect(retrieved.Messages[3].Content).To(Equal("reply two"))
	})

	It("session Status transitions preserved on GetSession", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Status).To(Equal("active"))

		err = mgr.CloseSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())

		retrieved, err = mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Status).To(Equal("completed"))
	})

	It("CreateWithParent → ChildSessions returns the child", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())

		child, err := mgr.CreateWithParent(parent.ID, "worker")
		Expect(err).NotTo(HaveOccurred())

		children, err := mgr.ChildSessions(parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(HaveLen(1))
		Expect(children[0].ID).To(Equal(child.ID))
	})

	It("multiple CreateWithParent → ChildSessions returns ALL children", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())

		child1, err := mgr.CreateWithParent(parent.ID, "worker-1")
		Expect(err).NotTo(HaveOccurred())

		child2, err := mgr.CreateWithParent(parent.ID, "worker-2")
		Expect(err).NotTo(HaveOccurred())

		child3, err := mgr.CreateWithParent(parent.ID, "worker-3")
		Expect(err).NotTo(HaveOccurred())

		children, err := mgr.ChildSessions(parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(HaveLen(3))

		childIDs := make([]string, 0, len(children))
		for _, c := range children {
			childIDs = append(childIDs, c.ID)
		}
		Expect(childIDs).To(ContainElement(child1.ID))
		Expect(childIDs).To(ContainElement(child2.ID))
		Expect(childIDs).To(ContainElement(child3.ID))
	})

	It("child session AgentID matches delegated agent", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())

		child, err := mgr.CreateWithParent(parent.ID, "specialist-agent")
		Expect(err).NotTo(HaveOccurred())

		retrieved, err := mgr.GetSession(child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.AgentID).To(Equal("specialist-agent"))
	})

	It("child session messages independently accessible via GetSession", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())

		child, err := mgr.CreateWithParent(parent.ID, "worker")
		Expect(err).NotTo(HaveOccurred())

		mgr.AppendMessage(child.ID, session.Message{
			Role:    "user",
			Content: "child task",
			AgentID: "worker",
		})

		retrieved, err := mgr.GetSession(child.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(HaveLen(1))
		Expect(retrieved.Messages[0].Content).To(Equal("child task"))
	})

	It("parent messages NOT polluted by child session messages", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())

		mgr.AppendMessage(parent.ID, session.Message{
			Role:    "user",
			Content: "parent task",
			AgentID: "coordinator",
		})

		child, err := mgr.CreateWithParent(parent.ID, "worker")
		Expect(err).NotTo(HaveOccurred())

		mgr.AppendMessage(child.ID, session.Message{
			Role:    "user",
			Content: "child task",
			AgentID: "worker",
		})

		retrievedParent, err := mgr.GetSession(parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrievedParent.Messages).To(HaveLen(1))
		Expect(retrievedParent.Messages[0].Content).To(Equal("parent task"))
	})

	It("ChildSessions returns empty slice when no children exist", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())

		children, err := mgr.ChildSessions(parent.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(children).To(BeEmpty())
	})

	It("child Depth = parent.Depth + 1", func() {
		parent, err := mgr.CreateSession("coordinator")
		Expect(err).NotTo(HaveOccurred())
		Expect(parent.Depth).To(Equal(0))

		child, err := mgr.CreateWithParent(parent.ID, "worker")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.Depth).To(Equal(parent.Depth + 1))
	})
})
