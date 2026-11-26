package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// execAdmitAll admits every packet; execRejectAll refuses every packet.
type execAdmitAll struct{}

func (execAdmitAll) Admit(swarm.Packet, []byte) error { return nil }

type execRejectAll struct{}

func (execRejectAll) Admit(swarm.Packet, []byte) error { return errors.New("fixture rejection") }

// swarmExecFixture is a store, a scripted provider and a config whose gate
// executor writes a marker file, so a spec can tell whether a gate ran.
type swarmExecFixture struct {
	st      *store.Store
	dbPath  string
	scripts *nestStreamer
	cfg     ExecuteSwarmConfig
	marker  string
	gateErr error
	gateFn  func(ctx context.Context) error
}

// lapsedContext reports DeadlineExceeded, not Canceled, once its parent is done:
// a deadline that passes exactly when the spec's hook says so, with no sleep.
type lapsedContext struct{ context.Context }

func (c lapsedContext) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func swarmExecProfile(name string, tools ...string) agent.Profile {
	p := nestProfile(name, 2, tools...)
	if name == "coord" {
		p.MaxTurns = 6
	}
	return p
}

func newSwarmExecFixture() *swarmExecFixture {
	return newSwarmExecFixtureOn(must(store.OpenMemory()), "")
}

// newSwarmExecFileFixture is newSwarmExecFixture on a real database file under the spec's temp
// directory, so a spec can alter it through a second connection.
func newSwarmExecFileFixture() *swarmExecFixture {
	path := filepath.Join(GinkgoT().TempDir(), "swarm.db")
	return newSwarmExecFixtureOn(must(store.Open(path)), path)
}

func newSwarmExecFixtureOn(st *store.Store, path string) *swarmExecFixture {
	DeferCleanup(func() { _ = st.Close() })
	f := &swarmExecFixture{st: st, dbPath: path, scripts: &nestStreamer{
		byRole: map[string][][]provider.StreamEvent{}, byTask: map[string][][]provider.StreamEvent{},
		reqs: map[string][]*provider.ChatRequest{},
	}}
	f.marker = filepath.Join(GinkgoT().TempDir(), "gate-ran")
	workdir := GinkgoT().TempDir()
	gates := swarm.NewGateRunnerWithIn(workdir, func(ctx context.Context, _ string, _ []string) error {
		check(os.WriteFile(f.marker, []byte("ran"), 0o600))
		if f.gateFn != nil {
			return f.gateFn(ctx)
		}
		return f.gateErr
	})
	f.cfg = ExecuteSwarmConfig{
		Manifest: swarm.Manifest{
			Name: "demo", Scope: "demo", Coordinator: "coord",
			Packets: []swarm.Packet{
				{ID: "p1", Scope: "demo.a", Task: "write it", Role: "writer", SourceSHA256: swarm.SHA256HexForTest([]byte("write it"))},
				{ID: "p2", Scope: "demo.b", Task: "review it", Role: "reviewer", Prerequisites: []string{"p1"},
					SourceSHA256: swarm.SHA256HexForTest([]byte("review it"))},
			},
			Gates: []swarm.Gate{{Name: "go", Description: "builds", Command: "true"}},
		},
		Profiles: []agent.Profile{
			swarmExecProfile("coord", "delegate"), swarmExecProfile("writer", "fs.read"),
			swarmExecProfile("reviewer", "fs.read"), swarmExecProfile("rogue", "fs.read"),
		},
		Streamer: f.scripts, Provider: "scripted", ModelID: "scripted-model", Harness: "h", ConfigSHA: "sha",
		Workdir: workdir, Admitter: execAdmitAll{}, Gates: gates,
	}
	return f
}

// coordinate scripts the coordinator: one delegate call per entry (agent, task),
// then a final message.
func (f *swarmExecFixture) coordinate(final string, calls ...[2]string) {
	var turns [][]provider.StreamEvent
	for i, c := range calls {
		args := must(json.Marshal(map[string]string{"agent": c[0], "task": c[1]}))
		turns = append(turns, callTurn(provider.ToolCall{ID: "c" + string(rune('a'+i)), Name: "delegate", Arguments: args}))
	}
	f.scripts.byRole["coord-body"] = append(turns, finishText(final))
}

// both scripts the happy path: p1 to the writer, p2 to the reviewer.
func (f *swarmExecFixture) both(final string) {
	f.coordinate(final, [2]string{"writer", "[packet p1] write it"}, [2]string{"reviewer", "[packet p2] review it"})
}

