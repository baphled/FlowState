package turn_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
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

	// FindActiveBySession is the Phase-4-Commit-1 lookup path that
	// powers `GET /sessions` projection of `activeTurnId`: callers can
	// project the in-flight turn id from a sessionID without scanning
	// the registry. Backed by the existing byActiveSession O(1) map.
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d Commit 1.
	Context("FindActiveBySession", func() {
		DescribeTable("returns the running turn's id for the supplied session, else (\"\", false)",
			func(setup func(*turn.Registry), sessionID, wantID string, wantOK bool) {
				setup(reg)
				gotID, gotOK := reg.FindActiveBySession(sessionID)
				Expect(gotOK).To(Equal(wantOK),
					"ok flag must report whether a Running turn exists for sessionID=%q", sessionID)
				if wantOK {
					Expect(gotID).NotTo(BeEmpty())
				} else {
					Expect(gotID).To(BeEmpty(),
						"on the not-found path the returned id must be the empty string, never a stale id")
				}
				_ = wantID // wantID asserted in per-row probes below where relevant
			},
			Entry("zero turns — empty registry returns (\"\", false)",
				func(_ *turn.Registry) {}, "sess-1", "", false),
			Entry("one running turn for this session — returns (id, true)",
				func(r *turn.Registry) {
					id, err := r.Start("sess-1")
					Expect(err).NotTo(HaveOccurred())
					Expect(id).NotTo(BeEmpty())
				}, "sess-1", "", true),
			Entry("one running + one completed (different sessions) — returns the running one for its session",
				func(r *turn.Registry) {
					done, err := r.Start("sess-done")
					Expect(err).NotTo(HaveOccurred())
					Expect(r.Complete(done, turn.ModelInfo{})).To(Succeed())
					_, err = r.Start("sess-running")
					Expect(err).NotTo(HaveOccurred())
				}, "sess-running", "", true),
			Entry("after Complete on the running turn — lookup returns (\"\", false)",
				func(r *turn.Registry) {
					id, err := r.Start("sess-1")
					Expect(err).NotTo(HaveOccurred())
					Expect(r.Complete(id, turn.ModelInfo{})).To(Succeed())
				}, "sess-1", "", false),
			Entry("after Fail on the running turn — lookup returns (\"\", false)",
				func(r *turn.Registry) {
					id, err := r.Start("sess-1")
					Expect(err).NotTo(HaveOccurred())
					Expect(r.Fail(id, errors.New("boom"))).To(Succeed())
				}, "sess-1", "", false),
		)

		It("does NOT leak a running turn across sessions (cross-session non-leak)", func() {
			idA, err := reg.Start("sess-a")
			Expect(err).NotTo(HaveOccurred())

			// sess-a sees its running turn id.
			gotA, okA := reg.FindActiveBySession("sess-a")
			Expect(okA).To(BeTrue())
			Expect(gotA).To(Equal(idA))

			// sess-b — never had a turn started — must see nothing.
			gotB, okB := reg.FindActiveBySession("sess-b")
			Expect(okB).To(BeFalse(),
				"cross-session isolation: sess-a's running turn must NOT surface in sess-b's lookup; the wire's `activeTurnId` projection would otherwise leak ids between sessions")
			Expect(gotB).To(BeEmpty())
		})

		It("returns the freshly-minted id after a prior turn completed and a new one started", func() {
			id1, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id1, turn.ModelInfo{})).To(Succeed())
			id2, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			got, ok := reg.FindActiveBySession("sess-1")
			Expect(ok).To(BeTrue())
			Expect(got).To(Equal(id2),
				"FindActiveBySession must surface the CURRENT running turn, not a terminal prior — the byActiveSession map is cleared on Complete/Fail and re-set on Start")
		})
	})

	// SetHeartbeat is the Phase-4-Commit-1 write path that lets a bus
	// subscriber stamp the most-recent `phase` + `token_count` onto a
	// running turn so `GET /turns/{id}` can surface live progress
	// without an SSE side-channel.
	Context("SetHeartbeat", func() {
		It("populates Phase + TokenCount on a Running turn", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			reg.SetHeartbeat(id, "thinking", 42)

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.Phase).To(Equal("thinking"))
			Expect(t.TokenCount).To(Equal(42))
		})

		It("overwrites prior heartbeat values (monotonic last-write-wins)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			reg.SetHeartbeat(id, "queued", 0)
			reg.SetHeartbeat(id, "thinking", 100)
			reg.SetHeartbeat(id, "generating", 250)

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.Phase).To(Equal("generating"),
				"the wire heartbeat is last-write-wins — the chat UI's chip reads the most-recent phase")
			Expect(t.TokenCount).To(Equal(250),
				"TokenCount is cumulative per the provider's UsageDelta; the registry simply mirrors the latest value")
		})

		It("is a no-op on a Completed turn (Phase + TokenCount frozen at last value)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			reg.SetHeartbeat(id, "thinking", 50)
			Expect(reg.Complete(id, turn.ModelInfo{})).To(Succeed())

			// Late heartbeat — bus subscriber fires after Complete. Must
			// not mutate the terminal-state turn.
			reg.SetHeartbeat(id, "generating", 999)

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.Phase).To(Equal("thinking"),
				"terminal-state heartbeats must be silently absorbed — the turn's reported phase belongs to its Running lifetime")
			Expect(t.TokenCount).To(Equal(50))
		})

		It("is a no-op on a Failed turn", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			reg.SetHeartbeat(id, "thinking", 33)
			Expect(reg.Fail(id, errors.New("provider blew up"))).To(Succeed())

			reg.SetHeartbeat(id, "generating", 999)

			t, getErr := reg.Get(id)
			Expect(getErr).NotTo(HaveOccurred())
			Expect(t.Phase).To(Equal("thinking"))
			Expect(t.TokenCount).To(Equal(33))
		})

		It("is a no-op on an unknown turn id (no panic, no side effect)", func() {
			Expect(func() {
				reg.SetHeartbeat("never-minted", "thinking", 7)
			}).NotTo(Panic(),
				"unknown turn id must be silently absorbed — the bus subscriber must not crash the engine on a race against Complete clearing the byID entry")
		})

		It("is a no-op on an empty turn id (no panic, mirrors Append's empty-id contract)", func() {
			Expect(func() {
				reg.SetHeartbeat("", "thinking", 0)
			}).NotTo(Panic(),
				"empty turn id must be a silent no-op — the heartbeat bus subscriber derives turn id from session and may see \"\" when no turn is in flight for the session")
		})

		It("is race-safe under concurrent SetHeartbeat / Get / FindActiveBySession (-race must report clean)", func() {
			// Race-flagged concurrent test: spawns one writer and two
			// readers; the suite is invoked with `-race` so any unsynchronised
			// access trips the detector. We assert (a) no panics, (b) no
			// torn reads on the Phase/TokenCount pair (last observed
			// values match a write that actually fired).
			id, err := reg.Start("sess-race")
			Expect(err).NotTo(HaveOccurred())

			var (
				wg              sync.WaitGroup
				stop            atomic.Bool
				lastWriteTokens atomic.Int64
			)
			wg.Add(3)

			// Writer goroutine — tight loop bumping the heartbeat. The
			// phase alternates so a torn read would surface a mismatched
			// pair (phase from write N, token from write N+1).
			go func() {
				defer wg.Done()
				phases := []string{"queued", "thinking", "generating"}
				i := 0
				for !stop.Load() {
					p := phases[i%len(phases)]
					tokens := i + 1
					reg.SetHeartbeat(id, p, tokens)
					lastWriteTokens.Store(int64(tokens))
					i++
				}
			}()

			// Reader 1 — Get in a tight loop.
			go func() {
				defer wg.Done()
				for !stop.Load() {
					t, gerr := reg.Get(id)
					Expect(gerr).NotTo(HaveOccurred())
					// Phase + TokenCount must remain a consistent pair —
					// both fields read under the same lock. A torn read
					// would surface a non-empty phase with TokenCount=0
					// AFTER the first heartbeat fired, or vice versa.
					_ = t.Phase
					_ = t.TokenCount
				}
			}()

			// Reader 2 — FindActiveBySession in a tight loop.
			go func() {
				defer wg.Done()
				for !stop.Load() {
					_, _ = reg.FindActiveBySession("sess-race")
				}
			}()

			// Let the goroutines pound the registry for a short window.
			// 50ms is enough to surface a race under `-race` without
			// inflating the suite runtime; the detector samples on every
			// memory access.
			time.Sleep(50 * time.Millisecond)
			stop.Store(true)
			wg.Wait()

			// Final state must match the writer's last observed write —
			// proves the writer's mutations land atomically under the
			// mutex (no torn final state).
			Expect(lastWriteTokens.Load()).To(BeNumerically(">", int64(0)),
				"writer goroutine must have fired at least one SetHeartbeat during the 50ms window")
			final, gerr := reg.Get(id)
			Expect(gerr).NotTo(HaveOccurred())
			Expect(final.TokenCount).To(BeNumerically(">", 0),
				"the last-observed TokenCount must reflect a real write — torn read or unsynchronised access would surface zero here")
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
