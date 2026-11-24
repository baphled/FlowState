package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	gtypes "github.com/onsi/gomega/types"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// delegateFixture is one parent session with a pool, a workdir and a scripted
// provider; every spec builds its own.
type delegateFixture struct {
	st       *store.Store
	parent   *store.Session
	pool     *BudgetPool
	workdir  string
	streamer *execLoopStreamer
	profile  agent.Profile
}

const delegateParentPool int64 = 100

const delegateBodyPrompt = "You review. You do not edit."

// check panics on a setup failure; must returns v or panics likewise.
func check(err error) {
	if err != nil {
		panic(err)
	}
}

func must[T any](v T, err error) T {
	check(err)
	return v
}

func newDelegateFixture(turns ...[]provider.StreamEvent) *delegateFixture {
	st := must(store.OpenMemory())
	DeferCleanup(func() { _ = st.Close() })
	ctx := context.Background()
	parent := must(st.CreateSession(ctx, "parent-session", "scripted", "parent-model", "harness", "sha"))
	check(st.SetBudgetPool(ctx, parent.ID, delegateParentPool))
	w := must(st.NewEventWriter(ctx, parent.ID))
	if _, err := w.Append(ctx, session.ActorUser, session.TypeTurnStart, map[string]any{"task": "parent task"}); err != nil {
		panic(err)
	}
	check(w.Close())
	return &delegateFixture{
		st: st, parent: parent, pool: NewBudgetPool(delegateParentPool), workdir: GinkgoT().TempDir(),
		streamer: &execLoopStreamer{turns: turns},
		profile: agent.Profile{
			Name: "reviewer", Description: "reviews", Tools: []string{"fs.read"}, Model: agent.InheritModel,
			MaxTurns: 3, Path: "/profiles/reviewer.md", SHA256: "feedface", Body: delegateBodyPrompt,
		},
	}
}

func (f *delegateFixture) request() DelegateRequest {
	return DelegateRequest{
		Store: f.st, Parent: f.parent, Streamer: f.streamer, Pool: f.pool,
		ParentWorkdir: f.workdir, Profile: f.profile, Task: "review the change",
	}
}