func (f *swarmExecFixture) execute(ctx context.Context) ExecuteSwarmResult {
	res, err := ExecuteSwarm(ctx, f.st, f.cfg)
	if err != nil {
		panic(err)
	}
	return res
}

func (f *swarmExecFixture) gateRan() bool {
	_, err := os.Stat(f.marker)
	return err == nil
}

func (f *swarmExecFixture) sessionCount() int {
	return len(must(f.st.ListSessions(context.Background(), "")))
}

func (f *swarmExecFixture) storedRun() store.SwarmRun {
	runs := must(f.st.SwarmRunsFor(context.Background(), "demo"))
	if len(runs) != 1 {
		panic("expected exactly one stored run")
	}
	return runs[0]
}

// execStop is how a stopped run ended: its status, its reason and whether a gate ran.
type execStop struct {
	Status, Reason string
	GateRan        bool
}

type execChildFlat struct{ Profile, PacketID string }

func flatChildren(children []ExecutedChild) []execChildFlat {
	out := []execChildFlat{}
	for _, c := range children {
		out = append(out, execChildFlat{c.Profile, c.PacketID})
	}
	return out
}

// refusals are the preconditions: each mutates a valid config into a refused one.
func refusals() []TableEntry {
	mutate := func(fn func(*ExecuteSwarmConfig)) func(*ExecuteSwarmConfig) { return fn }
	return []TableEntry{
		Entry("an invalid manifest", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Name = "" })),
		Entry("no coordinator", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Coordinator = "" })),
		Entry("an unloaded coordinator", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Coordinator = "ghost" })),
		Entry("an unknown role", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Packets[0].Role = "ghost" })),
		Entry("a packet without a role", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Packets[0].Role = "" })),
		Entry("a coordinator without delegate", mutate(func(c *ExecuteSwarmConfig) { c.Profiles[0].Tools = []string{"fs.read"} })),
		Entry("no packet", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Packets = nil })),
		Entry("no gate", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Gates = nil })),
		Entry("the coordinator as a packet's role", mutate(func(c *ExecuteSwarmConfig) { c.Manifest.Packets[0].Role = "coord" })),
		Entry("a gate runner in another directory", mutate(func(c *ExecuteSwarmConfig) {
			c.Gates = swarm.NewGateRunnerWithIn(filepath.Join(c.Workdir, "elsewhere"), nil)
		})),
		Entry("a gate runner with no directory", mutate(func(c *ExecuteSwarmConfig) { c.Gates = swarm.NewGateRunnerWith(nil) })),
		Entry("no admitter", mutate(func(c *ExecuteSwarmConfig) { c.Admitter = nil })),
		Entry("no gate runner", mutate(func(c *ExecuteSwarmConfig) { c.Gates = nil })),
		Entry("no streamer", mutate(func(c *ExecuteSwarmConfig) { c.Streamer = nil })),
	}
}

