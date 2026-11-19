package app

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/store"
)

// steerChainPair seeds a parent with one delegated child and returns
// the store plus both session ids; panics are setup failures.
func steerChainPair() (*store.Store, string, string) {
	st, err := store.OpenMemory()
	if err != nil {
		panic(err)
	}
	DeferCleanup(func() { _ = st.Close() })
	ctx := context.Background()
	parent, err := CreateSession(ctx, st, "steer-parent-prov", "model-x", DefaultHarness, "cfg", "parent")
	if err != nil {
		panic(err)
	}
	child, err := CreateChildSession(ctx, st, parent.ID, "prov", "model-x", DefaultHarness, "cfg", "child")
	if err != nil {
		panic(err)
	}
	return st, parent.ID, child.ID
}

var _ = Describe("Steering message send", func() {
	It("records a steering message from an ancestor in the chain", func() {
		st, parentID, childID := steerChainPair()
		msg, err := SendSteeringMessage(context.Background(), st, childID, parentID, "refocus")
		if err != nil {
			panic(err)
		}
		Expect([]string{msg.Sender, msg.Body}).To(Equal([]string{parentID, "refocus"}))
	})

	It("maps an out-of-chain sender to usage", func() {
		st, _, _ := steerChainPair()
		ctx := context.Background()
		stranger, serr := CreateSession(ctx, st, "steer-stranger-prov", "model-x", DefaultHarness, "cfg", "stranger")
		if serr != nil {
			panic(serr)
		}

		_, serr2 := SendSteeringMessage(ctx, st, "steer-parent-prov-child", stranger.ID, "cross-tree")
		Expect(errors.Is(serr2, ErrUsage)).To(BeTrue())
	})

	It("rejects empty bodies at the store boundary", func() {
		st, parentID, childID := steerChainPair()
		_, merr := st.SendMessage(context.Background(), childID, parentID, "")
		Expect(merr).To(MatchError(store.ErrMessageEmpty))
	})
})

var _ = Describe("Steering message drain", func() {
	var drainedCount int
	var steeredBodies []string
	BeforeEach(func() {
		steeredBodies = nil
		st, parentID, childID := steerChainPair()
		ctx := context.Background()
		if _, err := st.SendMessage(ctx, childID, parentID, "first"); err != nil {
			panic(err)
		}
		if _, err := st.SendMessage(ctx, childID, parentID, "second"); err != nil {
			panic(err)
		}
		n, derr := st.DrainSteeringMessagesFor(ctx, childID)
		if derr != nil {
			panic(derr)
		}
		drainedCount = n
		_, events, ierr := InspectSession(ctx, st, childID)
		if ierr != nil {
			panic(ierr)
		}
		for _, ev := range events {
			if ev.Type == "session.steered" {
				var payload map[string]any
				if uerr := json.Unmarshal([]byte(ev.PayloadJSON), &payload); uerr != nil {
					panic(uerr)
				}
				steeredBodies = append(steeredBodies, payload["body"].(string))
			}
		}
	})

	It("reports the drained count", func() {
		Expect(drainedCount).To(Equal(2))
	})

	It("preserves send order in the steered events", func() {
		Expect(steeredBodies).To(Equal([]string{"first", "second"}))
	})

	It("re-drains nothing after delivery", func() {
		st, _, childID := steerChainPair()
		again, aerr := st.DrainSteeringMessagesFor(context.Background(), childID)
		if aerr != nil {
			panic(aerr)
		}
		pending, perr := st.PendingSteeringMessages(context.Background(), childID)
		if perr != nil {
			panic(perr)
		}
		Expect([]int{again, len(pending)}).To(Equal([]int{0, 0}))
	})

	It("drains every pending message into ordered session.steered events", func() {
		st, parentID, childID := steerChainPair()
		ctx := context.Background()
		if _, err := st.SendMessage(ctx, childID, parentID, "first"); err != nil {
			panic(err)
		}
		if _, err := st.SendMessage(ctx, childID, parentID, "second"); err != nil {
			panic(err)
		}

		_, derr := st.DrainSteeringMessagesFor(ctx, childID)
		if derr != nil {
			panic(derr)
		}

		_, events, ierr := InspectSession(ctx, st, childID)
		if ierr != nil {
			panic(ierr)
		}
		var steeredBodies []string
		for _, ev := range events {
			if ev.Type == "session.steered" {
				var payload map[string]any
				if uerr := json.Unmarshal([]byte(ev.PayloadJSON), &payload); uerr != nil {
					panic(uerr)
				}
				steeredBodies = append(steeredBodies, payload["body"].(string))
			}
		}
		Expect(steeredBodies).To(Equal([]string{"first", "second"}))
	})

	It("reports the drained count of two", func() {
		Expect(drainedCount).To(Equal(2))
	})

	It("delivers each message exactly once", func() {
		st, parentID, childID := steerChainPair()
		ctx := context.Background()
		if _, err := st.SendMessage(ctx, childID, parentID, "only"); err != nil {
			panic(err)
		}

		n, derr := st.DrainSteeringMessagesFor(ctx, childID)
		if derr != nil {
			panic(derr)
		}
		again, aerr := st.DrainSteeringMessagesFor(ctx, childID)
		if aerr != nil {
			panic(aerr)
		}
		pending, perr := st.PendingSteeringMessages(ctx, childID)
		if perr != nil {
			panic(perr)
		}
		Expect([]int{n, again, len(pending)}).To(Equal([]int{1, 0, 0}))
	})
})
