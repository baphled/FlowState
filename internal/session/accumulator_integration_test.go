package session_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
)

var _ = Describe("AccumulateStream integration", Label("integration"), func() {
	var mgr *session.Manager

	BeforeEach(func() {
		mgr = session.NewManager(&noopStreamer{})
	})

	It("reads chunks from channel and appends a single consolidated assistant message to a real Manager", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		rawCh := make(chan provider.StreamChunk, 3)
		rawCh <- provider.StreamChunk{Content: "Hello "}
		rawCh <- provider.StreamChunk{Content: "world"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), mgr, sess.ID, "agent-a", rawCh)
		drainChannel(out)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(HaveLen(1))
		Expect(retrieved.Messages[0].Role).To(Equal("assistant"))
		Expect(retrieved.Messages[0].Content).To(Equal("Hello world"))
		Expect(retrieved.Messages[0].AgentID).To(Equal("agent-a"))
	})

	It("handles stream completion (channel close) gracefully — appends no messages when no content", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		rawCh := make(chan provider.StreamChunk)
		close(rawCh)

		out := session.AccumulateStream(context.Background(), mgr, sess.ID, "agent-a", rawCh)
		drainChannel(out)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(BeEmpty())
	})

	It("handles error chunks — forwards error to consumers without storing an assistant message", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		rawCh := make(chan provider.StreamChunk, 1)
		rawCh <- provider.StreamChunk{Error: context.DeadlineExceeded}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), mgr, sess.ID, "agent-a", rawCh)

		var received []provider.StreamChunk
		for chunk := range out {
			received = append(received, chunk)
		}
		Expect(received).To(HaveLen(1))
		Expect(received[0].Error).To(MatchError(context.DeadlineExceeded))

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(BeEmpty())
	})

	It("RegisterSession idempotency — same ID twice doesn't duplicate the session", func() {
		mgr.RegisterSession("idem-1", "agent-x")
		mgr.RegisterSession("idem-1", "agent-x")

		summaries := mgr.ListSessions()
		count := 0
		for _, s := range summaries {
			if s.ID == "idem-1" {
				count++
			}
		}
		Expect(count).To(Equal(1))
	})

	It("RegisterSession idempotency — preserves the original agent ID", func() {
		mgr.RegisterSession("idem-2", "first-agent")
		mgr.RegisterSession("idem-2", "second-agent")

		retrieved, err := mgr.GetSession("idem-2")
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.AgentID).To(Equal("first-agent"))
	})

	It("AppendMessage stores ToolName and ToolInput on the persisted message", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		msg := session.Message{
			Role:      "tool_result",
			Content:   "output text",
			AgentID:   "agent-a",
			ToolName:  "bash",
			ToolInput: "echo hello",
		}
		mgr.AppendMessage(sess.ID, msg)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(HaveLen(1))
		Expect(retrieved.Messages[0].ToolName).To(Equal("bash"))
		Expect(retrieved.Messages[0].ToolInput).To(Equal("echo hello"))
	})

	It("Session hierarchy — registering child session links it to parent via ParentID", func() {
		mgr.RegisterSession("hierarchy-parent", "coordinator")

		child, err := mgr.CreateWithParent("hierarchy-parent", "worker")
		Expect(err).NotTo(HaveOccurred())
		Expect(child.ParentID).To(Equal("hierarchy-parent"))
	})

	It("streaming multiple chunks produces a single consolidated assistant message", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		rawCh := make(chan provider.StreamChunk, 5)
		rawCh <- provider.StreamChunk{Content: "chunk1 "}
		rawCh <- provider.StreamChunk{Content: "chunk2 "}
		rawCh <- provider.StreamChunk{Content: "chunk3"}
		rawCh <- provider.StreamChunk{Done: true}
		close(rawCh)

		out := session.AccumulateStream(context.Background(), mgr, sess.ID, "agent-a", rawCh)
		drainChannel(out)

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(HaveLen(1))
		Expect(retrieved.Messages[0].Role).To(Equal("assistant"))
		Expect(retrieved.Messages[0].Content).To(Equal("chunk1 chunk2 chunk3"))
	})

	It("AccumulateStream with cancelled context / closed upstream stops forwarding cleanly", func() {
		sess, err := mgr.CreateSession("agent-a")
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		rawCh := make(chan provider.StreamChunk)
		close(rawCh)

		out := session.AccumulateStream(context.Background(), mgr, sess.ID, "agent-a", rawCh)

		Eventually(out).Should(BeClosed())

		retrieved, err := mgr.GetSession(sess.ID)
		Expect(err).NotTo(HaveOccurred())
		Expect(retrieved.Messages).To(BeEmpty())
		_ = ctx
	})
})