var _ = Describe("ExecuteSwarm", func() {
	var (
		ctx context.Context
		f   *swarmExecFixture
	)
	BeforeEach(func() {
		ctx = context.Background()
		f = newSwarmExecFixture()
	})

	DescribeTable("refuses a precondition as a usage error", func(fn func(*ExecuteSwarmConfig)) {
		fn(&f.cfg)
		_, err := ExecuteSwarm(ctx, f.st, f.cfg)
		Expect(err).To(MatchError(ErrUsage))
	}, refusals())

	DescribeTable("creates no session when a precondition fails", func(fn func(*ExecuteSwarmConfig)) {
		fn(&f.cfg)
		_, _ = ExecuteSwarm(ctx, f.st, f.cfg)
		Expect(f.sessionCount()).To(BeZero())
	}, refusals())

	DescribeTable("records no run when a precondition fails", func(fn func(*ExecuteSwarmConfig)) {
		fn(&f.cfg)
		_, _ = ExecuteSwarm(ctx, f.st, f.cfg)
		Expect(must(f.st.SwarmRunsFor(ctx, "demo"))).To(BeEmpty())
	}, refusals())

	It("gives the coordinator the brief as its task", func() {
		f.both("done")
		res := f.execute(ctx)
		events := must(f.st.Replay(ctx, res.CoordinatorID))
		Expect(payloadFieldText(events[0].PayloadJSON, "task")).To(Equal(swarm.Brief(f.cfg.Manifest)))
	})

	It("attributes each child to its packet", func() {
		f.both("done")
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal(
			[]execChildFlat{{"writer", "p1"}, {"reviewer", "p2"}}))
	})

	It("records the attributed children on the stored run", func() {
		f.both("done")
		f.execute(ctx)
		ids := []string{}
		for _, c := range f.storedRun().Children {
			ids = append(ids, c.PacketID)
		}
		Expect(ids).To(Equal([]string{"p1", "p2"}))
	})

	It("records a child without a marker unattributed", func() {
		f.coordinate("done", [2]string{"writer", "write it"}, [2]string{"reviewer", "[packet p2] review it"})
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal(
			[]execChildFlat{{"writer", ""}, {"reviewer", "p2"}}))
	})

	It("records a child whose profile is not the packet's role unattributed", func() {
		f.coordinate("done", [2]string{"reviewer", "[packet p1] write it"})
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal([]execChildFlat{{"reviewer", ""}}))
	})

	It("stores an unattributed child with an empty packet id", func() {
		f.coordinate("done", [2]string{"writer", "[packet nope] write it"})
		f.execute(ctx)
		Expect(f.storedRun().Children[0].PacketID).To(BeEmpty())
	})

	It("records a child whose task lacks the packet's task text unattributed", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1] say hello and stop"})
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal([]execChildFlat{{"writer", ""}}))
	})

	It("does not accept a packet whose child did not do the packet's work", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1] say hello and stop"}, [2]string{"reviewer", "[packet p2] review it"})
		Expect(f.execute(ctx).Packets[0].Admitted).To(BeFalse())
	})

	It("attributes a child whose task text differs from the packet's only in whitespace", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1]  write\n\tit  now"})
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal([]execChildFlat{{"writer", "p1"}}))
	})

	It("does not admit a packet whose attributed child failed", func() {
		f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{{provError("model down")}}
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"})
		Expect(f.execute(ctx).Packets[0].Reason).To(ContainSubstring("is failed, not completed"))
	})

	It("names the first completed child as the one that admitted a packet attributed to two", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"}, [2]string{"writer", "[packet p1] write it again"})
		res := f.execute(ctx)
		Expect(res.Packets[0].Evidence).To(Equal("admitted by child " + res.Children[0].ID))
	})

	It("records both children of a packet attributed to it", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"}, [2]string{"writer", "[packet p1] write it again"})
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal([]execChildFlat{{"writer", "p1"}, {"writer", "p1"}}))
	})

	It("names the retry as the child that admitted a packet whose first child failed", func() {
		f.scripts.byTask["[packet p1] write it"] = [][]provider.StreamEvent{{provError("model down")}}
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"}, [2]string{"writer", "[packet p1] write it again"})
		res := f.execute(ctx)
		Expect(res.Packets[0].Evidence).To(Equal("admitted by child " + res.Children[1].ID))
	})

	It("does not record or attribute a grandchild's marker", func() {
		f.cfg.Profiles[1].Tools, f.cfg.Profiles[1].Delegates = []string{"delegate"}, []string{"reviewer"}
		args := must(json.Marshal(map[string]string{"agent": "reviewer", "task": "[packet p2] review it"}))
		f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{callTurn(provider.ToolCall{ID: "g", Name: "delegate", Arguments: args}),
			finishText("done")}
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"})
		Expect(flatChildren(f.execute(ctx).Children)).To(Equal([]execChildFlat{{"writer", "p1"}}))
	})

	It("records a packet with no child as not run", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"})
		f.execute(ctx)
		Expect(f.storedRun().Packets[1].Reason).To(HavePrefix("not run"))
	})

	It("does not accept a swarm with a packet that was not run", func() {
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"})
		Expect(f.execute(ctx).Status).To(Equal(SwarmExecNotAccepted))
	})

	It("accepts a completed swarm whose gates pass and whose packets are admitted", func() {
		f.both("done")
		Expect(f.execute(ctx).Status).To(Equal(SwarmExecAccepted))
	})

	It("stores an accepted run as pass", func() {
		f.both("done")
		f.execute(ctx)
		Expect(f.storedRun().Outcome).To(Equal(store.SwarmRunOutcomePass))
	})

	It("runs the gates itself and records its own result, not the model's claim", func() {
		f.gateErr = errors.New("the build is red")
		f.both("all gates passed, ship it")
		Expect(f.execute(ctx).Gates).To(Equal([]ExecutedGate{{Name: "go", Passed: false}}))
	})

	It("does not accept a swarm whose gate fails although the coordinator claims success", func() {
		f.gateErr = errors.New("the build is red")
		f.both("all gates passed, ship it")
		Expect(f.execute(ctx).Status).To(Equal(SwarmExecGateFailed))
	})

	It("stores a gate failure as fail", func() {
		f.gateErr = errors.New("the build is red")
		f.both("all gates passed")
		f.execute(ctx)
		Expect(f.storedRun().Outcome).To(Equal(store.SwarmRunOutcomeFail))
	})

	It("does not accept a swarm the admitter rejects", func() {
		f.cfg.Admitter = execRejectAll{}
		f.both("done")
		Expect(f.execute(ctx).Status).To(Equal(SwarmExecNotAccepted))
	})

	It("records the admitter's reason for a rejected packet", func() {
		f.cfg.Admitter = execRejectAll{}
		f.both("done")
		Expect(f.execute(ctx).Packets[0].Reason).To(Equal("fixture rejection"))
	})

	It("fails the run, without running a gate, when the coordinator fails", func() {
		f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{{provError("provider down")}}
		f.execute(ctx)
		Expect(f.gateRan()).To(BeFalse())
	})

	It("reports a failed coordinator as a failed run", func() {
		f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{{provError("provider down")}}
		Expect(f.execute(ctx).Status).To(Equal(SwarmExecFailed))
	})

	It("stores a failed coordinator as a fail run with every packet not admitted", func() {
		f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{{provError("provider down")}}
		f.execute(ctx)
		Expect(f.storedRun().Packets).To(HaveLen(2))
	})

	It("fails the run with max_turns, without running a gate", func() {
		f.cfg.MaxTurns = 1
		f.both("done")
		res := f.execute(ctx)
		Expect(execStop{res.Status, res.Reason, f.gateRan()}).To(Equal(execStop{SwarmExecFailed, CodeMaxTurns, false}))
	})

	It("fails the run with output_truncated when the coordinator is cut off twice", func() {
		f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{lengthTurn("I"), lengthTurn("I")}
		res := f.execute(ctx)
		Expect(execStop{res.Status, res.Reason, f.gateRan()}).To(Equal(execStop{SwarmExecFailed, CodeOutputTruncated, false}))
	})

	It("carries ErrOutputTruncated as the cause of a coordinator cut off twice", func() {
		f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{lengthTurn("I"), lengthTurn("I")}
		Expect(errors.Is(f.execute(ctx).Cause, ErrOutputTruncated)).To(BeTrue())
	})

	It("names output_truncated in the reason of a packet whose child was cut off twice", func() {
		f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{lengthTurn("I"), lengthTurn("I")}
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"})
		Expect(f.execute(ctx).Packets[0].Reason).To(ContainSubstring("output_truncated"))
	})

	It("does not admit a packet whose child was cut off twice", func() {
		f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{lengthTurn("I"), lengthTurn("I")}
		f.coordinate("done", [2]string{"writer", "[packet p1] write it"})
		Expect(f.execute(ctx).Packets[0].Admitted).To(BeFalse())
	})

	It("carries ErrMaxTurns as the cause of a coordinator stopped by max_turns", func() {
		f.cfg.MaxTurns = 1
		f.both("done")
		Expect(errors.Is(f.execute(ctx).Cause, ErrMaxTurns)).To(BeTrue())
	})

	It("carries the provider's error as the cause of a failed coordinator", func() {
		f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{{provError("provider down")}}
		Expect(f.execute(ctx).Cause).To(MatchError(ContainSubstring("provider down")))
	})

	Describe("when the coordinator profile lists delegates", func() {
		It("refuses up front, as a usage error naming the role, a packet role it leaves out", func() {
			f.cfg.Profiles[0].Delegates = []string{"writer"}
			_, err := ExecuteSwarm(ctx, f.st, f.cfg)
			Expect(err).To(MatchError(ContainSubstring(`role "reviewer"`)))
		})
		It("creates no session when it refuses a packet role the list leaves out", func() {
			f.cfg.Profiles[0].Delegates = []string{"writer"}
			_, _ = ExecuteSwarm(ctx, f.st, f.cfg)
			Expect(f.sessionCount()).To(BeZero())
		})
		It("runs when the list covers every packet role", func() {
			f.cfg.Profiles[0].Delegates = []string{"writer", "reviewer"}
			f.both("done")
			Expect(f.execute(ctx).Status).To(Equal(SwarmExecAccepted))
		})
		It("runs on the manifest's roles when the list is empty", func() {
			f.cfg.Profiles[0].Delegates = []string{}
			f.both("done")
			Expect(f.execute(ctx).Status).To(Equal(SwarmExecAccepted))
		})
		It("keeps a profile the manifest does not name out of the allowlist", func() {
			coord := f.cfg.Profiles[0]
			coord.Delegates = []string{"writer", "reviewer", "rogue"}
			Expect(swarmAllowlist(f.cfg.Manifest, coord)).To(Equal([]string{"writer", "reviewer"}))
		})
		It("narrows the allowlist to the listed roles", func() {
			coord := f.cfg.Profiles[0]
			coord.Delegates = []string{"reviewer"}
			Expect(swarmAllowlist(f.cfg.Manifest, coord)).To(Equal([]string{"reviewer"}))
		})
	})

	It("fails the run when the budget is exhausted, without running a gate", func() {
		f.cfg.Budget = 1
		f.both("done")
		res := f.execute(ctx)
		Expect(execStop{res.Status, res.Reason, f.gateRan()}).To(Equal(execStop{SwarmExecFailed, "budget_exhausted", false}))
	})

	It("leaves a budget-exhausted coordinator paused", func() {
		f.cfg.Budget = 1
		f.both("done")
		Expect(must(f.st.GetSession(ctx, f.execute(ctx).CoordinatorID)).Status).To(Equal("paused"))
	})

	It("fails the run with reason deadline when the deadline passes during a child", func() {
		dctx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		f.scripts.hook = func(_ context.Context, role string) {
			if role == "writer-body" {
				cancel()
			}
		}
		f.both("done")
		res := f.execute(lapsedContext{dctx})
		Expect(execStop{res.Status, res.Reason, f.gateRan()}).To(Equal(execStop{SwarmExecFailed, "deadline", false}))
	})

	It("fails the run with reason deadline, and no error, when the deadline passes during a gate", func() {
		dctx, cancel := context.WithCancel(ctx)
		DeferCleanup(cancel)
		f.gateFn = func(gctx context.Context) error {
			cancel()
			return gctx.Err()
		}
		f.both("done")
		res := f.execute(lapsedContext{dctx})
		Expect(execStop{res.Status, res.Reason, f.gateRan()}).To(Equal(execStop{SwarmExecFailed, "deadline", true}))
	})

	Describe("when the run is cancelled during a gate", func() {
		var res ExecuteSwarmResult
		BeforeEach(func() {
			cctx, cancel := context.WithCancelCause(ctx)
			DeferCleanup(func() { cancel(nil) })
			f.gateFn = func(gctx context.Context) error {
				cancel(&CancelCause{Reason: "sigint"})
				return gctx.Err()
			}
			f.both("done")
			res = f.execute(cctx)
		})

		It("reports a cancelled run, not an accepted one", func() {
			Expect([]string{res.Status, res.Reason}).To(Equal([]string{SwarmExecCancelled, "sigint"}))
		})

		It("records the gate as interrupted and not passed", func() {
			Expect(res.Gates).To(Equal([]ExecutedGate{{Name: "go", Interrupted: true}}))
		})

		It("stores the run as fail with the packets admitted and no gate rows", func() {
			Expect(f.storedRun().Outcome).To(Equal(store.SwarmRunOutcomeFail))
		})
	})

	It("does not let the coordinator spawn a profile the manifest does not name", func() {
		f.coordinate("done", [2]string{"rogue", "[packet p1] write it"})
		Expect(f.execute(ctx).Children).To(BeEmpty())
	})

	Describe("when the run is cancelled while a child runs", func() {
		var res ExecuteSwarmResult
		BeforeEach(func() {
			cctx, cancel := context.WithCancelCause(ctx)
			DeferCleanup(func() { cancel(nil) })
			f.scripts.hook = func(_ context.Context, role string) {
				if role == "writer-body" {
					cancel(&CancelCause{Reason: "sigint"})
				}
			}
			f.both("done")
			res = f.execute(cctx)
		})

		It("reports one cancelled run with the one reason", func() {
			Expect([]string{res.Status, res.Reason}).To(Equal([]string{SwarmExecCancelled, "sigint"}))
		})

		It("settles every session as cancelled", func() {
			statuses := []string{}
			for _, s := range must(f.st.ListSessions(ctx, "")) {
				statuses = append(statuses, s.Status)
			}
			Expect(statuses).To(Equal([]string{"cancelled", "cancelled"}))
		})

		It("gives the child the same reason on its final event", func() {
			events := must(f.st.Replay(ctx, res.Children[0].ID))
			Expect(payloadFieldText(events[len(events)-1].PayloadJSON, "reason")).To(Equal("sigint"))
		})

		It("runs no gate", func() {
			Expect(f.gateRan()).To(BeFalse())
		})

		It("stores the run as fail", func() {
			Expect(f.storedRun().Outcome).To(Equal(store.SwarmRunOutcomeFail))
		})
	})
})

