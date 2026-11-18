package swarm_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/swarm"
)

// sendForTest records msg, panicking on failure (fixture setup).
func sendForTest(q *swarm.SteeringQueue, msg swarm.SteeringMessage) {
	if err := q.Send(msg); err != nil {
		panic("fixture steering send failed: " + err.Error())
	}
}

var _ = Describe("SteeringQueue", func() {
	It("counts a sent message to the addressed child", func() {
		q := swarm.NewSteeringQueue()
		sendForTest(q, swarm.SteeringMessage{From: "sess-p", To: "sess-c", Body: "focus on tests"})

		Expect(q.Queued("sess-c")).To(Equal(1))
	})

	It("drains messages for the addressed child in send order", func() {
		q := swarm.NewSteeringQueue()
		sendForTest(q, swarm.SteeringMessage{From: "sess-p", To: "sess-c", Body: "first"})
		sendForTest(q, swarm.SteeringMessage{From: "sess-p", To: "sess-c", Body: "second"})

		Expect(q.Drain("sess-c")).To(Equal([]swarm.SteeringMessage{
			{From: "sess-p", To: "sess-c", Body: "first"},
			{From: "sess-p", To: "sess-c", Body: "second"},
		}))
	})

	It("retains other children's messages when draining one child", func() {
		q := swarm.NewSteeringQueue()
		sendForTest(q, swarm.SteeringMessage{From: "sess-p", To: "sess-a", Body: "for a"})
		sendForTest(q, swarm.SteeringMessage{From: "sess-p", To: "sess-b", Body: "for b"})
		_ = q.Drain("sess-a")

		Expect(q.Queued("sess-b")).To(Equal(1))
	})

	It("removes each drained message so it is seen once", func() {
		q := swarm.NewSteeringQueue()
		sendForTest(q, swarm.SteeringMessage{From: "sess-p", To: "sess-c", Body: "once"})
		_ = q.Drain("sess-c")
		_ = q.Drain("sess-c")

		Expect(q.Queued("sess-c")).To(Equal(0))
	})

	It("fails closed on an empty sender id", func() {
		q := swarm.NewSteeringQueue()
		err := q.Send(swarm.SteeringMessage{From: "", To: "sess-c", Body: "hi"})

		Expect(err).To(MatchError(ContainSubstring("sender must not be empty")))
	})

	It("fails closed on an empty target id", func() {
		q := swarm.NewSteeringQueue()
		err := q.Send(swarm.SteeringMessage{From: "sess-p", To: "", Body: "hi"})

		Expect(err).To(MatchError(ContainSubstring("target must not be empty")))
	})

	It("fails closed on an empty body", func() {
		q := swarm.NewSteeringQueue()
		err := q.Send(swarm.SteeringMessage{From: "sess-p", To: "sess-c", Body: ""})

		Expect(err).To(MatchError(ContainSubstring("body must not be empty")))
	})
})
