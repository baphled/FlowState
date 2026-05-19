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

	// WaitForChange is the Phase-4-Commit-1b long-poll primitive: callers
	// snapshot a Turn under lock, capture the baseline (messages-count,
	// phase, token-count) they observed, then call WaitForChange with
	// those baselines + a timeout. WaitForChange returns either (a) when
	// any of the watched fields move past the baseline, (b) when the Turn
	// transitions to a terminal state, (c) when the timeout elapses, or
	// (d) when the caller's context is cancelled (handler-side client
	// disconnect propagation). Returns the fresh value-typed snapshot in
	// every non-error case.
	//
	// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
	//   Turn-Based Post-Then-Poll Architecture (May 2026).md §4d Commit 1b.
	Context("WaitForChange", func() {
		It("returns immediately when MessagesAdded grew past the baseline before the call", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			// Append before the wait — the registry already has 1 row,
			// so a wait with sinceMsgCount=0 must return without
			// blocking.
			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "early"})).To(Succeed())

			start := time.Now()
			snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second)
			elapsed := time.Since(start)

			Expect(changed).To(BeTrue(),
				"WaitForChange must report changed=true when MessagesAdded > sinceMsgCount at call time — the caller's last-seen count is stale")
			Expect(snap.MessagesAdded).To(HaveLen(1))
			Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond),
				"the call must return synchronously when the baseline is already exceeded — no sleeping until timeout")
		})

		It("returns immediately when the Turn is already in a terminal state at call time", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			Expect(reg.Complete(id, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

			start := time.Now()
			snap, changed := reg.WaitForChange(context.Background(), id, 999, "", 0, "", "", nil, nil, 5*time.Second)
			elapsed := time.Since(start)

			Expect(changed).To(BeTrue(),
				"a terminal-state Turn must surface changed=true so the caller observes the final snapshot without hanging — even though MessagesAdded did not grow past the (impossibly-large) baseline")
			Expect(snap.Status).To(Equal(turn.StatusCompleted))
			Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond),
				"a completed-before-wait Turn must NOT block — the caller's poll loop would then idle for the full timeout for no reason")
		})

		It("returns when Append fires DURING the wait (mutation broadcast)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			// Spawn the wait in a goroutine — the producer fires Append
			// from another goroutine after a short delay; WaitForChange
			// must wake within a few ms of the broadcast.
			type result struct {
				snap    turn.Turn
				changed bool
				elapsed time.Duration
			}
			done := make(chan result, 1)
			go func() {
				start := time.Now()
				snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second)
				done <- result{snap: snap, changed: changed, elapsed: time.Since(start)}
			}()

			// Give the wait a moment to settle on its select; then fire
			// the broadcast. 20ms is enough for the goroutine to be
			// parked on the channel without inflating the test runtime.
			time.Sleep(20 * time.Millisecond)
			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "live"})).To(Succeed())

			var r result
			Eventually(done, "2s").Should(Receive(&r))
			Expect(r.changed).To(BeTrue())
			Expect(r.snap.MessagesAdded).To(HaveLen(1))
			Expect(r.snap.MessagesAdded[0].Content).To(Equal("live"))
			Expect(r.elapsed).To(BeNumerically("<", 200*time.Millisecond),
				"the wake must arrive within a few ms of Append's broadcast — long-poll's perceived-latency promise is sub-50ms after chunk arrival; we allow 200ms slack for scheduler jitter on a loaded CI box")
			// Surface the actual wake latency on the test report so the
			// Phase-4-Commit-1b commit message can quote a concrete
			// sub-50ms post-broadcast latency rather than just "passed".
			// The 20ms above is "time-to-park"; the rest is the wake
			// + return cost from the broadcast.
			AddReportEntry("perceived_wake_latency_phase4_commit1b",
				ReportEntryVisibilityAlways,
				map[string]any{
					"total_elapsed_from_wait_start": r.elapsed.String(),
					"approx_post_broadcast_wake":    (r.elapsed - 20*time.Millisecond).String(),
					"target":                        "<50ms post-broadcast",
				})
		})

		It("returns when SetHeartbeat changes Phase or TokenCount DURING the wait", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			type result struct {
				snap    turn.Turn
				changed bool
			}
			done := make(chan result, 1)
			go func() {
				snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second)
				done <- result{snap: snap, changed: changed}
			}()

			time.Sleep(20 * time.Millisecond)
			reg.SetHeartbeat(id, "thinking", 42)

			var r result
			Eventually(done, "2s").Should(Receive(&r))
			Expect(r.changed).To(BeTrue())
			Expect(r.snap.Phase).To(Equal("thinking"))
			Expect(r.snap.TokenCount).To(Equal(42))
		})

		It("returns when Complete fires DURING the wait (terminal transition)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			type result struct {
				snap    turn.Turn
				changed bool
			}
			done := make(chan result, 1)
			go func() {
				snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second)
				done <- result{snap: snap, changed: changed}
			}()

			time.Sleep(20 * time.Millisecond)
			Expect(reg.Complete(id, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

			var r result
			Eventually(done, "2s").Should(Receive(&r))
			Expect(r.changed).To(BeTrue())
			Expect(r.snap.Status).To(Equal(turn.StatusCompleted))
		})

		It("returns when Fail fires DURING the wait (terminal transition)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			type result struct {
				snap    turn.Turn
				changed bool
			}
			done := make(chan result, 1)
			go func() {
				snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second)
				done <- result{snap: snap, changed: changed}
			}()

			time.Sleep(20 * time.Millisecond)
			Expect(reg.Fail(id, errors.New("provider ruptured"))).To(Succeed())

			var r result
			Eventually(done, "2s").Should(Receive(&r))
			Expect(r.changed).To(BeTrue())
			Expect(r.snap.Status).To(Equal(turn.StatusFailed))
			Expect(r.snap.Error).To(Equal("provider ruptured"))
		})

		It("returns with changed=false when the timeout elapses without any mutation", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			// Short timeout — the wait MUST return within the timeout
			// budget even though no producer ever fires. The caller
			// re-issues to start a fresh wait.
			start := time.Now()
			snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 80*time.Millisecond)
			elapsed := time.Since(start)

			Expect(changed).To(BeFalse(),
				"the timeout path must surface changed=false so the long-poll caller knows nothing actually moved — idempotent re-issue is the next step")
			Expect(snap.Status).To(Equal(turn.StatusRunning),
				"the snapshot returned on timeout must still be the live Turn — the long-poll handler always writes a body, even on the timeout path")
			Expect(elapsed).To(BeNumerically(">=", 80*time.Millisecond))
			Expect(elapsed).To(BeNumerically("<", 300*time.Millisecond),
				"the timeout must fire within a comfortable slack of the requested 80ms — anything past 300ms suggests a spinning or oversleeping wait loop")
		})

		It("returns when the caller's context is cancelled DURING the wait (client disconnect)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			ctx, cancel := context.WithCancel(context.Background())
			type result struct {
				changed bool
				elapsed time.Duration
			}
			done := make(chan result, 1)
			go func() {
				start := time.Now()
				_, changed := reg.WaitForChange(ctx, id, 0, "", 0, "", "", nil, nil, 5*time.Second)
				done <- result{changed: changed, elapsed: time.Since(start)}
			}()

			// Settle the wait, then cancel — the wait must wake promptly
			// off the ctx.Done() branch.
			time.Sleep(20 * time.Millisecond)
			cancel()

			var r result
			Eventually(done, "2s").Should(Receive(&r))
			Expect(r.changed).To(BeFalse(),
				"a ctx-cancelled wait must surface changed=false — the wire-side handler then exits without writing a body; the client is gone")
			Expect(r.elapsed).To(BeNumerically("<", 300*time.Millisecond),
				"the wake on ctx-cancel must be prompt — anything past 300ms suggests the select is not watching ctx.Done()")
		})

		It("returns immediately with the zero snapshot when turnID is unknown", func() {
			// Unknown turnID — the wait must NOT block. Surfaces
			// changed=false + zero snapshot so the handler can map this
			// to a 404 / not-found path.
			start := time.Now()
			snap, changed := reg.WaitForChange(context.Background(), "never-minted", 0, "", 0, "", "", nil, nil, 5*time.Second)
			elapsed := time.Since(start)

			Expect(changed).To(BeFalse())
			Expect(snap.ID).To(BeEmpty(),
				"an unknown turn id must surface a zero-valued snapshot — the handler reads ID==\"\" as the not-found discriminant")
			Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond),
				"a not-found wait must NOT hang for the timeout — the registry knows the id is unknown synchronously")
		})

		It("returns immediately when Phase already moved past the baseline before the call", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			reg.SetHeartbeat(id, "generating", 7)

			start := time.Now()
			snap, changed := reg.WaitForChange(context.Background(), id, 0, "thinking", 7, "", "", nil, nil, 5*time.Second)
			elapsed := time.Since(start)

			Expect(changed).To(BeTrue(),
				"Phase moved past the lastPhase baseline — the wait must surface changed=true synchronously")
			Expect(snap.Phase).To(Equal("generating"))
			Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond))
		})

		It("returns immediately when TokenCount already moved past the baseline before the call", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())
			reg.SetHeartbeat(id, "thinking", 100)

			start := time.Now()
			snap, changed := reg.WaitForChange(context.Background(), id, 0, "thinking", 50, "", "", nil, nil, 5*time.Second)
			elapsed := time.Since(start)

			Expect(changed).To(BeTrue(),
				"TokenCount moved past the lastTokens baseline — the wait must surface changed=true synchronously")
			Expect(snap.TokenCount).To(Equal(100))
			Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond))
		})

		It("is race-safe under concurrent waiters + mutations (-race must report clean)", func() {
			// Race-flagged: spawn N waiters against the same Turn while
			// a writer pounds Append + SetHeartbeat. -race must report
			// clean; every waiter must wake within the budget.
			id, err := reg.Start("sess-race")
			Expect(err).NotTo(HaveOccurred())

			const waiters = 8
			var wg sync.WaitGroup
			wakes := make(chan struct{}, waiters)
			wg.Add(waiters)
			for i := 0; i < waiters; i++ {
				go func() {
					defer wg.Done()
					_, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 2*time.Second)
					if changed {
						wakes <- struct{}{}
					}
				}()
			}

			// Writer — fires a single Append after a short delay; that
			// single broadcast must wake every waiting goroutine.
			time.Sleep(20 * time.Millisecond)
			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "broadcast"})).To(Succeed())

			wg.Wait()
			Expect(len(wakes)).To(Equal(waiters),
				"a single mutation broadcast must wake EVERY concurrent waiter — the channel-of-zero-values close pattern must broadcast, not signal one waiter")
		})

		// SetProviderModel populates CurrentProvider + CurrentModel onto a
		// Running Turn so the long-poll wire surfaces the (provider, model)
		// pair the engine is currently streaming under — distinct from
		// Turn.Model which is the post-Complete frozen snapshot. The
		// dispatcher's wrap goroutine taps `provider_changed` and
		// `model_active` chunks and calls this method so a mid-stream
		// failover surfaces on the next poll without waiting for the
		// terminal Complete to fire.
		//
		// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
		//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-α.
		Context("SetProviderModel (Phase-5 §1c-α)", func() {
			It("populates CurrentProvider + CurrentModel on a Running turn", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")

				t, getErr := reg.Get(id)
				Expect(getErr).NotTo(HaveOccurred())
				Expect(t.CurrentProvider).To(Equal("anthropic"))
				Expect(t.CurrentModel).To(Equal("claude-opus-4-7"))
			})

			It("overwrites prior values across calls (mid-stream failover lands on the latest pair)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")
				reg.SetProviderModel(id, "zai", "glm-4.6")

				t, _ := reg.Get(id)
				Expect(t.CurrentProvider).To(Equal("zai"),
					"failover: the most recent (provider, model) pair must overwrite the prior — clients reading CurrentProvider see the active provider, not the original")
				Expect(t.CurrentModel).To(Equal("glm-4.6"))
			})

			It("broadcasts changeCh so long-poll waiters wake on a real transition", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				type result struct {
					snap    turn.Turn
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					snap, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second)
					done <- result{snap: snap, changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeTrue(),
					"a SetProviderModel call on a transition (empty → real pair) MUST broadcast — the FE's long-poll wakes off this channel to learn the new model without polling on a tight loop")
				Expect(r.snap.CurrentProvider).To(Equal("anthropic"))
				Expect(r.snap.CurrentModel).To(Equal("claude-opus-4-7"))
			})

			It("does NOT broadcast when the pair is unchanged (spurious tap absorbed)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				// Seed the registry with the pair so the second call is a no-op.
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")

				type result struct {
					changed bool
					elapsed time.Duration
				}
				done := make(chan result, 1)
				go func() {
					start := time.Now()
					_, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"anthropic", "claude-opus-4-7", nil, nil, 80*time.Millisecond,
					)
					done <- result{changed: changed, elapsed: time.Since(start)}
				}()

				// Fire two no-op SetProviderModel calls during the wait. If
				// the registry broadcast on every call (instead of gating on
				// actual change), the wait would wake with changed=true
				// against the matched baseline. The correct semantics: the
				// wait should time out.
				time.Sleep(20 * time.Millisecond)
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeFalse(),
					"a SetProviderModel call that doesn't actually move the pair must NOT broadcast — every chunk in a long stream carries provider/model, so an unconditional broadcast would degrade the long-poll's perceived-cadence promise to spin")
			})

			It("is a no-op on a Completed turn (CurrentProvider + CurrentModel frozen at last value)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")
				Expect(reg.Complete(id, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

				// Late tap — wrap goroutine's chunk drain post-Complete (a
				// race shape the registry must absorb silently).
				reg.SetProviderModel(id, "zai", "glm-4.6")

				t, _ := reg.Get(id)
				Expect(t.CurrentProvider).To(Equal("anthropic"),
					"terminal-state taps must be silently absorbed — the live (provider, model) pair belongs to the Running lifetime")
				Expect(t.CurrentModel).To(Equal("claude-opus-4-7"))
			})

			It("is a no-op on a Failed turn", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")
				Expect(reg.Fail(id, errors.New("boom"))).To(Succeed())

				reg.SetProviderModel(id, "zai", "glm-4.6")

				t, _ := reg.Get(id)
				Expect(t.CurrentProvider).To(Equal("anthropic"))
				Expect(t.CurrentModel).To(Equal("claude-opus-4-7"))
			})

			It("is a no-op on an unknown turn id (no panic)", func() {
				Expect(func() {
					reg.SetProviderModel("never-minted", "anthropic", "claude-opus-4-7")
				}).NotTo(Panic())
			})

			It("is a no-op on an empty turn id (mirrors Append's contract)", func() {
				Expect(func() {
					reg.SetProviderModel("", "anthropic", "claude-opus-4-7")
				}).NotTo(Panic())
			})

			It("is race-safe under concurrent SetProviderModel + Get + WaitForChange (-race must report clean)", func() {
				id, err := reg.Start("sess-race")
				Expect(err).NotTo(HaveOccurred())

				var (
					wg   sync.WaitGroup
					stop atomic.Bool
				)
				wg.Add(2)

				// Writer goroutine — alternating pairs so a torn read would
				// surface a (anthropic, glm-4.6) mismatched pair.
				go func() {
					defer wg.Done()
					pairs := [][2]string{
						{"anthropic", "claude-opus-4-7"},
						{"zai", "glm-4.6"},
					}
					i := 0
					for !stop.Load() {
						p := pairs[i%len(pairs)]
						reg.SetProviderModel(id, p[0], p[1])
						i++
					}
				}()

				// Reader goroutine — tight Get loop. Verifies the (Provider,
				// Model) pair is read atomically under the same mutex.
				go func() {
					defer wg.Done()
					for !stop.Load() {
						t, _ := reg.Get(id)
						// A torn read would surface e.g. CurrentProvider=zai
						// with CurrentModel=claude-opus-4-7 — the assertion
						// below would fail. Pinned pairs verify atomicity.
						if t.CurrentProvider == "anthropic" {
							Expect(t.CurrentModel).To(Or(Equal("claude-opus-4-7"), Equal("")),
								"torn read — provider=anthropic must be paired with claude-opus-4-7 or empty (initial state); got %q", t.CurrentModel)
						}
						if t.CurrentProvider == "zai" {
							Expect(t.CurrentModel).To(Equal("glm-4.6"),
								"torn read — provider=zai must be paired with glm-4.6; got %q", t.CurrentModel)
						}
					}
				}()

				time.Sleep(50 * time.Millisecond)
				stop.Store(true)
				wg.Wait()
			})
		})

		// WaitForChange — Phase-5 §1c-α extension: the predicate now also
		// wakes on CurrentProvider / CurrentModel transitions past the
		// caller's baseline. Mirrors the existing Phase / TokenCount
		// baseline contract; the test below pins both immediate-return and
		// during-wait wake paths.
		Context("WaitForChange (Phase-5 §1c-α — provider/model baseline)", func() {
			It("returns immediately when CurrentProvider already moved past the baseline before the call", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetProviderModel(id, "zai", "glm-4.6")

				start := time.Now()
				snap, changed := reg.WaitForChange(
					context.Background(), id, 0, "", 0,
					"anthropic", "claude-opus-4-7", nil, nil, 5*time.Second,
				)
				elapsed := time.Since(start)

				Expect(changed).To(BeTrue(),
					"CurrentProvider moved past the lastProvider baseline — the wait must surface changed=true synchronously")
				Expect(snap.CurrentProvider).To(Equal("zai"))
				Expect(snap.CurrentModel).To(Equal("glm-4.6"))
				Expect(elapsed).To(BeNumerically("<", 50*time.Millisecond))
			})

			It("returns immediately when CurrentModel moved past the baseline (same provider, model swap)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")

				snap, changed := reg.WaitForChange(
					context.Background(), id, 0, "", 0,
					"anthropic", "claude-sonnet-4-6", nil, nil, 5*time.Second,
				)
				Expect(changed).To(BeTrue(),
					"CurrentModel moved past lastModel — even with provider unchanged the wait must wake (e.g. anthropic Opus → Sonnet switch within the same provider)")
				Expect(snap.CurrentModel).To(Equal("claude-opus-4-7"))
			})

			It("returns when SetProviderModel changes the pair DURING the wait", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetProviderModel(id, "anthropic", "claude-opus-4-7")

				type result struct {
					snap    turn.Turn
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					snap, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"anthropic", "claude-opus-4-7", nil, nil, 5*time.Second,
					)
					done <- result{snap: snap, changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.SetProviderModel(id, "zai", "glm-4.6")

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeTrue())
				Expect(r.snap.CurrentProvider).To(Equal("zai"))
				Expect(r.snap.CurrentModel).To(Equal("glm-4.6"))
			})
		})

		// SetContextUsage records the most-recent context_usage payload onto
		// a Running Turn. Wired off the dispatcher's wrapWithTurnLifecycle
		// chunk-tap on `context_usage` chunks so the long-poll wire surfaces
		// the live figure without an SSE side-channel.
		//
		// Plan ref: ~/vaults/baphled/1. Projects/FlowState/Plans/
		//   Phase-5 Turn-Endpoint Event-Type Parity (May 2026).md §1c-β.
		Context("SetContextUsage (Phase-5 §1c-β)", func() {
			It("populates ContextUsage on a Running turn", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				cu := &turn.ContextUsage{
					InputTokens:   1234,
					OutputReserve: 8192,
					Limit:         200000,
					Percentage:    1,
					Provider:      "anthropic",
					Model:         "claude-opus-4-7",
				}
				reg.SetContextUsage(id, cu)

				t, getErr := reg.Get(id)
				Expect(getErr).NotTo(HaveOccurred())
				Expect(t.ContextUsage).NotTo(BeNil(),
					"ContextUsage must be populated on the Turn after a SetContextUsage call — the long-poll wire reads this off the Turn")
				Expect(t.ContextUsage.InputTokens).To(Equal(1234))
				Expect(t.ContextUsage.Limit).To(Equal(200000))
				Expect(t.ContextUsage.Provider).To(Equal("anthropic"))
				Expect(t.ContextUsage.Model).To(Equal("claude-opus-4-7"))
			})

			It("overwrites prior values across calls (mid-stream growth lands on the latest figure)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1000, Limit: 200000, Percentage: 0, Provider: "anthropic", Model: "claude-opus-4-7"})
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 5000, Limit: 200000, Percentage: 2, Provider: "anthropic", Model: "claude-opus-4-7"})

				t, _ := reg.Get(id)
				Expect(t.ContextUsage.InputTokens).To(Equal(5000),
					"each new context_usage chunk overwrites the prior — the chip ticks up monotonically as the prompt grows")
			})

			It("broadcasts changeCh so long-poll waiters wake on a real transition", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				type result struct {
					snap    turn.Turn
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					snap, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0, "", "", nil, nil, 5*time.Second,
					)
					done <- result{snap: snap, changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Percentage: 1, Provider: "anthropic", Model: "claude-opus-4-7"})

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeTrue(),
					"a SetContextUsage call on a transition (nil → real figure) MUST broadcast — the FE's long-poll wakes off this channel to learn the new figure")
				Expect(r.snap.ContextUsage).NotTo(BeNil())
				Expect(r.snap.ContextUsage.InputTokens).To(Equal(1234))
			})

			It("does NOT broadcast when the figure is unchanged (spurious tap absorbed)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				// Seed the registry with the figure so the second call is a no-op.
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Percentage: 1, Provider: "anthropic", Model: "claude-opus-4-7"})

				type result struct {
					changed bool
				}
				done := make(chan result, 1)
				baseline := &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Percentage: 1, Provider: "anthropic", Model: "claude-opus-4-7"}
				go func() {
					_, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"", "", baseline, nil, 80*time.Millisecond,
					)
					done <- result{changed: changed}
				}()

				// Fire two no-op SetContextUsage calls during the wait. If the
				// registry broadcast on every call (instead of gating on actual
				// change), the wait would wake with changed=true against the
				// matched baseline. Correct semantics: wait should time out.
				time.Sleep(20 * time.Millisecond)
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Percentage: 1, Provider: "anthropic", Model: "claude-opus-4-7"})
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Percentage: 1, Provider: "anthropic", Model: "claude-opus-4-7"})

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeFalse(),
					"a SetContextUsage call that doesn't actually move the figure must NOT broadcast — every chunk could carry the same figure, and an unconditional broadcast would degrade the long-poll's perceived-cadence promise to spin")
			})

			It("is a no-op on a Completed turn (ContextUsage frozen at last value)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Percentage: 1, Provider: "anthropic", Model: "claude-opus-4-7"})
				Expect(reg.Complete(id, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

				// Late tap — wrap goroutine's chunk drain post-Complete (a
				// race the registry must absorb silently).
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 9999, Limit: 200000, Percentage: 4, Provider: "anthropic", Model: "claude-opus-4-7"})

				t, _ := reg.Get(id)
				Expect(t.ContextUsage.InputTokens).To(Equal(1234),
					"terminal-state taps must be silently absorbed — the live figure belongs to the Running lifetime")
			})

			It("is a no-op on an unknown turn id (no panic)", func() {
				Expect(func() {
					reg.SetContextUsage("never-minted", &turn.ContextUsage{InputTokens: 1, Limit: 100, Provider: "x", Model: "y"})
				}).NotTo(Panic())
			})

			It("is a no-op on an empty turn id (mirrors Append's contract)", func() {
				Expect(func() {
					reg.SetContextUsage("", &turn.ContextUsage{InputTokens: 1, Limit: 100, Provider: "x", Model: "y"})
				}).NotTo(Panic())
			})

			It("is a no-op on a nil cu pointer (defensive — chunk-tap parse failure surface)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				Expect(func() {
					reg.SetContextUsage(id, nil)
				}).NotTo(Panic())
				t, _ := reg.Get(id)
				Expect(t.ContextUsage).To(BeNil(),
					"nil cu must NOT mutate the stored figure — a parse failure at the tap site must absorb silently")
			})

			It("is race-safe under concurrent SetContextUsage + Get + WaitForChange (-race must report clean)", func() {
				id, err := reg.Start("sess-race-cu")
				Expect(err).NotTo(HaveOccurred())

				var (
					wg   sync.WaitGroup
					stop atomic.Bool
				)
				wg.Add(2)

				go func() {
					defer wg.Done()
					i := 0
					for !stop.Load() {
						reg.SetContextUsage(id, &turn.ContextUsage{
							InputTokens: i, Limit: 200000, Percentage: 0,
							Provider: "anthropic", Model: "claude-opus-4-7",
						})
						i++
					}
				}()

				go func() {
					defer wg.Done()
					for !stop.Load() {
						_, _ = reg.Get(id)
					}
				}()

				time.Sleep(50 * time.Millisecond)
				stop.Store(true)
				wg.Wait()
			})
		})

		// UpsertProviderQuota records a provider_quota snapshot onto a
		// Running Turn's ProviderQuotas slice with partition-key dedup
		// semantics (Option B). Each partition's most-recent payload wins.
		// Plan ref: Phase-5 §1c-β.
		Context("UpsertProviderQuota (Phase-5 §1c-β)", func() {
			seedSnap := func(provider, model string, spent int64) turn.ProviderQuotaSnapshot {
				return turn.ProviderQuotaSnapshot{
					Provider:    provider,
					AccountHash: "acc-hash-1",
					Model:       model,
					ObservedAt:  "2026-05-19T00:00:00Z",
					Variant:     "token_spend",
					TokenSpend: &turn.ProviderQuotaTokenSpend{
						SpentMinor:    spent,
						SpentCurrency: "USD",
						Period:        "monthly",
					},
				}
			}

			It("appends the first snapshot onto an empty ProviderQuotas slice", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))

				t, getErr := reg.Get(id)
				Expect(getErr).NotTo(HaveOccurred())
				Expect(t.ProviderQuotas).To(HaveLen(1))
				Expect(t.ProviderQuotas[0].Provider).To(Equal("anthropic"))
				Expect(t.ProviderQuotas[0].TokenSpend.SpentMinor).To(Equal(int64(1000)))
			})

			It("REPLACES a snapshot with the same partition key (Provider:AccountHash:Model) — no duplicate appends", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))
				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 2500))

				t, _ := reg.Get(id)
				Expect(t.ProviderQuotas).To(HaveLen(1),
					"same partition key must REPLACE not APPEND — the FE quotaStore's snapshots map is keyed by partition, and a duplicated slice entry would surface mixed historical figures")
				Expect(t.ProviderQuotas[0].TokenSpend.SpentMinor).To(Equal(int64(2500)),
					"newest wins — the registry tracks the most recent quota figure per partition, not a history")
			})

			It("APPENDS a snapshot with a different partition key (different provider OR model OR account_hash)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))
				reg.UpsertProviderQuota(id, seedSnap("zai", "glm-4.6", 500))
				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-sonnet-4-6", 100))

				t, _ := reg.Get(id)
				Expect(t.ProviderQuotas).To(HaveLen(3),
					"different partition keys MUST append — the chip renders one row per (provider, account_hash, model) tuple")
			})

			It("does NOT broadcast when the partition's snapshot is unchanged (spurious tap absorbed)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				// Seed the registry with the snapshot so the second call is a no-op.
				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))

				baseline := []turn.ProviderQuotaSnapshot{seedSnap("anthropic", "claude-opus-4-7", 1000)}

				type result struct {
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					_, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"", "", nil, baseline, 80*time.Millisecond,
					)
					done <- result{changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))
				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeFalse(),
					"a no-op UpsertProviderQuota call must NOT broadcast — the engine emits provider_quota pre-reply AND post-turn at the same cadence as context_usage, and an unconditional broadcast would degrade the long-poll's perceived-cadence promise to spin")
			})

			It("broadcasts when a new partition key appears (FE diff loop must observe append)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))

				baseline := []turn.ProviderQuotaSnapshot{seedSnap("anthropic", "claude-opus-4-7", 1000)}

				type result struct {
					snap    turn.Turn
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					snap, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"", "", nil, baseline, 5*time.Second,
					)
					done <- result{snap: snap, changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.UpsertProviderQuota(id, seedSnap("zai", "glm-4.6", 500))

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeTrue(),
					"a new partition key MUST broadcast — the FE quotaStore's per-partition diff fires applyProviderQuotaEvent once on append")
				Expect(r.snap.ProviderQuotas).To(HaveLen(2))
			})

			It("is a no-op on a Completed turn (ProviderQuotas frozen at last value)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 1000))
				Expect(reg.Complete(id, turn.ModelInfo{Provider: "anthropic", Model: "claude-opus-4-7"})).To(Succeed())

				reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", 9999))
				reg.UpsertProviderQuota(id, seedSnap("zai", "glm-4.6", 1))

				t, _ := reg.Get(id)
				Expect(t.ProviderQuotas).To(HaveLen(1),
					"terminal-state taps must NOT append fresh partitions — the live set belongs to the Running lifetime")
				Expect(t.ProviderQuotas[0].TokenSpend.SpentMinor).To(Equal(int64(1000)),
					"terminal-state taps must NOT mutate existing partitions either")
			})

			It("is a no-op on empty / unknown turn id", func() {
				snap := seedSnap("anthropic", "claude-opus-4-7", 1)
				Expect(func() { reg.UpsertProviderQuota("", snap) }).NotTo(Panic())
				Expect(func() { reg.UpsertProviderQuota("never-minted", snap) }).NotTo(Panic())
			})

			It("is race-safe under concurrent UpsertProviderQuota writers on different partition keys (-race must report clean)", func() {
				id, err := reg.Start("sess-race-quota")
				Expect(err).NotTo(HaveOccurred())

				var (
					wg   sync.WaitGroup
					stop atomic.Bool
				)
				wg.Add(3)

				// Writer 1 — anthropic partition.
				go func() {
					defer wg.Done()
					i := int64(0)
					for !stop.Load() {
						reg.UpsertProviderQuota(id, seedSnap("anthropic", "claude-opus-4-7", i))
						i++
					}
				}()
				// Writer 2 — zai partition.
				go func() {
					defer wg.Done()
					i := int64(0)
					for !stop.Load() {
						reg.UpsertProviderQuota(id, seedSnap("zai", "glm-4.6", i))
						i++
					}
				}()
				// Reader.
				go func() {
					defer wg.Done()
					for !stop.Load() {
						_, _ = reg.Get(id)
					}
				}()

				time.Sleep(50 * time.Millisecond)
				stop.Store(true)
				wg.Wait()

				t, _ := reg.Get(id)
				Expect(t.ProviderQuotas).To(HaveLen(2),
					"two distinct partitions must coexist without slice corruption — partition-key dedup must NOT collapse different keys")
			})
		})

		// WaitForChange — Phase-5 §1c-β extension: the predicate now also
		// wakes on ContextUsage / ProviderQuotas transitions past the
		// caller's baseline. Pins both immediate-return and during-wait wake.
		Context("WaitForChange (Phase-5 §1c-β — ContextUsage / ProviderQuotas baseline)", func() {
			It("returns immediately when ContextUsage moved past a nil baseline before the call", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Provider: "anthropic", Model: "claude-opus-4-7"})

				snap, changed := reg.WaitForChange(
					context.Background(), id, 0, "", 0,
					"", "", nil, nil, 5*time.Second,
				)
				Expect(changed).To(BeTrue(),
					"ContextUsage moved past the nil baseline — the wait must surface changed=true synchronously")
				Expect(snap.ContextUsage).NotTo(BeNil())
			})

			It("returns immediately when ContextUsage figure differs from a non-nil baseline", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 5000, Limit: 200000, Provider: "anthropic", Model: "claude-opus-4-7"})

				baseline := &turn.ContextUsage{InputTokens: 1234, Limit: 200000, Provider: "anthropic", Model: "claude-opus-4-7"}
				snap, changed := reg.WaitForChange(
					context.Background(), id, 0, "", 0,
					"", "", baseline, nil, 5*time.Second,
				)
				Expect(changed).To(BeTrue(),
					"ContextUsage's InputTokens moved — the wait must surface the new figure synchronously")
				Expect(snap.ContextUsage.InputTokens).To(Equal(5000))
			})

			It("returns immediately when ProviderQuotas length grew past the baseline", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				snap1 := turn.ProviderQuotaSnapshot{
					Provider: "anthropic", AccountHash: "h", Model: "claude-opus-4-7",
					Variant: "token_spend",
					TokenSpend: &turn.ProviderQuotaTokenSpend{
						SpentMinor: 100, SpentCurrency: "USD", Period: "monthly",
					},
				}
				reg.UpsertProviderQuota(id, snap1)

				snap, changed := reg.WaitForChange(
					context.Background(), id, 0, "", 0,
					"", "", nil, nil, 5*time.Second,
				)
				Expect(changed).To(BeTrue(),
					"a non-empty ProviderQuotas against an empty baseline must surface synchronously")
				Expect(snap.ProviderQuotas).To(HaveLen(1))
			})

			It("returns immediately when a ProviderQuotas entry differs from baseline (replace-in-place)", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())
				snap := turn.ProviderQuotaSnapshot{
					Provider: "anthropic", AccountHash: "h", Model: "claude-opus-4-7",
					Variant: "token_spend",
					TokenSpend: &turn.ProviderQuotaTokenSpend{
						SpentMinor: 500, SpentCurrency: "USD", Period: "monthly",
					},
				}
				reg.UpsertProviderQuota(id, snap)

				baseline := []turn.ProviderQuotaSnapshot{{
					Provider: "anthropic", AccountHash: "h", Model: "claude-opus-4-7",
					Variant: "token_spend",
					TokenSpend: &turn.ProviderQuotaTokenSpend{
						SpentMinor: 100, SpentCurrency: "USD", Period: "monthly",
					},
				}}
				out, changed := reg.WaitForChange(
					context.Background(), id, 0, "", 0,
					"", "", nil, baseline, 5*time.Second,
				)
				Expect(changed).To(BeTrue(),
					"a replace-in-place that changes the TokenSpend payload must surface — the FE diff loop pivots on per-partition value change, not just length")
				Expect(out.ProviderQuotas[0].TokenSpend.SpentMinor).To(Equal(int64(500)))
			})

			It("wakes during the wait when SetContextUsage fires", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				type result struct {
					snap    turn.Turn
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					snap, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"", "", nil, nil, 5*time.Second,
					)
					done <- result{snap: snap, changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.SetContextUsage(id, &turn.ContextUsage{InputTokens: 7777, Limit: 200000, Provider: "anthropic", Model: "claude-opus-4-7"})

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeTrue())
				Expect(r.snap.ContextUsage.InputTokens).To(Equal(7777))
			})

			It("wakes during the wait when UpsertProviderQuota fires a new partition", func() {
				id, err := reg.Start("sess-1")
				Expect(err).NotTo(HaveOccurred())

				type result struct {
					snap    turn.Turn
					changed bool
				}
				done := make(chan result, 1)
				go func() {
					snap, changed := reg.WaitForChange(
						context.Background(), id, 0, "", 0,
						"", "", nil, nil, 5*time.Second,
					)
					done <- result{snap: snap, changed: changed}
				}()

				time.Sleep(20 * time.Millisecond)
				reg.UpsertProviderQuota(id, turn.ProviderQuotaSnapshot{
					Provider: "anthropic", AccountHash: "h", Model: "claude-opus-4-7",
					Variant: "token_spend",
					TokenSpend: &turn.ProviderQuotaTokenSpend{
						SpentMinor: 1, SpentCurrency: "USD", Period: "monthly",
					},
				})

				var r result
				Eventually(done, "2s").Should(Receive(&r))
				Expect(r.changed).To(BeTrue())
				Expect(r.snap.ProviderQuotas).To(HaveLen(1))
			})
		})

		It("supports re-issuing waits after a timeout (channel is replaced, not exhausted)", func() {
			id, err := reg.Start("sess-1")
			Expect(err).NotTo(HaveOccurred())

			// First wait — short timeout, no mutation. Must surface
			// changed=false on the timeout path.
			_, changed1 := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 50*time.Millisecond)
			Expect(changed1).To(BeFalse())

			// Second wait against the same Turn — fire a mutation
			// mid-wait and observe the wake. This proves the notifier
			// channel is replenished after each broadcast/timeout cycle;
			// a stale closed channel would either spin synchronously
			// (returning changed=false) or panic on close-of-closed.
			type result struct{ changed bool }
			done := make(chan result, 1)
			go func() {
				_, changed := reg.WaitForChange(context.Background(), id, 0, "", 0, "", "", nil, nil, 2*time.Second)
				done <- result{changed: changed}
			}()
			time.Sleep(20 * time.Millisecond)
			Expect(reg.Append(id, session.Message{Role: "assistant", Content: "after-timeout"})).To(Succeed())

			var r result
			Eventually(done, "2s").Should(Receive(&r))
			Expect(r.changed).To(BeTrue(),
				"the second wait must succeed — the notifier channel MUST be replenished after the prior timeout closed it (or after a prior broadcast); the registry's mutation path replaces the channel under lock")
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
