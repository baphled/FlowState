package turn_test

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/turn"
)

func TestTurn(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Turn Suite")
}

// fixedClock returns a monotonically-increasing time so CompletedAt
// is strictly greater than StartedAt across consecutive calls.
type fixedClock struct {
	base time.Time
	tick int
}

func (c *fixedClock) Now() time.Time {
	c.tick++
	return c.base.Add(time.Duration(c.tick) * time.Second)
}

var _ = Describe("Registry", func() {
	var (
		reg   *turn.Registry
		clock *fixedClock
	)

	BeforeEach(func() {
		clock = &fixedClock{base: time.Date(2026, 5, 19, 12, 0, 0, 0, time.UTC)}
		// Deterministic ID generator — each Start produces a fresh
		// numeric id keyed off the registry call count so assertions
		// can pin the exact turn_id values without UUID regex noise.
		i := 0
		idGen := func() string {
			i++
			return "turn-" + string(rune('0'+i))
		}
		reg = turn.NewRegistryWithIDGen(idGen, clock.Now)
	})

	Context("Start", func() {
		It("returns a fresh turn id in StatusRunning", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(id).NotTo(BeEmpty())

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.ID).To(Equal(id))
			Expect(t.SessionID).To(Equal("sess-1"))
			Expect(t.Status).To(Equal(turn.StatusRunning))
			Expect(t.StartedAt).NotTo(BeZero())
			Expect(t.CompletedAt).To(BeNil())
			Expect(t.MessagesAdded).To(BeEmpty())
		})

		It("returns ErrTurnConflict on a second Start while the first is Running", func() {
			_, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			_, err2 := reg.Start("sess-1")
			Expect(err2).To(MatchError(turn.ErrTurnConflict),
				"only one in-flight Turn per session at v1 — concurrent POST must surface ErrTurnConflict for the HTTP layer to map to 409")
		})

		It("permits a new Start after the prior turn has Completed", func() {
			id1, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id1, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

			id2, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(id2).NotTo(Equal(id1))
		})

		It("permits a new Start after the prior turn has Failed", func() {
			id1, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Fail(id1, errors.New("boom"))).To(Succeed())

			_, err = reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
		})

		It("does NOT cross-block different sessions", func() {
			_, err := reg.Start("sess-a")
			Expect(err).NotTo(HaveOccurred())

			_, err = reg.Start("sess-b")
			Expect(err).NotTo(HaveOccurred(),
				"per-session keying must not serialise across sessions — sess-b's Start must succeed while sess-a is still Running")
		})
	})

	Context("Append", func() {
		It("routes messages to the correct Turn in arrival order", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "one"})).To(Succeed())
			Expect(reg.Append(id, session.Message{Role: "thinking", Content: "two"})).To(Succeed())
			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "three"})).To(Succeed())

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.MessagesAdded).To(HaveLen(3))
			Expect(t.MessagesAdded[0].Content).To(Equal("one"))
			Expect(t.MessagesAdded[1].Content).To(Equal("two"))
			Expect(t.MessagesAdded[2].Content).To(Equal("three"))
		})

		It("returns ErrTurnNotFound for an unknown turn id", func() {
			err := reg.Append("never-minted", session.Message{Role: "assistant", Content: "x"})
			Expect(err).To(MatchError(turn.ErrTurnNotFound))
		})

		It("is a silent no-op when turnID is empty (accumulator sees no turn_id in ctx)", func() {
			Expect(reg.Append("", session.Message{Role: "assistant", Content: "x"})).To(Succeed())
		})

		It("returns ErrTurnTerminal once the turn has Completed", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id, turn.ModelInfo{})).To(Succeed())

			err = reg.Append(id, session.Message{Role: "assistant", Content: "late"})
			Expect(err).To(MatchError(turn.ErrTurnTerminal))
		})

		It("does NOT route messages across turns on the same session", func() {
			id1, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Append(id1, session.Message{Role: "assistant", Content: "turn-1-msg"})).To(Succeed())
			Expect(reg.Complete(id1, turn.ModelInfo{})).To(Succeed())

			id2, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Append(id2, session.Message{Role: "assistant", Content: "turn-2-msg"})).To(Succeed())

			t1, _ := reg.Get(id1)
			t2, _ := reg.Get(id2)
			Expect(t1.MessagesAdded).To(HaveLen(1))
			Expect(t1.MessagesAdded[0].Content).To(Equal("turn-1-msg"))
			Expect(t2.MessagesAdded).To(HaveLen(1))
			Expect(t2.MessagesAdded[0].Content).To(Equal("turn-2-msg"))
		})
	})

	Context("Complete", func() {
		It("transitions Status to completed and stamps CompletedAt", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			Expect(reg.Complete(id, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.Status).To(Equal(turn.StatusCompleted))
			Expect(t.CompletedAt).NotTo(BeNil())
			Expect(t.CompletedAt.After(t.StartedAt)).To(BeTrue(),
				"CompletedAt must be strictly greater than StartedAt for a turn that did real work")
			Expect(t.Model.Provider).To(Equal("anthropic"))
			Expect(t.Model.Model).To(Equal("claude-opus-4-7"))
			Expect(t.Error).To(BeEmpty(),
				"a Completed turn must have an empty Error — Failed is the error-carrying state")
		})

		It("releases the per-session conflict gate so the next Start proceeds", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id, turn.ModelInfo{})).To(Succeed())

			_, err = reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred(),
				"after Complete fires, the active-session map entry must clear so the next POST can mint a fresh turn")
		})

		It("returns ErrTurnNotFound for an unknown turn id", func() {
			err := reg.Complete("never-minted", turn.ModelInfo{})
			Expect(err).To(MatchError(turn.ErrTurnNotFound))
		})

		It("returns ErrTurnTerminal on double-complete", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id, turn.ModelInfo{})).To(Succeed())

			err = reg.Complete(id, turn.ModelInfo{})
			Expect(err).To(MatchError(turn.ErrTurnTerminal))
		})
	})

	Context("Fail", func() {
		It("transitions Status to failed and populates Error", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			cause := errors.New("provider stream ruptured")
			Expect(reg.Fail(id, cause)).To(Succeed())

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.Status).To(Equal(turn.StatusFailed))
			Expect(t.CompletedAt).NotTo(BeNil())
			Expect(t.Error).To(Equal("provider stream ruptured"))
		})

		It("releases the per-session conflict gate after Fail", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Fail(id, errors.New("x"))).To(Succeed())

			_, err = reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns ErrTurnTerminal when Complete already fired", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id, turn.ModelInfo{})).To(Succeed())

			err = reg.Fail(id, errors.New("x"))
			Expect(err).To(MatchError(turn.ErrTurnTerminal),
				"a turn that has already Completed cannot be Failed — terminal states are absorbing")
		})
	})

	Context("Get", func() {
		It("returns ErrTurnNotFound for an unknown turn id", func() {
			_, err := reg.Get("never-minted")
			Expect(err).To(MatchError(turn.ErrTurnNotFound))
		})

		It("returns a value-typed snapshot — mutating it does not affect the registry", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "real"})).To(Succeed())

			snap, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			// Mutate the returned slice.
			snap.MessagesAdded = append(snap.MessagesAdded, session.Message{Content: "MUTATED"})

			// The registry's copy must be untouched.
			again, _ := reg.Get(id)
			Expect(again.MessagesAdded).To(HaveLen(1))
			Expect(again.MessagesAdded[0].Content).To(Equal("real"))
		})
	})
})

