package dispatch_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/dispatch"
)

var _ = Describe("sessionQueue", func() {
	It("returns incrementing one-based positions on enqueue", func() {
		q := dispatch.NewSessionQueue()

		pos1, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-1"})
		Expect(err).NotTo(HaveOccurred())
		Expect(pos1).To(Equal(1))

		pos2, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-2"})
		Expect(err).NotTo(HaveOccurred())
		Expect(pos2).To(Equal(2))
	})

	It("dequeues prompts in FIFO order and returns nil when empty", func() {
		q := dispatch.NewSessionQueue()

		Expect(q.Dequeue()).To(BeNil())

		_, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-1"})
		Expect(err).NotTo(HaveOccurred())
		_, err = q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-2"})
		Expect(err).NotTo(HaveOccurred())

		first := q.Dequeue()
		Expect(first).NotTo(BeNil())
		Expect(first.PromptID).To(Equal("prompt-1"))

		second := q.Dequeue()
		Expect(second).NotTo(BeNil())
		Expect(second.PromptID).To(Equal("prompt-2"))
		Expect(q.Dequeue()).To(BeNil())
	})

	It("cancels prompts by id", func() {
		q := dispatch.NewSessionQueue()

		_, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-1"})
		Expect(err).NotTo(HaveOccurred())
		_, err = q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-2"})
		Expect(err).NotTo(HaveOccurred())

		Expect(q.Cancel("prompt-1")).To(BeTrue())
		Expect(q.Cancel("prompt-1")).To(BeFalse())

		next := q.Dequeue()
		Expect(next).NotTo(BeNil())
		Expect(next.PromptID).To(Equal("prompt-2"))
		Expect(q.Cancel("missing")).To(BeFalse())
	})

	It("rejects enqueue when the queue reaches max depth", func() {
		q := dispatch.NewSessionQueue()

		for i := 0; i < dispatch.MaxQueueDepth; i++ {
			_, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt"})
			Expect(err).NotTo(HaveOccurred())
		}

		_, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "overflow"})
		Expect(err).To(MatchError(dispatch.ErrQueueOverflow))
	})

	It("drops queued prompts on close and rejects later enqueue attempts", func() {
		q := dispatch.NewSessionQueue()

		_, err := q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-1"})
		Expect(err).NotTo(HaveOccurred())
		q.Close()

		Expect(q.Len()).To(Equal(0))
		Expect(q.Dequeue()).To(BeNil())

		_, err = q.Enqueue(dispatch.QueuedPrompt{PromptID: "prompt-2"})
		Expect(err).To(HaveOccurred())
	})
})