// seqStreamer plays a scripted sequence per role: the nth request of a role (its system prompt)
// gets the nth script, whatever the history holds, as a compaction rewrites the history; an
// exhausted script answers "done". The summariser is the role compactSystemPrompt.
type seqStreamer struct {
	mu    sync.Mutex
	seqs  map[string][][]provider.StreamEvent
	calls map[string]int
}

func (*seqStreamer) Name() string     { return "scripted" }
func (*seqStreamer) Models() []string { return []string{"scripted-model"} }
func (*seqStreamer) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ignored"}, nil
}

func (s *seqStreamer) Stream(_ context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	role := req.Messages[0].Content
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.calls[role]
	s.calls[role]++
	if n < len(s.seqs[role]) {
		return streamOf(s.seqs[role][n]...)
	}
	return streamOf(finishText("done")...)
}

// summaries is how many summary calls the run made.
func (s *seqStreamer) summaries() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[compactSystemPrompt]
}

// usedCall is a tool-call turn that reports a context of ctxTokens.
func usedCall(c provider.ToolCall, ctxTokens int64) []provider.StreamEvent {
	finish := provider.StreamEvent{Kind: provider.StreamEventFinish, FinishReason: "stop",
		Usage: &provider.Usage{InputTokens: ctxTokens}}
	return []provider.StreamEvent{provCall(c), finish}
}