func (f *delegateFixture) events(id, typ string) []store.RecordedEvent {
	all := must(f.st.Replay(context.Background(), id))
	var out []store.RecordedEvent
	for _, e := range all {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func (f *delegateFixture) status(id string) string {
	s := must(f.st.GetSession(context.Background(), id))
	return s.Status
}

func (f *delegateFixture) reservation(id string) store.BudgetReservation {
	r := must(f.st.ReservationFor(context.Background(), id))
	return r
}

func (f *delegateFixture) sessionCount() int {
	all := must(f.st.ListSessions(context.Background(), ""))
	return len(all)
}

// receiptPayload decodes the parent's single delegation.completed receipt.
func (f *delegateFixture) receiptPayload() map[string]any {
	evs := f.events(f.parent.ID, TypeDelegationCompleted)
	if len(evs) != 1 {
		panic("expected exactly one delegation receipt")
	}
	var m map[string]any
	check(json.Unmarshal([]byte(evs[0].PayloadJSON), &m))
	return m
}

func finishText(text string) []provider.StreamEvent {
	return []provider.StreamEvent{provText(text), provFinish()}
}

func callTurn(c provider.ToolCall) []provider.StreamEvent {
	return []provider.StreamEvent{provCall(c), provFinish()}
}

func writeCallID(id string) provider.ToolCall {
	c := writeCall()
	c.ID = id
	return c
}

func writeCall() provider.ToolCall {
	return provider.ToolCall{ID: "w1", Name: "fs.write", Arguments: json.RawMessage(`{"path":"new.txt","content":"x"}`)}
}

type blockingStreamer struct {
	execLoopStreamer
	started chan struct{}
	once    sync.Once
}

func (b *blockingStreamer) Stream(ctx context.Context, _ *provider.ChatRequest) <-chan provider.StreamEvent {
	b.once.Do(func() { close(b.started) })
	out := make(chan provider.StreamEvent)
	go func() {
		<-ctx.Done()
		close(out)
	}()
	return out
}

// lateDeadlineCtx expires with context.DeadlineExceeded only when expire is
// called, so a spec can arm the deadline after the provider has been reached.
type lateDeadlineCtx struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newLateDeadlineCtx() *lateDeadlineCtx {
	return &lateDeadlineCtx{Context: context.Background(), done: make(chan struct{})}
}

func (c *lateDeadlineCtx) Done() <-chan struct{} { return c.done }

func (c *lateDeadlineCtx) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *lateDeadlineCtx) expire() { c.once.Do(func() { close(c.done) }) }

type panicStreamer struct{ execLoopStreamer }

func (*panicStreamer) Stream(context.Context, *provider.ChatRequest) <-chan provider.StreamEvent {
	panic("provider exploded")
}

type captureSink struct{ types []string }

func (c *captureSink) Append(_ context.Context, _, typ string, _ any) (int64, error) {
	c.types = append(c.types, typ)
	return int64(len(c.types)), nil
}

// escapeEntries are the working directories a child must be refused; each
// refusal is the one uniform message, which names neither the parent's directory
// nor the directory outside it.
func escapeEntries() []TableEntry {
	entries := []TableEntry{
		Entry("a parent-relative escape", func(*delegateFixture) string { return "../" }),
		Entry("an absolute directory elsewhere", func(*delegateFixture) string { return GinkgoT().TempDir() }),
		Entry("a missing subdirectory", func(*delegateFixture) string { return "absent" }),
		Entry("a file", func(f *delegateFixture) string {
			check(os.WriteFile(filepath.Join(f.workdir, "afile"), []byte("x"), 0o600))
			return "afile"
		}),
		Entry("a path that cleans to outside through a missing component", func(f *delegateFixture) string {
			return f.workdir + string(filepath.Separator) + "trap" + string(filepath.Separator) + ".." + string(filepath.Separator) + ".." + string(filepath.Separator) + "x"
		}),
	}
	if runtime.GOOS != "windows" {
		entries = append(entries, Entry("a symlink that leaves the parent's directory", func(f *delegateFixture) string {
			check(os.Symlink(GinkgoT().TempDir(), filepath.Join(f.workdir, "out")))
			return "out"
		}), Entry("a symlink to a missing directory", func(f *delegateFixture) string {
			check(os.Symlink(filepath.Join(GinkgoT().TempDir(), "gone"), filepath.Join(f.workdir, "dangling")))
			return "dangling"
		}), Entry("a path through a symlink that leaves the parent's directory", func(f *delegateFixture) string {
			check(os.Symlink(GinkgoT().TempDir(), filepath.Join(f.workdir, "out")))
			return "out/x"
		}))
	}
	return entries
}

var _ = Describe("RunDelegatedChild", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	Describe("a completed child", func() {
		var f *delegateFixture
		var res DelegateResult
		BeforeEach(func() {
			f = newDelegateFixture(finishText("all good"))
			res = must(RunDelegatedChild(ctx, f.request()))
		})

		It("reports the completed status", func() { Expect(res.Status).To(Equal(DelegateStatusCompleted)) })
		It("returns the child's final message", func() { Expect(res.FinalContent).To(Equal("all good")) })
		It("counts the provider turns", func() { Expect(res.Turns).To(Equal(int64(1))) })
		It("leaves the parent active", func() { Expect(f.status(f.parent.ID)).To(Equal("active")) })
		It("marks the child session completed", func() { Expect(f.status(res.ChildID)).To(Equal("completed")) })
		It("sends the profile body as the first, system message", func() {
			Expect(f.streamer.reqs[0].Messages[0]).To(Equal(provider.Message{Role: "system", Content: delegateBodyPrompt}))
		})
		It("sends the task as the user message after it", func() {
			Expect(f.streamer.reqs[0].Messages[1]).To(Equal(provider.Message{Role: "user", Content: "review the change"}))
		})
		It("sends the parent's model for an inherit profile", func() {
			Expect(f.streamer.reqs[0].Model).To(Equal("parent-model"))
		})
		It("records the profile name in the child's session.delegated payload", func() {
			Expect(f.events(res.ChildID, TypeSessionDelegated)[0].PayloadJSON).To(ContainSubstring(`"name":"reviewer"`))
		})
		It("records the profile path in the child's session.delegated payload", func() {
			Expect(f.events(res.ChildID, TypeSessionDelegated)[0].PayloadJSON).To(ContainSubstring(`"path":"/profiles/reviewer.md"`))
		})
		It("records the profile sha256 in the child's session.delegated payload", func() {
			Expect(f.events(res.ChildID, TypeSessionDelegated)[0].PayloadJSON).To(ContainSubstring(`"sha256":"feedface"`))
		})
		It("writes the receipt with the profile name", func() {
			Expect(f.receiptPayload()["profile"]).To(HaveKeyWithValue("name", "reviewer"))
		})
		It("writes the receipt with the profile path", func() {
			Expect(f.receiptPayload()["profile"]).To(HaveKeyWithValue("path", "/profiles/reviewer.md"))
		})
		It("writes the receipt with the profile sha256", func() {
			Expect(f.receiptPayload()["profile"]).To(HaveKeyWithValue("sha256", "feedface"))
		})
		It("writes the receipt with the status", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("status", "completed")) })
		It("writes the receipt with the turns", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("turns", BeNumerically("==", 1))) })
		It("writes the receipt with the child id", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("child_id", res.ChildID)) })
		It("writes the receipt with an empty reason", func() { Expect(f.receiptPayload()).To(HaveKeyWithValue("reason", "")) })
		It("charges the spent turn against the reservation", func() { Expect(f.reservation(res.ChildID).Consumed).To(Equal(int64(1))) })
		It("settles the reservation", func() {
			Expect(f.reservation(res.ChildID).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("returns only the unspent grant to the parent pool", func() {
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 1))
		})
		It("writes the receipt with the consumed turns", func() {
			Expect(f.receiptPayload()).To(HaveKeyWithValue("consumed", BeNumerically("==", 1)))
		})
	})

	Describe("consumption over several turns", func() {
		var f *delegateFixture
		var res DelegateResult
		BeforeEach(func() {
			f = newDelegateFixture(callTurn(echoCall("e1")), finishText("done"))
			f.profile.Tools = []string{"echo"}
			res = must(RunDelegatedChild(ctx, f.request()))
		})
		It("charges every spent turn", func() { Expect(f.reservation(res.ChildID).Consumed).To(Equal(int64(2))) })
		It("returns the rest of the grant to the pool", func() { Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 2)) })
		It("counts the tool calls", func() { Expect(res.ToolCalls).To(Equal(int64(1))) })
	})

	Describe("a read-only profile whose model asks to write", func() {
		var f *delegateFixture
		var res DelegateResult
		BeforeEach(func() {
			f = newDelegateFixture(callTurn(writeCall()), finishText("could not write"))
			res = must(RunDelegatedChild(ctx, f.request()))
		})
		It("creates no file", func() {
			_, err := os.Stat(filepath.Join(f.workdir, "new.txt"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
		It("records the denial", func() {
			Expect(f.events(res.ChildID, tool.TypeToolResult)[0].PayloadJSON).To(ContainSubstring("tool_denied"))
		})
		It("lets the child continue to completion", func() { Expect(res.Status).To(Equal(DelegateStatusCompleted)) })
		It("leaves the parent active", func() { Expect(f.status(f.parent.ID)).To(Equal("active")) })
	})

	It("a profile with no tools: denies any tool request", func() {
		f := newDelegateFixture(callTurn(echoCall("e1")), finishText("none"))
		f.profile.Tools = nil
		res := must(RunDelegatedChild(ctx, f.request()))
		Expect(f.events(res.ChildID, tool.TypeToolResult)[0].PayloadJSON).To(ContainSubstring("tool_denied"))
	})

	DescribeTable("refused requests",
		func(mutate func(f *delegateFixture, req *DelegateRequest), want gtypes.GomegaMatcher) {
			f := newDelegateFixture()
			req := f.request()
			mutate(f, &req)
			_, err := RunDelegatedChild(ctx, req)
			Expect(err).To(want)
		},
		Entry("a profile that lists delegate", func(_ *delegateFixture, r *DelegateRequest) {
			r.Profile.Tools = []string{"fs.read", "delegate"}
		}, MatchError(ContainSubstring("delegate is not available to children yet"))),
		Entry("an unknown tool, as a usage error", func(_ *delegateFixture, r *DelegateRequest) {
			r.Profile.Tools = []string{"fs.nope"}
		}, MatchError(ErrUsage)),
		Entry("a terminal parent", func(f *delegateFixture, _ *DelegateRequest) {
			check(f.st.UpdateSessionStatus(ctx, f.parent.ID, "completed"))
		}, MatchError(ErrUsage)),
		Entry("a nested delegation beyond the depth cap", func(f *delegateFixture, r *DelegateRequest) {
			c1 := must(CreateChildSession(ctx, f.st, f.parent.ID, "scripted", "m", "h", "s", "t"))
			r.Parent = must(CreateChildSession(ctx, f.st, c1.ID, "scripted", "m", "h", "s", "t"))
		}, MatchError(ContainSubstring("delegation depth exceeds"))),
		Entry("a request without a store", func(_ *delegateFixture, r *DelegateRequest) { r.Store = nil },
			MatchError(ErrUsage)),
	)

	It("creates no child for a refused profile", func() {
		f := newDelegateFixture()
		f.profile.Tools = []string{"delegate"}
		_, _ = RunDelegatedChild(ctx, f.request())
		Expect(f.sessionCount()).To(Equal(1))
	})

	Describe("the child's working directory", func() {
		var f *delegateFixture
		var seen string
		BeforeEach(func() {
			f = newDelegateFixture(finishText("ok"))
			seen = ""
		})
		request := func(workdir string) DelegateRequest {
			req := f.request()
			req.Workdir = workdir
			req.NewRegistry = func(dir string) (*tool.Registry, error) {
				seen = dir
				return NewSessionToolRegistry(dir)
			}
			return req
		}
		DescribeTable("refuses with the uniform message", func(dir func(*delegateFixture) string) {
			_, err := RunDelegatedChild(ctx, request(dir(f)))
			Expect(err).To(MatchError(errWorkdirRefused().Error()))
		}, escapeEntries())
		It("creates no child for a refused directory", func() {
			_, _ = RunDelegatedChild(ctx, request("../"))
			Expect(f.sessionCount()).To(Equal(1))
		})
		It("refuses a parent working directory that is a file", func() {
			file := filepath.Join(f.workdir, "afile")
			check(os.WriteFile(file, []byte("x"), 0o600))
			req := request("")
			req.ParentWorkdir = file
			_, err := RunDelegatedChild(ctx, req)
			Expect(err).To(MatchError(errParentWorkdirRefused().Error()))
		})
		It("refuses a parent working directory that is missing, without naming it", func() {
			req := request("")
			req.ParentWorkdir = filepath.Join(f.workdir, "gone")
			_, err := RunDelegatedChild(ctx, req)
			Expect(err).To(MatchError(errParentWorkdirRefused().Error()))
		})
		It("accepts a path that cleans to a directory inside the parent's through a missing component", func() {
			check(os.Mkdir(filepath.Join(f.workdir, "pkg"), 0o750))
			sep := string(filepath.Separator)
			res := must(RunDelegatedChild(ctx, request(f.workdir+sep+"trap"+sep+".."+sep+"pkg")))
			Expect(res.Status).To(Equal(DelegateStatusCompleted))
		})
		if runtime.GOOS != "windows" {
			Describe("under a symlinked parent", func() {
				var real, link string
				BeforeEach(func() {
					real = must(filepath.EvalSymlinks(f.workdir))
					check(os.Mkdir(filepath.Join(real, "sub"), 0o750))
					link = filepath.Join(GinkgoT().TempDir(), "link")
					check(os.Symlink(real, link))
				})
				run := func(child string) {
					req := request(child)
					req.ParentWorkdir = link
					_ = must(RunDelegatedChild(ctx, req))
				}
				It("accepts an absolute child named through the symlink", func() {
					run(filepath.Join(link, "sub"))
					Expect(seen).To(Equal(filepath.Join(real, "sub")))
				})
				It("accepts an absolute child named by the real path", func() {
					run(filepath.Join(real, "sub"))
					Expect(seen).To(Equal(filepath.Join(real, "sub")))
				})
			})
		}
		It("roots the registry at the parent's directory by default", func() {
			_, _ = RunDelegatedChild(ctx, request(""))
			Expect(seen).To(Equal(must(filepath.EvalSymlinks(f.workdir))))
		})
		It("accepts a subdirectory and roots the registry there", func() {
			sub := filepath.Join(f.workdir, "pkg")
			check(os.Mkdir(sub, 0o750))
			res := must(RunDelegatedChild(ctx, request("pkg")))
			Expect([]string{res.Status, seen}).To(Equal([]string{DelegateStatusCompleted, must(filepath.EvalSymlinks(sub))}))
		})
	})

	Describe("a budget that cannot cover max_turns", func() {
		var f *delegateFixture
		var res DelegateResult
		var err error
		BeforeEach(func() {
			f = newDelegateFixture(finishText("never"))
			f.pool = NewBudgetPool(2)
			res, err = RunDelegatedChild(ctx, f.request())
		})
		It("reports a failed delegation", func() { Expect(res.Status).To(Equal(DelegateStatusFailed)) })
		It("names the budget in the reason", func() { Expect(res.Reason).To(ContainSubstring("budget_exhausted")) })
		It("returns no error", func() { Expect(err).To(Succeed()) })
		It("creates no child session", func() { Expect(f.sessionCount()).To(Equal(1)) })
		It("leaves the pool unchanged", func() { Expect(f.pool.Remaining()).To(Equal(int64(2))) })
		It("runs no provider turn", func() { Expect(f.streamer.reqs).To(BeEmpty()) })
	})

	Describe("a store pool that cannot cover max_turns", func() {
		var f *delegateFixture
		var res DelegateResult
		var err error
		BeforeEach(func() {
			f = newDelegateFixture(finishText("never"))
			check(f.st.SetBudgetPool(ctx, f.parent.ID, 2))
			res, err = RunDelegatedChild(ctx, f.request())
		})
		It("reports a failed delegation", func() { Expect(res.Status).To(Equal(DelegateStatusFailed)) })
		It("names the budget in the reason", func() { Expect(res.Reason).To(ContainSubstring("budget_exhausted")) })
		It("returns no error", func() { Expect(err).To(Succeed()) })
		It("returns the in-memory draw", func() { Expect(f.pool.Remaining()).To(Equal(delegateParentPool)) })
		It("creates no child session", func() { Expect(f.sessionCount()).To(Equal(1)) })
	})

	Describe("a cancellation between the creation checks and the reserve", func() {
		var f *delegateFixture
		var res DelegateResult
		var err error
		BeforeEach(func() {
			f = newDelegateFixture(finishText("never"))
			cctx, cancel := context.WithCancelCause(ctx)
			req := f.request()
			req.NewRegistry = func(dir string) (*tool.Registry, error) {
				cancel(&CancelCause{Reason: "client_disconnected"})
				return NewSessionToolRegistry(dir)
			}
			res, err = RunDelegatedChild(cctx, req)
		})
		It("reports the cancelled status with the cause's reason", func() {
			Expect(res).To(Equal(DelegateResult{Status: DelegateStatusCancelled, Reason: "client_disconnected"}))
		})
		It("returns no error", func() { Expect(err).To(Succeed()) })
		It("returns the in-memory draw", func() { Expect(f.pool.Remaining()).To(Equal(delegateParentPool)) })
		It("creates no child session", func() { Expect(f.sessionCount()).To(Equal(1)) })
	})

	Describe("a deadline", func() {
		var f *delegateFixture
		var res DelegateResult
		BeforeEach(func() {
			f = newDelegateFixture()
			req := f.request()
			req.Streamer = &blockingStreamer{started: make(chan struct{})}
			blocker := req.Streamer.(*blockingStreamer)
			dctx := newLateDeadlineCtx()
			type outcome struct {
				res DelegateResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				r, e := RunDelegatedChild(dctx, req)
				done <- outcome{r, e}
			}()
			select {
			case <-blocker.started:
			case <-time.After(10 * time.Second):
				panic("child never reached the provider")
			}
			dctx.expire()
			select {
			case o := <-done:
				if o.err != nil {
					panic(o.err)
				}
				res = o.res
			case <-time.After(10 * time.Second):
				panic("the child loop did not return after the deadline")
			}
		})
		It("fails the child rather than cancelling it", func() { Expect(f.status(res.ChildID)).To(Equal("failed")) })
		It("says the deadline was exceeded", func() { Expect(res.Reason).To(ContainSubstring("deadline exceeded")) })
	})

	Describe("which failures are the child's own", func() {
		expired := func() context.Context {
			c := newLateDeadlineCtx()
			c.expire()
			return c
		}
		It("counts an unrelated error once the context's deadline has expired", func() {
			Expect(delegateFailureIsChild(expired(), errors.New("lease read: interrupted"))).To(BeTrue())
		})
		It("counts a deadline in the error chain", func() {
			Expect(delegateFailureIsChild(ctx, fmt.Errorf("read: %w", context.DeadlineExceeded))).To(BeTrue())
		})
		It("leaves an unrelated error to the caller while the deadline holds", func() {
			Expect(delegateFailureIsChild(ctx, errors.New("lease read: disk full"))).To(BeFalse())
		})
	})

	Describe("a panicking provider", func() {
		var f *delegateFixture
		var recovered any
		var childID string
		BeforeEach(func() {
			f = newDelegateFixture()
			req := f.request()
			req.Streamer = &panicStreamer{}
			func() {
				defer func() { recovered = recover() }()
				_, _ = RunDelegatedChild(ctx, req)
			}()
			all, err := f.st.ListSessions(ctx, "")
			if err != nil || len(all) != 2 {
				panic("expected the parent and one child")
			}
			childID = all[0].ID
			if childID == f.parent.ID {
				childID = all[1].ID
			}
		})
		It("propagates the original panic", func() { Expect(recovered).To(Equal("provider exploded")) })
		It("marks the child failed", func() { Expect(f.status(childID)).To(Equal("failed")) })
		It("settles the reservation", func() {
			Expect(f.reservation(childID).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("charges the whole grant, as the spent turns are unknown", func() {
			Expect(f.reservation(childID).Consumed).To(Equal(int64(f.profile.MaxTurns)))
		})
		It("keeps the whole grant out of the pool", func() {
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - int64(f.profile.MaxTurns)))
		})
		It("writes the receipt with the reason panic", func() {
			Expect(f.receiptPayload()).To(HaveKeyWithValue("reason", "panic"))
		})
	})

	It("a child failure: clips a long reason in the receipt with the truncation notice", func() {
		f := newDelegateFixture([]provider.StreamEvent{provError(strings.Repeat("r", 200*1024))})
		if _, err := RunDelegatedChild(ctx, f.request()); err != nil {
			panic(err)
		}
		Expect(f.receiptPayload()["reason"]).To(HaveSuffix("cut at 50 KB / 2000 lines]"))
	})

	It("a nil receipt sink with concurrent children: writes one receipt per child", func() {
		f := newDelegateFixture()
		var wg sync.WaitGroup
		for range 8 {
			req := f.request()
			req.Streamer = &execLoopStreamer{turns: [][]provider.StreamEvent{finishText("ok")}}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				if _, err := RunDelegatedChild(ctx, req); err != nil {
					panic(err)
				}
			}()
		}
		wg.Wait()
		Expect(f.events(f.parent.ID, TypeDelegationCompleted)).To(HaveLen(8))
	})

	It("the receipt locks: hold back a receipt for a parent that is locked", func() {
		f := newDelegateFixture(finishText("ok"))
		release := receiptLocks.acquire(f.parent.ID)
		done := make(chan struct{})
		go func() {
			defer close(done)
			if _, err := RunDelegatedChild(ctx, f.request()); err != nil {
				panic(err)
			}
		}()
		Consistently(func() int { return len(f.events(f.parent.ID, TypeDelegationCompleted)) }).
			WithTimeout(200 * time.Millisecond).WithPolling(20 * time.Millisecond).Should(BeZero())
		release()
		<-done
	})
	It("forget a parent once its last user is done", func() {
		receiptLocks.acquire("q")()
		receiptLocks.mu.Lock()
		defer receiptLocks.mu.Unlock()
		Expect(receiptLocks.locks).NotTo(HaveKey("q"))
	})

	Describe("a provider failure in the child", func() {
		var f *delegateFixture
		var res DelegateResult
		var err error
		BeforeEach(func() {
			f = newDelegateFixture([]provider.StreamEvent{provError("upstream down")}, finishText("second ok"))
			res, err = RunDelegatedChild(ctx, f.request())
		})
		It("returns no error", func() { Expect(err).To(Succeed()) })
		It("reports the failed status", func() { Expect(res.Status).To(Equal(DelegateStatusFailed)) })
		It("carries the provider reason", func() { Expect(res.Reason).To(ContainSubstring("upstream down")) })
		It("marks the child failed", func() { Expect(f.status(res.ChildID)).To(Equal("failed")) })
		It("settles the reservation", func() {
			Expect(f.reservation(res.ChildID).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("charges the turn the failure used", func() { Expect(f.reservation(res.ChildID).Consumed).To(Equal(int64(1))) })
		It("returns the unspent grant to the parent pool", func() {
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 1))
		})
		It("leaves the parent active", func() { Expect(f.status(f.parent.ID)).To(Equal("active")) })
		It("writes the receipt with the failure reason", func() {
			Expect(f.receiptPayload()["reason"]).To(ContainSubstring("upstream down"))
		})
		It("lets a second delegation complete", func() {
			second := must(RunDelegatedChild(ctx, f.request()))
			Expect(second.Status).To(Equal(DelegateStatusCompleted))
		})
	})

	Describe("a parent cancelled mid-child", func() {
		var f *delegateFixture
		var res DelegateResult
		var err error
		BeforeEach(func() {
			f = newDelegateFixture()
			blocker := &blockingStreamer{started: make(chan struct{})}
			req := f.request()
			req.Streamer = blocker
			cctx, cancel := context.WithCancelCause(ctx)
			defer cancel(nil)
			type outcome struct {
				res DelegateResult
				err error
			}
			done := make(chan outcome, 1)
			go func() {
				r, e := RunDelegatedChild(cctx, req)
				done <- outcome{r, e}
			}()
			select {
			case <-blocker.started:
			case <-time.After(10 * time.Second):
				panic("child never reached the provider")
			}
			cancel(&CancelCause{Reason: "sigterm"})
			select {
			case o := <-done:
				res, err = o.res, o.err
			case <-time.After(10 * time.Second):
				panic("the child loop did not return after cancellation")
			}
		})
		It("returns no error", func() { Expect(err).To(Succeed()) })
		It("reports the cancelled status", func() { Expect(res.Status).To(Equal(DelegateStatusCancelled)) })
		It("carries the parent's reason", func() { Expect(res.Reason).To(Equal("sigterm")) })
		It("marks the child cancelled", func() { Expect(f.status(res.ChildID)).To(Equal("cancelled")) })
		It("records the parent's reason in the child's session.cancelled event", func() {
			Expect(f.events(res.ChildID, session.TypeSessionCancelled)[0].PayloadJSON).To(ContainSubstring(`"reason":"sigterm"`))
		})
		It("settles the reservation", func() {
			Expect(f.reservation(res.ChildID).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("returns the unspent grant to the parent pool", func() {
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 1))
		})
		It("records the cancellation in the receipt", func() {
			Expect(f.receiptPayload()).To(HaveKeyWithValue("status", "cancelled"))
		})
	})

	It("the system prompt: is not part of the parent's own requests", func() {
		f := newDelegateFixture(finishText("child"), finishText("parent"))
		if _, err := RunDelegatedChild(ctx, f.request()); err != nil {
			panic(err)
		}
		registry := must(NewSessionToolRegistry(""))
		_, err := RunExecLoop(ctx, ExecLoopConfig{
			Store: f.st, Session: f.parent, Streamer: f.streamer, Registry: registry,
			LeaseItem: "exec:parent", LeaseAgent: "cli", MaxTurns: 1,
		})
		if err != nil {
			panic(err)
		}
		Expect(f.streamer.reqs[1].Messages[0].Role).To(Equal("user"))
	})

	It("the model: uses a named profile model for the child", func() {
		f := newDelegateFixture(finishText("ok"))
		f.profile.Model = "other-model"
		if _, err := RunDelegatedChild(ctx, f.request()); err != nil {
			panic(err)
		}
		Expect(f.streamer.reqs[0].Model).To(Equal("other-model"))
	})

	Context("a child still asking for tools at its max_turns", func() {
		var f *delegateFixture
		var res DelegateResult
		var rerr error
		BeforeEach(func() {
			ask := func(id, text string) []provider.StreamEvent {
				return []provider.StreamEvent{provText(text), provCall(echoCall(id)), provFinish()}
			}
			f = newDelegateFixture(ask("a", "so far"), ask("b", "last words"))
			f.profile.Tools, f.profile.MaxTurns = []string{"echo"}, 2
			res, rerr = RunDelegatedChild(ctx, f.request())
		})

		It("ends failed", func() { Expect(res.Status).To(Equal(DelegateStatusFailed)) })
		It("gives max_turns as the reason", func() { Expect(res.Reason).To(Equal("max_turns")) })
		It("marks the result partial", func() { Expect(res.Partial).To(BeTrue()) })
		It("keeps the child's last text", func() { Expect(res.FinalContent).To(Equal("last words")) })
		It("marks the child session failed", func() { Expect(f.status(res.ChildID)).To(Equal("failed")) })
		It("is not an error for the caller: the parent continues", func() { Expect(rerr).To(Succeed()) })
		It("charges the reservation for every turn", func() {
			Expect(f.reservation(res.ChildID).Consumed).To(Equal(int64(2)))
		})
		It("settles the reservation", func() {
			Expect(f.reservation(res.ChildID).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("records max_turns in the receipt", func() {
			Expect(f.receiptPayload()).To(HaveKeyWithValue("reason", "max_turns"))
		})
	})

	Context("a parent whose child reaches max_turns", func() {
		var f *toolFixture
		var res *ExecLoopResult
		var perr error
		ask := func(id, text string) []provider.StreamEvent {
			return []provider.StreamEvent{provText(text), provCall(writeCallID(id)), provFinish()}
		}
		BeforeEach(func() {
			f = newToolFixture(callTurn(delegateCall(`{"agent":"writer","task":"look"}`)),
				ask("w1", "wrote one"), ask("w2", "wrote two"), finishText("parent done"))
			res, perr = f.runParent(ctx)
		})

		It("continues and answers", func() { Expect(res.FinalContent).To(Equal("parent done")) })
		It("completes without an error", func() { Expect(perr).To(Succeed()) })
		It("tells the model the child failed with max_turns", func() {
			Expect(f.toolResultContent()).To(ContainSubstring("failed after 2 turns: max_turns"))
		})
		It("tells the model the child's output is partial", func() {
			Expect(f.toolResultContent()).To(ContainSubstring("partial output:\nwrote two"))
		})
	})

	It("a parallel entry whose child reaches max_turns: reports failed, max_turns and the partial text", func() {
		ask := func(id, text string) []provider.StreamEvent {
			return []provider.StreamEvent{provText(text), provCall(writeCallID(id)), provFinish()}
		}
		f := newToolFixture(callTurn(delegateCall(`{"tasks":[{"agent":"writer","task":"look"}]}`)),
			ask("w1", "wrote one"), ask("w2", "wrote two"), finishText("parent done"))
		_, perr := f.runParent(ctx)
		if perr != nil {
			panic(perr)
		}
		Expect(f.toolResultContent()).To(ContainSubstring(`"status":"failed","turns":2,"text":"wrote two","reason":"max_turns"`))
	})

	It("a receipt through the caller's sink: is appended to the sink instead of a new parent writer", func() {
		f := newDelegateFixture(finishText("ok"))
		sink := &captureSink{}
		req := f.request()
		req.ParentEvents = sink
		if _, err := RunDelegatedChild(ctx, req); err != nil {
			panic(err)
		}
		Expect(sink.types).To(Equal([]string{TypeDelegationCompleted}))
	})

	It("a parent writer released within the retry window: still writes the receipt", func() {
		f := newDelegateFixture(finishText("ok"))
		held := must(f.st.NewEventWriter(ctx, f.parent.ID))
		time.AfterFunc(30*time.Millisecond, func() { _ = held.Close() })
		must(RunDelegatedChild(ctx, f.request()))
		Expect(f.events(f.parent.ID, TypeDelegationCompleted)).To(HaveLen(1))
	})

	It("a parent whose event writer is held elsewhere: reports the receipt failure as an error", func() {
		f := newDelegateFixture(finishText("ok"))
		req := f.request()
		held := must(f.st.NewEventWriter(ctx, f.parent.ID))
		defer func() { _ = held.Close() }()
		_, rerr := RunDelegatedChild(ctx, req)
		Expect(rerr).To(MatchError(ContainSubstring("receipt writer")))
	})
})

// lateCancelContext reports context.Canceled from Err as soon as the session's
// log holds a tool.result, and never closes Done. That is the window the
// last-turn guard has to cover: a cancel that lands after the last tool
// result is recorded (the store refuses events on a cancelled context, so a
// cancel any earlier is caught elsewhere) and before the loop looks at the
// context again.
type lateCancelContext struct {
	context.Context
	st        *store.Store
	sessionID string
}

func (c *lateCancelContext) Err() error {
	if err := c.Context.Err(); err != nil {
		return err
	}
	events, err := c.st.Replay(context.Background(), c.sessionID)
	if err != nil {
		panic(err)
	}
	for _, e := range events {
		if e.Type == tool.TypeToolResult {
			return context.Canceled
		}
	}
	return nil
}

var _ = Describe("A parent cancelled during its last turn's delegation", func() {
	var (
		st      *store.Store
		sess    *store.Session
		workdir string
		cctx    *lateCancelContext
		turns   [][]provider.StreamEvent
	)

	BeforeEach(func() {
		config := GinkgoT().TempDir()
		for _, env := range []string{"XDG_CONFIG_HOME", "HOME", "APPDATA"} {
			GinkgoT().Setenv(env, config)
		}
		base := must(os.UserConfigDir())
		putAgent(filepath.Join(base, "flowstate", "agents"), "reviewer.md", agentText("reviewer"))
		st, sess = execLoopSeed()
		DeferCleanup(func() { _ = st.Close() })
		workdir = GinkgoT().TempDir()
		cctx = &lateCancelContext{Context: context.Background(), st: st, sessionID: sess.ID}
		// Request 1 is the parent's only turn; request 2 is the child's.
		turns = [][]provider.StreamEvent{callTurn(delegateCall(`{"agent":"reviewer","task":"look"}`)), finishText("child")}
	})

	Context("through RunExecLoop", func() {
		var err error
		BeforeEach(func() {
			registry := must(NewSessionToolRegistry(workdir))
			streamer := &execLoopStreamer{turns: turns}
			profiles := must(LoadProfiles("user", workdir))
			check(registry.Register(NewDelegateTool(DelegateToolConfig{
				Store: st, Parent: sess, Streamer: streamer, Workdir: workdir, Profiles: profiles,
			})))
			_, err = RunExecLoop(cctx, ExecLoopConfig{
				Store: st, Session: sess, Streamer: streamer, Registry: registry, Allowlist: []string{"delegate"},
				LeaseItem: "exec:" + sess.ID, LeaseAgent: "cli", MaxTurns: 1,
			})
		})

		It("reports the context error, not success", func() { Expect(err).To(MatchError(context.Canceled)) })
		It("does not fail the run with max_turns", func() { Expect(err).NotTo(MatchError(ErrMaxTurns)) })
		It("records no provider.error", func() {
			Expect(lastType(st, sess.ID)).NotTo(Equal(TypeProviderError))
		})
	})

	Context("through RunSessionExecLoop", func() {
		var err error
		BeforeEach(func() {
			streamer := &execLoopStreamer{turns: turns}
			_, err = RunSessionExecLoop(cctx, st, filepath.Join(workdir, "run.db"), sess, streamer, SessionExecLoopRequest{
				Allow: []string{"delegate"}, Agents: "user", Workdir: workdir, MaxTurns: 1,
			})
		})

		It("reports the session cancelled", func() { Expect(err).To(MatchError(ErrSessionCancelled)) })
		It("ends the session cancelled, not active or failed", func() {
			Expect(statusOf(st, sess.ID)).To(Equal("cancelled"))
		})
	})
})