var _ = Describe("Context propagation", func() {
	Context("WithTurnID then TurnIDFromContext", func() {
		It("round-trips the turn id through the engine ctx", func() {
			ctx := turn.WithTurnID(context.Background(), "turn-abc")
			id, ok := turn.TurnIDFromContext(ctx)
			Expect(ok).To(BeTrue())
			Expect(id).To(Equal("turn-abc"))
		})

		It("returns ok=false when no turn id was set", func() {
			id, ok := turn.TurnIDFromContext(context.Background())
			Expect(ok).To(BeFalse())
			Expect(id).To(BeEmpty())
		})

		It("treats an empty stored id as absent (ok=false)", func() {
			ctx := turn.WithTurnID(context.Background(), "")
			id, ok := turn.TurnIDFromContext(ctx)
			Expect(ok).To(BeFalse(),
				"an empty turn_id is functionally equivalent to no value — every Append site already nil-checks via ok")
			Expect(id).To(BeEmpty())
		})

		It("is nil-safe (defensive — engine call sites that lose the ctx must not crash)", func() {
			//nolint:staticcheck // intentional nil-ctx probe — guards against engine-side regressions
			id, ok := turn.TurnIDFromContext(nil)
			Expect(ok).To(BeFalse())
			Expect(id).To(BeEmpty())
		})
	})
})