// compactingSwarm scripts the swarm so that the coordinator (when coordOver) and the writer (when
// childOver) cross a line of 900 tokens on their second turn; the summariser answers "s".
func (f *swarmExecFixture) compactingSwarm(coordOver, childOver bool) *seqStreamer {
	delegate := func(id, role, task string, ctxTokens int64) []provider.StreamEvent {
		args := must(json.Marshal(map[string]string{"agent": role, "task": task}))
		return usedCall(provider.ToolCall{ID: id, Name: "delegate", Arguments: args}, ctxTokens)
	}
	read := func(id string, ctxTokens int64) []provider.StreamEvent {
		return usedCall(provider.ToolCall{ID: id, Name: "fs.read", Arguments: json.RawMessage(`{"path":"missing.txt"}`)}, ctxTokens)
	}
	second := map[bool]int64{true: 950, false: 100}
	seq := &seqStreamer{calls: map[string]int{}, seqs: map[string][][]provider.StreamEvent{
		"coord-body": {delegate("ca", "writer", "[packet p1] write it", 100),
			delegate("cb", "reviewer", "[packet p2] review it", second[coordOver]), summaryTurn("s"), finishText("done")},
		"writer-body":       {read("r1", 100), read("r2", second[childOver]), summaryTurn("s"), finishText("written")},
		compactSystemPrompt: {summaryTurn("s")},
	}}
	f.cfg.Streamer = seq
	return seq
}

