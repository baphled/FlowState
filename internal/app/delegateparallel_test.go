package app

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// parallelStreamer is a thread-safe fake provider for the children (keyed by the
// task text) and, when parent is set, for the parent's two turns. A gated task
// blocks inside Stream until its gate is closed or the context is cancelled.
type parallelStreamer struct {
	mu          sync.Mutex
	started     int
	running     int
	peak        int
	parentTurns int
	gates       map[string]chan struct{}
	fail        map[string]bool
	explode     map[string]bool
	text        map[string]string
	entered     chan string
	parent      *provider.ToolCall
}

func newParallelStreamer() *parallelStreamer {
	return &parallelStreamer{
		gates: map[string]chan struct{}{}, fail: map[string]bool{}, explode: map[string]bool{},
		text: map[string]string{}, entered: make(chan string, 64),
	}
}

func (*parallelStreamer) Name() string     { return "scripted" }
func (*parallelStreamer) Models() []string { return []string{"scripted-model"} }
func (*parallelStreamer) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ignored"}, nil
}

// gate makes the named tasks block until release.
func (p *parallelStreamer) gate(tasks ...string) {
	for _, t := range tasks {
		p.gates[t] = make(chan struct{})
	}
}

// release unblocks a gated task.
func (p *parallelStreamer) release(task string) { close(p.gates[task]) }

// releaseAll unblocks every gate still closed.
func (p *parallelStreamer) releaseAll() {
	for _, g := range p.gates {
		select {
		case <-g:
		default:
			close(g)
		}
	}
}

func (p *parallelStreamer) counts() (started, running, peak int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.started, p.running, p.peak
}

func (p *parallelStreamer) startedCount() int { s, _, _ := p.counts(); return s }

func streamOf(events ...provider.StreamEvent) <-chan provider.StreamEvent {
	out := make(chan provider.StreamEvent, len(events))
	for _, e := range events {
		out <- e
	}
	close(out)
	return out
}

func (p *parallelStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	if p.parent != nil && slices.Contains(toolNames(req), "delegate") {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.parentTurns++
		if p.parentTurns == 1 {
			return streamOf(callTurn(*p.parent)...)
		}
		return streamOf(finishText("all done")...)
	}
	task := ""
	for _, m := range req.Messages {
		if m.Role == "user" {
			task = m.Content
			break
		}
	}
	p.mu.Lock()
	p.started++
	p.running++
	p.peak = max(p.peak, p.running)
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.running--
		p.mu.Unlock()
	}()
	p.entered <- task
	if g := p.gates[task]; g != nil {
		select {
		case <-g:
		case <-ctx.Done():
			return streamOf()
		}
	}
	switch {
	case p.explode[task]:
		panic("provider exploded")
	case p.fail[task]:
		return streamOf(provError("boom"))
	case p.text[task] != "":
		return streamOf(finishText(p.text[task])...)
	}
	return streamOf(finishText("done " + task)...)
}

// parallelOutcome is what a parallel delegate call produced.
type parallelOutcome struct {
	result     any
	err        error
	panicValue any
}

func (o parallelOutcome) results() []DelegateTaskResult {
	r, ok := o.result.(DelegateParallelResult)
	if !ok {
		panic("the call returned no parallel result")
	}
	return r.Results
}

func (o parallelOutcome) statuses() []string {
	var out []string
	for _, r := range o.results() {
		out = append(out, r.Status)
	}
	return out
}

func (o parallelOutcome) reasons() []string {
	var out []string
	for _, r := range o.results() {
		out = append(out, r.Reason)
	}
	return out
}

func (o parallelOutcome) texts() []string {
	var out []string
	for _, r := range o.results() {
		out = append(out, r.Text)
	}
	return out
}

func (o parallelOutcome) indexes() []int {
	var out []int
	for _, r := range o.results() {
		out = append(out, r.Index)
	}
	return out
}

// parallelFixture is a delegate fixture whose children share a parallelStreamer.
type parallelFixture struct {
	*delegateFixture
	ps   *parallelStreamer
	tool *DelegateTool
}