// withWindows gives the named profiles a context_window and the config the small reserve and
// keep_recent the specs use, so that a window of 1000 puts the line at 900.
func (f *swarmExecFixture) withWindows(enabled *bool, windows map[string]int) {
	for i := range f.cfg.Profiles {
		f.cfg.Profiles[i].ContextWindow = windows[f.cfg.Profiles[i].Name]
		if f.cfg.Profiles[i].Name == "writer" {
			f.cfg.Profiles[i].MaxTurns = 4
		}
	}
	f.cfg.Compaction = CompactionConfig{Enabled: enabled, Reserve: intPtr(100), KeepRecent: intPtr(1)}
}

// crossDefaultLine makes the second turn of the role hold more than the default keep_recent (20000
// tokens), so a cut exists before it, and report 90000 tokens: over the default line of a window of 100000.
func crossDefaultLine(seq *seqStreamer, role string) {
	turn := append([]provider.StreamEvent{provText(strings.Repeat("a", 200000))}, seq.seqs[role][1]...)
	turn[2].Usage.InputTokens = 90000
	seq.seqs[role][1] = turn
}

var _ = Describe("Compaction in an executed swarm", func() {
	var (
		f   *swarmExecFixture
		seq *seqStreamer
		res ExecuteSwarmResult
		off bool
	)
	compactions := func(id string) int {
		n := 0
		for _, t := range eventTypes(f.st, id) {
			if t == TypeContextCompacted {
				n++
			}
		}
		return n
	}
	BeforeEach(func() {
		f, off = newSwarmExecFixture(), false
	})

	Context("with compaction.enabled false and only the coordinator over its default line", func() {
		BeforeEach(func() {
			// Only enabled is set: were the object not passed on, the defaults would compact the coordinator.
			f.withWindows(&off, map[string]int{"coord": 100000})
			f.cfg.Compaction = CompactionConfig{Enabled: &off}
			seq = f.compactingSwarm(true, false)
			crossDefaultLine(seq, "coord-body")
			res = f.execute(context.Background())
		})
		It("never calls the summariser for the coordinator", func() { Expect(seq.summaries()).To(BeZero()) })
	})

	Context("with compaction.enabled false and only a child over its default line", func() {
		BeforeEach(func() {
			// Only enabled is set: were the object not passed on, the defaults would compact the child.
			f.withWindows(&off, map[string]int{"writer": 100000})
			f.cfg.Compaction = CompactionConfig{Enabled: &off}
			seq = f.compactingSwarm(false, true)
			crossDefaultLine(seq, "writer-body")
			res = f.execute(context.Background())
		})
		It("never calls the summariser for the child", func() { Expect(seq.summaries()).To(BeZero()) })
	})

	Context("with a coordinator profile window and no configured window", func() {
		BeforeEach(func() {
			f.withWindows(nil, map[string]int{"coord": 1000})
			// The writer has no window and reports 950 tokens: it would cross the line were a window to leak.
			seq = f.compactingSwarm(true, true)
			res = f.execute(context.Background())
		})
		It("compacts the coordinator by its profile's window", func() {
			Expect(compactions(res.CoordinatorID)).To(Equal(1))
		})
		It("leaves the children uncompacted", func() { Expect(compactions(res.Children[0].ID)).To(BeZero()) })
	})

	Context("with a child profile window and a coordinator with none", func() {
		BeforeEach(func() {
			f.withWindows(nil, map[string]int{"writer": 1000})
			// The coordinator has no window and reports 950 tokens: it would cross the line were a window to leak.
			seq = f.compactingSwarm(true, true)
			res = f.execute(context.Background())
		})
		It("compacts the child by its profile's window", func() { Expect(compactions(res.Children[0].ID)).To(Equal(1)) })
		It("leaves the coordinator uncompacted", func() { Expect(compactions(res.CoordinatorID)).To(BeZero()) })
	})

	Context("with the configured window in the config and no profile window", func() {
		BeforeEach(func() {
			f.withWindows(nil, nil)
			f.cfg.Compaction.ContextWindow = intPtr(1000)
			seq = f.compactingSwarm(true, false)
			res = f.execute(context.Background())
		})
		It("compacts the coordinator by compaction.context_window", func() {
			Expect(compactions(res.CoordinatorID)).To(Equal(1))
		})
	})

	Context("with the compaction object left unset", func() {
		BeforeEach(func() {
			f.cfg.Compaction = CompactionConfig{}
			f.cfg.Profiles[0].ContextWindow = 100000
			seq = f.compactingSwarm(true, false)
			crossDefaultLine(seq, "coord-body")
			res = f.execute(context.Background())
		})
		It("applies the built-in reserve, so a line of 83616 is crossed by 90000", func() {
			Expect(compactions(res.CoordinatorID)).To(Equal(1))
		})
	})
})