func newParallelFixture(ps *parallelStreamer, maxTurns int, pool *BudgetPool) *parallelFixture {
	f := &parallelFixture{delegateFixture: newDelegateFixture(), ps: ps}
	f.profile.MaxTurns = maxTurns
	if pool != nil {
		f.pool = pool
	}
	f.tool = NewDelegateTool(DelegateToolConfig{
		Store: f.st, Parent: f.parent, Streamer: ps, Pool: f.pool, Workdir: f.workdir,
		Profiles: []agent.Profile{f.profile},
	})
	DeferCleanup(ps.releaseAll)
	return f
}

// start runs the call in a goroutine and reports its outcome, recovering a panic.
func (f *parallelFixture) start(ctx context.Context, args string) <-chan parallelOutcome {
	done := make(chan parallelOutcome, 1)
	go func() {
		var o parallelOutcome
		defer func() {
			o.panicValue = recover()
			done <- o
		}()
		o.result, o.err = f.tool.Invoke(ctx, json.RawMessage(args))
	}()
	return done
}

func awaitOutcome(done <-chan parallelOutcome) parallelOutcome {
	select {
	case o := <-done:
		return o
	case <-time.After(20 * time.Second):
		panic("the delegate call did not return")
	}
}

func (f *parallelFixture) awaitEntered(n int) {
	for i := 0; i < n; i++ {
		select {
		case <-f.ps.entered:
		case <-time.After(20 * time.Second):
			panic("a child never reached the provider")
		}
	}
}