var _ = Describe("The swarm coordinator's judged lease", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	Describe("while the coordinator runs", func() {
		var busy bool
		var owner string
		BeforeEach(func() {
			f := newSwarmExecFixture()
			f.both("done")
			f.scripts.hook = func(_ context.Context, role string) {
				if role != "coord-body" || busy {
					return
				}
				for _, s := range must(f.st.ListSessions(context.Background(), "")) {
					if s.ParentID == "" {
						busy, owner = leaseBusy(f.st, s.ID), leaseBusyOwner(f.st, s.ID)
					}
				}
			}
			f.execute(ctx)
		})
		It("refuses a second acquire of the coordinator's item", func() { Expect(busy).To(BeTrue()) })
		It("names a run owner as the holder", func() { Expect(owner).To(HavePrefix(runLeasePrefix)) })
		It("does not name the constant cli owner", func() { Expect(owner).NotTo(Equal("cli")) })
	})

	DescribeTable("is still held when the coordinator's completion is written",
		func(status string, script func(*swarmExecFixture)) {
			f := newSwarmExecFileFixture()
			script(f)
			installLeaseProbe(f.dbPath, status)
			res := f.execute(ctx)
			Expect(probedLeaseRows(f.dbPath, res.CoordinatorID)).To(Equal([]int{1}))
		},
		Entry("completed, in completeExecLoopRun", "completed", func(f *swarmExecFixture) { f.both("done") }),
		Entry("failed, in finishExecLoopState", "failed", func(f *swarmExecFixture) {
			f.scripts.byRole["coord-body"] = [][]provider.StreamEvent{{provError("upstream down")}}
		}),
	)

	It("leaves no lease row once the run is over", func() {
		f := newSwarmExecFixture()
		f.both("done")
		res := f.execute(ctx)
		Expect(f.st.LeaseToken(ctx, "exec:"+res.CoordinatorID)).To(BeZero())
	})

	It("refuses a coordinator whose session is held, writing nothing", func() {
		f := newSwarmExecFixture()
		sess := must(f.st.CreateSession(ctx, "coord-session", "scripted", "m", "h", "sha"))
		held := must(f.st.AcquireRunLease(ctx, "exec:"+sess.ID, "run:other"))
		DeferCleanup(func() { _ = held.Release(context.Background()) })
		_, err := acquireCoordinatorLease(ctx, f.st, sess.ID)
		Expect(err).To(MatchError(store.ErrSessionBusy))
	})
})