// pollUntil waits, without sleeping, until cond holds; it panics on a timeout.
func pollUntil(what string, cond func() bool) {
	deadline := time.After(20 * time.Second)
	for !cond() {
		select {
		case <-deadline:
			panic("timed out waiting for " + what)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

// awaitReceipts waits until the parent's log holds n delegation receipts.
func (f *parallelFixture) awaitReceipts(n int) {
	pollUntil("the receipts", func() bool { return len(f.events(f.parent.ID, TypeDelegationCompleted)) == n })
}

// holdWindow guards that exactly n children have started and stay so for a short window.
func (f *parallelFixture) holdWindow(n int) {
	end := time.After(150 * time.Millisecond)
	for {
		if f.ps.startedCount() != n {
			panic("the number of started children changed")
		}
		select {
		case <-end:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// childSessions lists the sessions other than the parent's.
func (f *parallelFixture) childSessions() []store.Session {
	var out []store.Session
	for _, s := range must(f.st.ListSessions(context.Background(), "")) {
		if s.ID != f.parent.ID {
			out = append(out, s)
		}
	}
	return out
}

func (f *parallelFixture) childStatuses() []string {
	var out []string
	for _, s := range f.childSessions() {
		out = append(out, s.Status)
	}
	slices.Sort(out)
	return out
}

// reservationStatuses lists the status of the reservation of every child.
func (f *parallelFixture) reservationStatuses() []string {
	var out []string
	for _, s := range f.childSessions() {
		out = append(out, f.reservation(s.ID).Status)
	}
	return out
}

func repeated(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func taskNames(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "task-" + string(rune('0'+i))
	}
	return out
}

// receiptIndexes lists the index of every receipt in log order.
func (f *parallelFixture) receiptIndexes() []int {
	var out []int
	for _, e := range f.events(f.parent.ID, TypeDelegationCompleted) {
		var m map[string]any
		check(json.Unmarshal([]byte(e.PayloadJSON), &m))
		out = append(out, int(m["index"].(float64)))
	}
	return out
}

// gateSink blocks every Append until its gate closes and counts how many are inside.
type gateSink struct {
	inside atomic.Int32
	gate   chan struct{}
}

func (g *gateSink) Append(context.Context, string, string, any) (int64, error) {
	g.inside.Add(1)
	<-g.gate
	return 1, nil
}

var _ = Describe("The delegate tool with tasks", func() {
	var f *parallelFixture
	var ps *parallelStreamer

	BeforeEach(func() { ps = newParallelStreamer() })

	Describe("a refused call", func() {
		BeforeEach(func() { f = newParallelFixture(ps, 3, nil) })
		It("starts no child when entry 3 names an unknown agent", func() {
			args := `{"tasks":[{"agent":"reviewer","task":"a"},{"agent":"reviewer","task":"b"},` +
				`{"agent":"reviewer","task":"c"},{"agent":"ghost","task":"d"}]}`
			_ = awaitOutcome(f.start(context.Background(), args))
			Expect(ps.startedCount()).To(Equal(0))
		})
		It("creates no session for nine tasks", func() {
			_ = awaitOutcome(f.start(context.Background(), tasksJSON(9)))
			Expect(f.sessionCount()).To(Equal(1))
		})
		It("creates no session when entry 3 names an unknown agent", func() {
			args := `{"tasks":[{"agent":"reviewer","task":"a"},{"agent":"reviewer","task":"b"},` +
				`{"agent":"reviewer","task":"c"},{"agent":"ghost","task":"d"}]}`
			_ = awaitOutcome(f.start(context.Background(), args))
			Expect(f.sessionCount()).To(Equal(1))
		})
	})

	Describe("four children that finish in reverse order", func() {
		var out parallelOutcome
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			ps.gate(taskNames(4)...)
			done := f.start(context.Background(), tasksJSON(4))
			f.awaitEntered(4)
			for k := 3; k >= 0; k-- {
				ps.release(taskNames(4)[k])
				f.awaitReceipts(4 - k)
			}
			out = awaitOutcome(done)
		})
		It("returns the results in input order", func() { Expect(out.indexes()).To(Equal([]int{0, 1, 2, 3})) })
		It("pairs each result with its own task", func() {
			Expect(out.texts()).To(Equal([]string{"done task-0", "done task-1", "done task-2", "done task-3"}))
		})
		It("completes every child", func() { Expect(out.statuses()).To(Equal(repeated("completed", 4))) })
		It("logs the receipts as the children finish, each with its index", func() {
			Expect(f.receiptIndexes()).To(Equal([]int{3, 2, 1, 0}))
		})
	})

	Describe("eight tasks all held at a barrier", func() {
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			ps.gate(taskNames(8)...)
		})
		It("starts only four while four are running", func() {
			done := f.start(context.Background(), tasksJSON(8))
			f.awaitEntered(4)
			DeferCleanup(func() { ps.releaseAll(); _ = awaitOutcome(done) })
			Consistently(ps.startedCount).WithTimeout(150 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Equal(4))
		})
		It("never runs more than four at once", func() {
			done := f.start(context.Background(), tasksJSON(8))
			f.awaitEntered(4)
			f.holdWindow(4)
			ps.releaseAll()
			_ = awaitOutcome(done)
			_, _, peak := ps.counts()
			Expect(peak).To(Equal(4))
		})
	})

	Describe("a freed slot", func() {
		It("starts the next waiting task while the other three still run", func() {
			f = newParallelFixture(ps, 3, nil)
			ps.gate("task-0", "task-1", "task-2", "task-3")
			done := f.start(context.Background(), tasksJSON(8))
			DeferCleanup(func() { ps.releaseAll(); _ = awaitOutcome(done) })
			f.awaitEntered(4)
			f.holdWindow(4)
			ps.release("task-0")
			pollUntil("the eighth start", func() bool { return ps.startedCount() == 8 })
			Expect(ps.startedCount()).To(Equal(8))
		})
	})

	Describe("one child that fails", func() {
		var out parallelOutcome
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			ps.fail["task-1"] = true
			ps.gate("task-0", "task-2")
			done := f.start(context.Background(), tasksJSON(3))
			f.awaitEntered(3)
			f.awaitReceipts(1) // the failing child has finished while its siblings still run
			ps.release("task-0")
			ps.release("task-2")
			out = awaitOutcome(done)
		})
		It("leaves its siblings to complete", func() {
			Expect(out.statuses()).To(Equal([]string{"completed", "failed", "completed"}))
		})
		It("is an entry with its reason, not a tool error", func() { Expect(out.result).To(BeAssignableToTypeOf(DelegateParallelResult{})) })
		It("carries the provider's reason", func() { Expect(out.results()[1].Reason).To(ContainSubstring("boom")) })
	})

	Describe("a working directory outside the parent's", func() {
		var out parallelOutcome
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			out = awaitOutcome(f.start(context.Background(),
				`{"tasks":[{"agent":"reviewer","task":"task-0","cwd":"../"},{"agent":"reviewer","task":"task-1"}]}`))
		})
		It("is that entry's failed result and spares the sibling", func() {
			Expect(out.statuses()).To(Equal([]string{"failed", "completed"}))
		})
		It("names the refusal as its reason", func() {
			Expect(out.results()[0].Reason).To(ContainSubstring("inside the parent's"))
		})
	})

	Describe("a pool that covers two of four one-turn grants", func() {
		var out parallelOutcome
		var pool *BudgetPool
		BeforeEach(func() {
			pool = NewBudgetPool(2)
			f = newParallelFixture(ps, 1, pool)
			out = awaitOutcome(f.start(context.Background(), tasksJSON(4)))
		})
		It("completes two children", func() {
			Expect(slices.DeleteFunc(out.statuses(), func(s string) bool { return s != "completed" })).To(HaveLen(2))
		})
		It("refuses the other two as budget_exhausted results", func() {
			Expect(slices.DeleteFunc(out.reasons(), func(r string) bool {
				return !strings.HasPrefix(r, "budget_exhausted")
			})).To(HaveLen(2))
		})
		It("ends with the pool holding exactly the unspent grants", func() { Expect(pool.Remaining()).To(BeZero()) })
		It("creates sessions only for the two granted children", func() { Expect(f.sessionCount()).To(Equal(3)) })
		It("settles both reservations", func() {
			Expect(f.reservationStatuses()).To(Equal(repeated(store.ReservationStatusCharged, 2)))
		})
		It("records one receipt per settled child", func() {
			Expect(f.events(f.parent.ID, TypeDelegationCompleted)).To(HaveLen(2))
		})
	})

	Describe("a parent cancelled while four children run and four wait", func() {
		var out parallelOutcome
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			ps.gate("task-0", "task-1", "task-2", "task-3")
			cctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			done := f.start(cctx, tasksJSON(8))
			f.awaitEntered(4)
			cancel(&CancelCause{Reason: "sigterm"})
			out = awaitOutcome(done)
		})
		It("cancels all eight entries", func() { Expect(out.statuses()).To(Equal(repeated("cancelled", 8))) })
		It("gives every entry the parent's reason", func() { Expect(out.reasons()).To(Equal(repeated("sigterm", 8))) })
		It("creates sessions for the running children only", func() { Expect(f.sessionCount()).To(Equal(5)) })
		It("never starts a waiting task", func() { Expect(ps.startedCount()).To(Equal(4)) })
		It("leaves no child running when the call returns", func() {
			_, running, _ := ps.counts()
			Expect(running).To(BeZero())
		})
		It("settles every reservation", func() {
			Expect(f.reservationStatuses()).To(Equal(repeated(store.ReservationStatusCharged, 4)))
		})
		It("ends the running children as cancelled", func() {
			Expect(f.childStatuses()).To(Equal(repeated("cancelled", 4)))
		})
	})

	Describe("a child that panics", func() {
		var out parallelOutcome
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			ps.explode["task-1"] = true
			out = awaitOutcome(f.start(context.Background(), tasksJSON(4)))
		})
		It("re-raises the panic from the call", func() { Expect(out.panicValue).To(Equal("provider exploded")) })
		It("lets the siblings complete and fails the panicking child", func() {
			Expect(f.childStatuses()).To(Equal([]string{"completed", "completed", "completed", "failed"}))
		})
		It("settles every reservation", func() {
			Expect(f.reservationStatuses()).To(Equal(repeated(store.ReservationStatusCharged, 4)))
		})
		It("charges the panicking child's whole grant and the siblings one turn each", func() {
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 6))
		})
		It("records a receipt for every child", func() {
			Expect(f.events(f.parent.ID, TypeDelegationCompleted)).To(HaveLen(4))
		})
	})

	Describe("a child with a long message", func() {
		It("caps that entry's text", func() {
			f = newParallelFixture(ps, 3, nil)
			ps.text["task-0"] = strings.Repeat("x", delegateTextMaxBytes+10)
			out := awaitOutcome(f.start(context.Background(), tasksJSON(2)))
			Expect(out.results()[0].Text).To(ContainSubstring("[truncated"))
		})
	})

	Describe("the parent's one writer", func() {
		It("takes one receipt at a time", func() {
			f = newParallelFixture(ps, 3, nil)
			sink := &gateSink{gate: make(chan struct{})}
			done := f.start(withParentEvents(context.Background(), sink), tasksJSON(4))
			DeferCleanup(func() { close(sink.gate); _ = awaitOutcome(done) })
			pollUntil("the first receipt", func() bool { return sink.inside.Load() >= 1 })
			Consistently(sink.inside.Load).WithTimeout(200 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(Equal(int32(1)))
		})
		It("ends the call with the event persistence failure once every child finished", func() {
			f = newParallelFixture(ps, 3, nil)
			out := awaitOutcome(f.start(withParentEvents(context.Background(), receiptFailSink{}), tasksJSON(3)))
			Expect(out.err).To(MatchError(tool.ErrEventAppend))
		})
		It("lets every child finish before reporting that failure", func() {
			f = newParallelFixture(ps, 3, nil)
			_ = awaitOutcome(f.start(withParentEvents(context.Background(), receiptFailSink{}), tasksJSON(3)))
			Expect(f.childStatuses()).To(Equal(repeated("completed", 3)))
		})
	})

	Describe("under the parent's loop", func() {
		var parentTurns int64
		var events []store.RecordedEvent
		BeforeEach(func() {
			f = newParallelFixture(ps, 3, nil)
			call := delegateCall(tasksJSON(4))
			ps.parent = &call
			registry := tool.NewRegistry()
			check(registry.Register(f.tool))
			res := must(RunExecLoop(context.Background(), ExecLoopConfig{
				Store: f.st, Session: f.parent, Streamer: ps, Registry: registry, Allowlist: []string{"delegate"},
				Pool: f.pool, TurnBudget: 1, LeaseItem: "exec:" + f.parent.ID, LeaseAgent: "cli", MaxTurns: 4,
			}))
			parentTurns = res.Turns
			events = must(f.st.Replay(context.Background(), f.parent.ID))
		})
		It("lets the parent run finish its two turns", func() { Expect(parentTurns).To(Equal(int64(2))) })
		It("writes one receipt per task with distinct indexes", func() {
			idx := f.receiptIndexes()
			slices.Sort(idx)
			Expect(idx).To(Equal([]int{0, 1, 2, 3}))
		})
		It("writes the tool result once", func() {
			n := 0
			for _, e := range events {
				if e.Type == tool.TypeToolResult {
					n++
				}
			}
			Expect(n).To(Equal(1))
		})
		It("writes the tool result after every receipt", func() {
			var last, result int64
			for _, e := range events {
				switch e.Type {
				case TypeDelegationCompleted:
					last = e.Seq
				case tool.TypeToolResult:
					result = e.Seq
				}
			}
			Expect(result).To(BeNumerically(">", last))
		})
		It("keeps the sequence numbers strictly increasing", func() {
			seqs := make([]int64, len(events))
			for i, e := range events {
				seqs[i] = e.Seq
			}
			Expect(slices.IsSorted(seqs) && len(slices.Compact(slices.Clone(seqs))) == len(seqs)).To(BeTrue())
		})
	})
})