var _ = Describe("the swarm coordinator's hook set (ADR-012 D22)", func() {
	var cfg ExecuteSwarmConfig
	BeforeEach(func() { cfg = ExecuteSwarmConfig{Hooks: stubHookSet("root")} })

	It("carries the root set in the coordinator's loop config", func() {
		loop := coordinatorLoopConfig(nil, &store.Session{}, agent.Profile{}, cfg, nil, 1)
		Expect(loop.Hooks).To(Equal(HookSet(stubHookSet("root"))))
	})

	It("carries the root set in the coordinator's delegate tool, so a packet child inherits it", func() {
		tc := coordinatorToolConfig(nil, &store.Session{}, agent.Profile{}, cfg, nil, nil)
		Expect(tc.Hooks).To(Equal(HookSet(stubHookSet("root"))))
	})

	It("keeps an empty set empty in the coordinator's loop config", func() {
		loop := coordinatorLoopConfig(nil, &store.Session{}, agent.Profile{}, ExecuteSwarmConfig{}, nil, 1)
		Expect(loop.Hooks).To(BeNil())
	})

	It("keeps an empty set empty in the coordinator's delegate tool", func() {
		tc := coordinatorToolConfig(nil, &store.Session{}, agent.Profile{}, ExecuteSwarmConfig{}, nil, nil)
		Expect(tc.Hooks).To(BeNil())
	})
})

var _ = Describe("The protected hook trees of a swarm (ADR-012 D20)", func() {
	var (
		f    *swarmExecFixture
		user string
	)
	BeforeEach(func() {
		f = newSwarmExecFixture()
		user = userHooksUnder(f.cfg.Workdir)
	})

	Describe("the coordinator", func() {
		var res ExecuteSwarmResult
		BeforeEach(func() {
			f.cfg.Profiles[0].Tools = []string{"delegate", tool.FSWriteName}
			f.both("swarm done")
			f.scripts.byRole["coord-body"] = append([][]provider.StreamEvent{callTurn(writeCallTo("w0", user))}, f.scripts.byRole["coord-body"]...)
			res = f.execute(context.Background())
		})
		It("is refused the user hook file with fs_path_protected", func() {
			Expect(protectedResults(f.st, res.CoordinatorID)).To(HaveLen(1))
		})
		It("creates no user hook directory", func() {
			Expect(filepath.Join(f.cfg.Workdir, filepath.Dir(user))).NotTo(BeADirectory())
		})
	})

	Describe("a packet child", func() {
		var res ExecuteSwarmResult
		BeforeEach(func() {
			f.cfg.Profiles[1].Tools = []string{tool.FSWriteName}
			f.both("swarm done")
			f.scripts.byRole["writer-body"] = [][]provider.StreamEvent{callTurn(writeCallTo("w1", user)), finishText("writer done")}
			res = f.execute(context.Background())
		})
		It("is refused the user hook file with fs_path_protected", func() {
			var writer string
			for _, c := range res.Children {
				if c.Profile == "writer" {
					writer = c.ID
				}
			}
			Expect(protectedResults(f.st, writer)).To(HaveLen(1))
		})
		It("creates no user hook directory", func() {
			Expect(filepath.Join(f.cfg.Workdir, filepath.Dir(user))).NotTo(BeADirectory())
		})
	})
})
