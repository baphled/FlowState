package app

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/store"
	"github.com/baphled/flowstate/internal/tool"
)

// nestStreamer is a thread-safe fake provider for a whole delegation tree. A
// request's role is its system prompt (the profile body, "<name>-body"; the root
// has none and is "root"), and the turn is the number of assistant messages it
// already holds. A script is found by task first, then by role; a missing turn
// answers "done <task>". hook runs inside Stream, so a spec can hold a run there.
type nestStreamer struct {
	mu     sync.Mutex
	byRole map[string][][]provider.StreamEvent
	byTask map[string][][]provider.StreamEvent
	reqs   map[string][]*provider.ChatRequest
	hook   func(ctx context.Context, role string)
}

func (*nestStreamer) Name() string     { return "scripted" }
func (*nestStreamer) Models() []string { return []string{"scripted-model"} }
func (*nestStreamer) Chat(context.Context, *provider.ChatRequest) (*provider.ChatResponse, error) {
	return &provider.ChatResponse{Content: "ignored"}, nil
}

func (n *nestStreamer) Stream(ctx context.Context, req *provider.ChatRequest) <-chan provider.StreamEvent {
	role, task, turn := "root", "", 0
	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			role = m.Content
		case "user":
			if task == "" {
				task = m.Content
			}
		case "assistant":
			turn++
		}
	}
	n.mu.Lock()
	n.reqs[role] = append(n.reqs[role], req)
	script := n.byTask[task]
	if script == nil {
		script = n.byRole[role]
	}
	n.mu.Unlock()
	if n.hook != nil {
		n.hook(ctx, role)
	}
	if ctx.Err() != nil {
		return streamOf()
	}
	if turn < len(script) {
		return streamOf(script[turn]...)
	}
	return streamOf(finishText("done " + task)...)
}

// seen is the model-visible content of the last message the role was sent last.
func (n *nestStreamer) seen(role string) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	reqs := n.reqs[role]
	if len(reqs) == 0 {
		return ""
	}
	msgs := reqs[len(reqs)-1].Messages
	return msgs[len(msgs)-1].Content
}

// turns is how many provider requests the role made.
func (n *nestStreamer) turns(role string) int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.reqs[role])
}

// nestFixture is a root session whose real loop offers the delegate tool, with
// three profiles: lead (may delegate), specialist and plain (may not).
type nestFixture struct {
	*delegateFixture
	scripts  *nestStreamer
	tool     *DelegateTool
	registry *tool.Registry
	profiles []agent.Profile
}

func nestProfile(name string, turns int, tools ...string) agent.Profile {
	return agent.Profile{
		Name: name, Description: name, Tools: tools, Model: agent.InheritModel, MaxTurns: turns,
		Path: "/profiles/" + name + ".md", SHA256: "sha-" + name, Body: name + "-body",
	}
}

func newNestFixture(limit int) *nestFixture {
	f := &nestFixture{delegateFixture: newDelegateFixture(), scripts: &nestStreamer{
		byRole: map[string][][]provider.StreamEvent{}, byTask: map[string][][]provider.StreamEvent{},
		reqs: map[string][]*provider.ChatRequest{},
	}}
	f.profiles = []agent.Profile{
		nestProfile("lead", 6, "delegate"), nestProfile("specialist", 2, "fs.read"), nestProfile("plain", 2, "fs.read"),
	}
	f.profiles[0].Delegates = []string{"specialist", "lead"}
	f.tool = NewDelegateTool(DelegateToolConfig{
		Store: f.st, Parent: f.parent, Streamer: f.scripts, Pool: f.pool, Workdir: f.workdir,
		Profiles: f.profiles, DescendantLimit: limit,
	})
	f.registry = tool.NewRegistry()
	check(f.registry.Register(f.tool))
	return f
}

// addProfile loads one more profile into the fixture's tool.
func (f *nestFixture) addProfile(p agent.Profile) {
	f.profiles = append(f.profiles, p)
	f.tool.cfg.Profiles = append(f.tool.cfg.Profiles, p)
}

// delegatingDefinition is the description of the delegate tool the role was offered.
func (f *nestFixture) delegatingDefinition(role string) string {
	for _, d := range f.scripts.reqs[role][0].Tools {
		if d.Name == agent.DelegateTool {
			return d.Description
		}
	}
	panic("the role was not offered delegate")
}

// say scripts a role's turns; the root is "root", a profile its name.
func (f *nestFixture) say(name string, turns ...[]provider.StreamEvent) {
	if name != "root" {
		name += "-body"
	}
	f.scripts.byRole[name] = turns
}

// turnsOf sets the max_turns of a loaded profile.
func (f *nestFixture) turnsOf(name string, turns int) {
	for i := range f.tool.cfg.Profiles {
		if f.tool.cfg.Profiles[i].Name == name {
			f.tool.cfg.Profiles[i].MaxTurns = turns
		}
	}
}

// runRoot runs the root loop, which may call delegate, under ctx.
func (f *nestFixture) runRoot(ctx context.Context) (*ExecLoopResult, error) {
	lease, _ := acquireTestLease(f.st, "exec:"+f.parent.ID, "cli")
	return RunExecLoop(ctx, ExecLoopConfig{
		Store: f.st, Session: f.parent, Streamer: f.scripts, Registry: f.registry, Allowlist: []string{"delegate"},
		Pool: f.pool, TurnBudget: 1, Lease: lease, MaxTurns: 4,
	})
}

// startRoot runs the root loop in the background and returns its outcome channel.
func (f *nestFixture) startRoot(ctx context.Context) chan error {
	done := make(chan error, 1)
	go func() {
		_, err := f.runRoot(ctx)
		done <- err
	}()
	return done
}

// childOf is the id of the parent's only child.
func (f *nestFixture) childOf(parentID string) string {
	var out []string
	for _, s := range must(f.st.ListSessions(context.Background(), "")) {
		if s.ParentID == parentID {
			out = append(out, s.ID)
		}
	}
	if len(out) != 1 {
		panic("expected exactly one child of " + parentID)
	}
	return out[0]
}

// logField is the field of the single event of the type in the session's log.
func (f *nestFixture) logField(id, typ, field string) string {
	evs := f.events(id, typ)
	if len(evs) != 1 {
		panic("expected exactly one " + typ + " event")
	}
	return payloadFieldText(evs[0].PayloadJSON, field)
}

func delegates(args string) []provider.StreamEvent { return callTurn(delegateCall(args)) }

// awaitNest waits for a background root run.
func awaitNest(done chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		panic("the delegation tree did not return")
	}
}

// nestLeads starts four leads in one call; nestSpecs four specialists.
const nestLeads = `{"tasks":[{"agent":"lead","task":"l1"},{"agent":"lead","task":"l2"},{"agent":"lead","task":"l3"},` +
	`{"agent":"lead","task":"l4"}]}`
const nestSpecs = `{"tasks":[{"agent":"specialist","task":"s1"},{"agent":"specialist","task":"s2"},` +
	`{"agent":"specialist","task":"s3"},{"agent":"specialist","task":"s4"}]}`

// readmeProfile matches a profile the README writes with `cat > .../agents/NAME.md <<'EOF'`.
var readmeTools = []string{"delegate", "echo", "fs.read", "fs.write", "fs.edit", "exec.run"}

var readmeProfile = regexp.MustCompile(`(?s)agents/[a-z-]+\.md <<'EOF'\n(.*?)\n *EOF\n`)

var _ = Describe("Nested delegation", func() {
	var ctx context.Context
	BeforeEach(func() { ctx = context.Background() })

	Describe("a coordinator, a team lead and a specialist", func() {
		var f *nestFixture
		var lead, spec string
		BeforeEach(func() {
			f = newNestFixture(0)
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead summary"))
			f.say("specialist", finishText("specialist report"))
			must(f.runRoot(ctx))
			lead = f.childOf(f.parent.ID)
			spec = f.childOf(lead)
		})
		It("gives the lead the specialist's result", func() {
			Expect(f.scripts.seen("lead-body")).To(ContainSubstring("specialist report"))
		})
		It("gives the root the lead's final text", func() {
			Expect(f.scripts.seen("root")).To(ContainSubstring("lead summary"))
		})
		It("keeps the specialist's text out of the root's result", func() {
			Expect(f.scripts.seen("root")).NotTo(ContainSubstring("specialist report"))
		})
		It("completes the child and the grandchild", func() {
			Expect([]string{f.status(lead), f.status(spec)}).To(Equal([]string{"completed", "completed"}))
		})
		It("records the specialist's start in the lead's log", func() {
			Expect(f.logField(lead, TypeDelegationStarted, "child_id")).To(Equal(spec))
		})
		It("records the specialist's receipt in the lead's log", func() {
			Expect(f.logField(lead, TypeDelegationCompleted, "child_id")).To(Equal(spec))
		})
		It("records the lead's start in the root's log", func() {
			Expect(f.logField(f.parent.ID, TypeDelegationStarted, "child_id")).To(Equal(lead))
		})
		It("records the lead's receipt in the root's log", func() {
			Expect(f.logField(f.parent.ID, TypeDelegationCompleted, "child_id")).To(Equal(lead))
		})
		It("keeps the specialist's receipt out of the root's log", func() {
			Expect(f.events(f.parent.ID, TypeDelegationCompleted)).To(HaveLen(1))
		})
		It("returns the unspent grants up the chain to the root's pool", func() {
			// root 2 turns; lead 6 granted, 2 own + 1 for the specialist (granted 2, 1 returned) = 3 spent.
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 2 - 3))
		})
		It("charges the lead for its own turns and its descendants'", func() {
			Expect(f.reservation(lead).Consumed).To(Equal(int64(3)))
		})
		It("charges the specialist against the lead's pool", func() {
			Expect(f.reservation(spec).Consumed).To(Equal(int64(1)))
		})
		It("settles the specialist's reservation", func() {
			Expect(f.reservation(spec).Status).To(Equal(store.ReservationStatusCharged))
		})
	})

	Describe("a child whose profile does not list delegate", func() {
		It("has no delegate tool: it is neither registered nor on its allowlist", func() {
			f := newNestFixture(0)
			f.say("root", delegates(`{"agent":"plain","task":"try"}`), finishText("root done"))
			f.say("plain", delegates(`{"agent":"specialist","task":"read it"}`), finishText("plain done"))
			must(f.runRoot(ctx))
			Expect(f.scripts.seen("plain-body")).To(ContainSubstring("unknown tool: delegate"))
		})
	})

	Describe("a grandchild whose profile lists delegate", func() {
		var f *nestFixture
		BeforeEach(func() {
			f = newNestFixture(0)
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"lead","task":"again"}`), finishText("lead done"))
			must(f.runRoot(ctx))
		})
		It("is refused at spawn with the depth message", func() {
			Expect(f.scripts.seen("lead-body")).To(ContainSubstring("delegate is not available at depth 2: the nesting limit is 2"))
		})
		It("creates no session", func() { Expect(f.sessionCount()).To(Equal(2)) })
	})

	It("refuses a hand-made depth-3 attempt, creating nothing", func() {
		f := newNestFixture(0)
		c1 := must(CreateChildSession(ctx, f.st, f.parent.ID, "scripted", "m", "h", "s", "t"))
		c2 := must(CreateChildSession(ctx, f.st, c1.ID, "scripted", "m", "h", "s", "t"))
		req := f.request()
		req.Parent, req.Profile = c2, f.profiles[1]
		_, err := RunDelegatedChild(ctx, req)
		Expect(err).To(MatchError(ContainSubstring("delegation depth exceeds")))
	})

	Describe("a grandchild's directory", func() {
		var f *nestFixture
		BeforeEach(func() {
			f = newNestFixture(0)
			check(os.MkdirAll(filepath.Join(f.workdir, "sub"), 0o700))
			check(os.WriteFile(filepath.Join(f.workdir, "sub", "inside.txt"), []byte("CHILD-FILE"), 0o600))
			check(os.WriteFile(filepath.Join(f.workdir, "outside.txt"), []byte("ROOT-FILE"), 0o600))
			f.say("root", delegates(`{"agent":"lead","task":"plan it","cwd":"sub"}`), finishText("root done"))
		})
		It("is the child's, so the specialist reads the child's files", func() {
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			f.say("specialist", callTurn(provider.ToolCall{ID: "r1", Name: "fs.read", Arguments: []byte(`{"path":"inside.txt"}`)}), finishText("read"))
			must(f.runRoot(ctx))
			Expect(f.scripts.seen("specialist-body")).To(ContainSubstring("CHILD-FILE"))
		})
		It("never reaches the root's files", func() {
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			f.say("specialist", callTurn(provider.ToolCall{ID: "r1", Name: "fs.read", Arguments: []byte(`{"path":"../outside.txt"}`)}), finishText("read"))
			must(f.runRoot(ctx))
			Expect(f.scripts.seen("specialist-body")).NotTo(ContainSubstring("ROOT-FILE"))
		})
		It("cannot be moved above the child's with cwd", func() {
			f.say("lead", delegates(`{"agent":"specialist","task":"read it","cwd":".."}`), finishText("lead done"))
			must(f.runRoot(ctx))
			Expect(f.scripts.seen("lead-body")).To(ContainSubstring("inside the parent's"))
		})
	})

	Describe("the child's grant bounds its subtree", func() {
		It("refuses a grandchild's grant the child's pool cannot cover", func() {
			f := newNestFixture(0)
			f.turnsOf("lead", 3)
			f.turnsOf("specialist", 3)
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			must(f.runRoot(ctx))
			Expect(f.scripts.seen("lead-body")).To(ContainSubstring("budget_exhausted"))
		})
		It("never lets a child and its descendants spend more provider turns than its grant", func() {
			f := newNestFixture(0)
			f.turnsOf("lead", 4)
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			again := delegates(`{"agent":"specialist","task":"read it"}`)
			f.say("lead", again, again, again, again, again, again, finishText("lead done"))
			must(f.runRoot(ctx))
			Expect(f.scripts.turns("lead-body") + f.scripts.turns("specialist-body")).To(Equal(4))
		})
	})

	Describe("a lead and its specialist running at once", func() {
		var leadBusy, specBusy bool
		BeforeEach(func() {
			f := newNestFixture(0)
			f.scripts.hook = func(_ context.Context, role string) {
				if role != "specialist-body" {
					return
				}
				lead := f.childOf(f.parent.ID)
				leadBusy, specBusy = leaseBusy(f.st, lead), leaseBusy(f.st, f.childOf(lead))
			}
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			must(f.runRoot(ctx))
		})
		It("holds the lead's own lease", func() { Expect(leadBusy).To(BeTrue()) })
		It("holds the specialist's own lease", func() { Expect(specBusy).To(BeTrue()) })
	})

	Describe("a root cancelled while a grandchild runs", func() {
		var f *nestFixture
		var lead, spec string
		var err error
		BeforeEach(func() {
			f = newNestFixture(0)
			cctx, cancel := context.WithCancelCause(ctx)
			DeferCleanup(func() { cancel(nil) })
			entered := make(chan struct{})
			var once sync.Once
			f.scripts.hook = func(c context.Context, role string) {
				if role == "specialist-body" {
					once.Do(func() { close(entered) })
					<-c.Done()
				}
			}
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			done := f.startRoot(cctx)
			select {
			case <-entered:
			case <-time.After(20 * time.Second):
				panic("the specialist never started")
			}
			cancel(&CancelCause{Reason: "sigterm"})
			err = finishExecLoopState(cctx, f.st, f.parent, awaitNest(done))
			lead = f.childOf(f.parent.ID)
			spec = f.childOf(lead)
		})
		It("ends the root run as cancelled", func() { Expect(err).To(MatchError(ErrSessionCancelled)) })
		It("cancels the root session", func() { Expect(f.status(f.parent.ID)).To(Equal("cancelled")) })
		It("cancels the lead", func() { Expect(f.status(lead)).To(Equal("cancelled")) })
		It("cancels the specialist", func() { Expect(f.status(spec)).To(Equal("cancelled")) })
		It("gives the lead the root's reason", func() {
			Expect(f.logField(lead, session.TypeSessionCancelled, "reason")).To(Equal("sigterm"))
		})
		It("gives the specialist the root's reason", func() {
			Expect(f.logField(spec, session.TypeSessionCancelled, "reason")).To(Equal("sigterm"))
		})
		It("settles the lead's reservation", func() {
			Expect(f.reservation(lead).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("settles the specialist's reservation", func() {
			Expect(f.reservation(spec).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("returns everything unspent to the root's pool", func() {
			// root 1 turn; the lead spent 1 of its own and the specialist 1: 2 of the 6 granted.
			Expect(f.pool.Remaining()).To(Equal(delegateParentPool - 1 - 2))
		})
	})

	Describe("a grandchild that fails", func() {
		var f *nestFixture
		var lead string
		BeforeEach(func() {
			f = newNestFixture(0)
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead recovered"))
			f.say("specialist", []provider.StreamEvent{provError("boom")})
			must(f.runRoot(ctx))
			lead = f.childOf(f.parent.ID)
		})
		It("is an error result the lead can read", func() {
			Expect(f.scripts.seen("lead-body")).To(ContainSubstring("failed"))
		})
		It("lets the lead complete", func() { Expect(f.status(lead)).To(Equal("completed")) })
		It("gives the root the lead's final text", func() {
			Expect(f.scripts.seen("root")).To(ContainSubstring("lead recovered"))
		})
	})

	Describe("the cap on running descendants", func() {
		Describe("four children each starting four grandchildren", func() {
			var f *nestFixture
			var running, peak atomic.Int32
			var release chan struct{}
			var done chan error
			BeforeEach(func() {
				f = newNestFixture(0)
				f.turnsOf("lead", 12)
				release = make(chan struct{})
				running.Store(0)
				peak.Store(0)
				f.scripts.hook = func(c context.Context, role string) {
					if role == "root" {
						return
					}
					n := running.Add(1)
					defer running.Add(-1)
					for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
					}
					if role == "specialist-body" {
						select {
						case <-release:
						case <-c.Done():
						}
					}
				}
				f.say("root", delegates(nestLeads), finishText("root done"))
				f.say("lead", delegates(nestSpecs), finishText("lead done"))
				cctx, cancel := context.WithCancel(ctx)
				DeferCleanup(cancel)
				done = f.startRoot(cctx)
				pollUntil("eight grandchildren in flight", func() bool { return running.Load() >= 8 })
			})
			It("never runs a ninth while eight are running", func() {
				DeferCleanup(func() { close(release); _ = awaitNest(done) })
				Consistently(peak.Load).WithTimeout(150 * time.Millisecond).WithPolling(10 * time.Millisecond).Should(BeNumerically("<=", 8))
			})
			It("still runs every grandchild to the end", func() {
				close(release)
				check(awaitNest(done))
				Expect(f.scripts.turns("specialist-body")).To(Equal(16))
			})
			It("stays within eight over the whole run", func() {
				close(release)
				check(awaitNest(done))
				Expect(peak.Load()).To(BeNumerically("<=", 8))
			})
		})

		It("does not deadlock when every slot is first held by a child waiting for a grandchild", func() {
			f := newNestFixture(4) // four slots, four children each needing one more
			f.say("root", delegates(nestLeads), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"s1"}`), finishText("lead done"))
			cctx, cancel := context.WithCancel(ctx)
			DeferCleanup(cancel)
			done := f.startRoot(cctx)
			Eventually(done).WithTimeout(10 * time.Second).WithPolling(10 * time.Millisecond).Should(Receive(BeNil()))
		})
	})

	Describe("recovery across two levels", func() {
		var f *nestFixture
		var lead, spec string
		BeforeEach(func() {
			f = newNestFixture(0)
			f.profile = f.profiles[0]
			leadSession := f.seedChildID(f.parent, "lead-1", 1)
			f.profile = f.profiles[1]
			lead, spec = leadSession.ID, f.seedChildID(leadSession, "spec-1", 1).ID
			// The kill: the root's and the lead's delegate calls have their start and no result, and the
			// specialist's runner left its lease.
			for _, id := range []string{f.parent.ID, lead} {
				child := map[string]string{f.parent.ID: lead, lead: spec}[id]
				mustAppendEvent(ctx, f.st, id, "tool:delegate", tool.TypeToolCall, map[string]any{
					"tool": "delegate", "tool_call_id": "d0", "args": map[string]any{},
				})
				mustAppendEvent(ctx, f.st, id, "engine", TypeDelegationStarted, map[string]any{"child_id": child})
			}
			must(f.st.AcquireLease(ctx, "exec:"+lead, delegateLeasePrefix+"dead-lead"))
			must(f.st.AcquireLease(ctx, "exec:"+spec, delegateLeasePrefix+"dead-spec"))
			check(RecoverCrashedSession(ctx, f.st, f.parent.ID))
			unused := finishText("unused")
			// The replay folds a turn's calls, the crashed one included, into one assistant message, so a script starts after those.
			f.say("root", unused, delegates(`{"resume":"lead-1"}`), finishText("root done"))
			f.say("lead", unused, delegates(`{"resume":"spec-1"}`), finishText("lead done"))
			f.say("specialist", unused, finishText("specialist done"))
			must(f.runRoot(ctx))
		})
		It("completes the resumed lead", func() { Expect(f.status(lead)).To(Equal("completed")) })
		It("completes the resumed specialist", func() { Expect(f.status(spec)).To(Equal("completed")) })
		It("tells the lead which grandchild its crashed call started", func() {
			first := f.scripts.reqs["lead-body"][0].Messages
			Expect(first[len(first)-1].Content).To(ContainSubstring("spec-1 (active): resume it with delegate"))
		})
		It("gives the root the lead's final text", func() {
			Expect(f.scripts.seen("root")).To(ContainSubstring("lead done"))
		})
	})

	It("refuses to resume a child at the nesting limit whose profile lists delegate", func() {
		f := newNestFixture(0)
		f.profile = f.profiles[0]
		lead := f.seedChildID(f.parent, "lead-1", 1)
		deep := f.seedChildID(lead, "deep-1", 1)
		_, err := ResumeDelegatedChild(ctx, DelegateResumeRequest{
			DelegateRequest: DelegateRequest{Store: f.st, Parent: lead, Streamer: f.scripts, ParentWorkdir: f.workdir},
			ChildID:         deep.ID, Profiles: f.profiles,
		})
		Expect(err).To(MatchError(ContainSubstring("delegate is not available at depth 2")))
	})

	Describe("which profiles a delegating child may spawn", func() {
		var f *nestFixture
		BeforeEach(func() {
			f = newNestFixture(0)
			f.addProfile(nestProfile("rev", 3, "delegate", "fs.read"))
			f.addProfile(nestProfile("writer", 3, "fs.write"))
			f.say("root", delegates(`{"agent":"rev","task":"review"}`), finishText("root done"))
			f.say("writer", callTurn(provider.ToolCall{ID: "w1", Name: "fs.write",
				Arguments: []byte(`{"path":"pwned.txt","content":"x"}`)}), finishText("wrote"))
		})
		Describe("a profile that lists delegate and declares no delegates", func() {
			BeforeEach(func() {
				f.say("rev", delegates(`{"agent":"writer","task":"write it"}`), finishText("rev done"))
				must(f.runRoot(ctx))
			})
			It("is told it declares no delegates", func() {
				Expect(f.scripts.seen("rev-body")).To(ContainSubstring("profile rev lists delegate but declares no delegates"))
			})
			It("writes no file", func() {
				_, err := os.Stat(filepath.Join(f.workdir, "pwned.txt"))
				Expect(os.IsNotExist(err)).To(BeTrue())
			})
			It("creates no session for the writer", func() { Expect(f.sessionCount()).To(Equal(2)) })
		})
		Describe("a name outside the profile's delegates", func() {
			It("is refused for a single agent, naming both profiles", func() {
				f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
				f.say("lead", delegates(`{"agent":"writer","task":"write it"}`), finishText("lead done"))
				must(f.runRoot(ctx))
				Expect(f.scripts.seen("lead-body")).To(ContainSubstring("profile lead may not delegate to writer: it is not in lead's delegates"))
			})
			It("creates no session for a single agent", func() {
				f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
				f.say("lead", delegates(`{"agent":"writer","task":"write it"}`), finishText("lead done"))
				must(f.runRoot(ctx))
				Expect(f.sessionCount()).To(Equal(2))
			})
			It("refuses the whole tasks call for one bad entry", func() {
				f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
				f.say("lead", delegates(`{"tasks":[{"agent":"specialist","task":"a"},{"agent":"plain","task":"b"}]}`),
					finishText("lead done"))
				must(f.runRoot(ctx))
				Expect(f.scripts.seen("lead-body")).To(ContainSubstring("tasks[1]: usage: profile lead may not delegate to plain"))
			})
			It("starts no child of a refused tasks call, not even the allowed entry", func() {
				f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
				f.say("lead", delegates(`{"tasks":[{"agent":"specialist","task":"a"},{"agent":"plain","task":"b"}]}`),
					finishText("lead done"))
				must(f.runRoot(ctx))
				Expect(f.sessionCount()).To(Equal(2))
			})
			Describe("on resume", func() {
				BeforeEach(func() {
					f.profile = f.profiles[0]
					lead := f.seedChildID(f.parent, "lead-1", 1)
					f.profile = f.profiles[2] // plain, which the lead does not list
					f.seedChildID(lead, "plain-1", 1)
					unused := finishText("unused")
					f.say("root", delegates(`{"resume":"lead-1"}`), finishText("root done"))
					f.say("lead", unused, delegates(`{"resume":"plain-1"}`), finishText("lead done"))
					must(f.runRoot(ctx))
				})
				It("refuses a child spawned from a name the delegator does not list", func() {
					Expect(f.scripts.seen("lead-body")).To(ContainSubstring("profile lead may not delegate to plain"))
				})
				It("leaves the refused child active", func() { Expect(f.status("plain-1")).To(Equal("active")) })
			})
		})
		Describe("the tool's description", func() {
			BeforeEach(func() {
				f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
				f.say("lead", finishText("lead done"))
				must(f.runRoot(ctx))
			})
			It("lists the allowed profile to the nested child", func() {
				Expect(f.delegatingDefinition("lead-body")).To(ContainSubstring("- specialist:"))
			})
			It("omits a loaded profile the child may not spawn", func() {
				Expect(f.delegatingDefinition("lead-body")).NotTo(ContainSubstring("- writer:"))
			})
			It("lists every loaded profile to the root", func() {
				Expect(f.delegatingDefinition("root")).To(ContainSubstring("- writer:"))
			})
		})
		It("lets the root spawn a profile that no profile lists", func() {
			f.say("rev", finishText("rev done"))
			must(f.runRoot(ctx))
			Expect(f.status(f.childOf(f.parent.ID))).To(Equal("completed"))
		})
		It("refuses a delegates name that is not loaded, in a hand-built set", func() {
			t := NewDelegateTool(DelegateToolConfig{
				Store: f.st, Parent: f.parent, Streamer: f.scripts, Workdir: f.workdir, Profiles: f.profiles,
				DelegatorProfile: "lead", AllowedProfiles: []string{"ghost"},
			})
			_, err := t.Invoke(ctx, []byte(`{"agent":"ghost","task":"x"}`))
			Expect(err).To(MatchError(ContainSubstring("no agents are available")))
		})
		It("lets a delegating child spawn an allowed profile", func() {
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			must(f.runRoot(ctx))
			Expect(f.status(f.childOf(f.childOf(f.parent.ID)))).To(Equal("completed"))
		})
	})

	Describe("the README example", func() {
		var profiles []agent.Profile
		BeforeEach(func() {
			readme := must(os.ReadFile("../../README.md"))
			profiles = nil
			for _, m := range readmeProfile.FindAllStringSubmatch(strings.ReplaceAll(string(readme), "\r\n", "\n"), -1) { // Windows checks out CRLF
				text := strings.TrimLeft(strings.ReplaceAll(m[1], "\n  ", "\n"), " ")
				profiles = append(profiles, must(agent.Parse([]byte(text), readmeTools)))
			}
		})
		It("gives the team lead a delegates list naming its specialists", func() {
			var lead []string
			for _, p := range profiles {
				if p.Name == "team-lead" {
					lead = p.Delegates
				}
			}
			Expect(lead).To(Equal([]string{"reader", "reviewer"}))
		})
		It("loads as one set whose delegates are all present", func() {
			Expect(agent.CheckDelegates(profiles)).To(Succeed())
		})
	})

	Describe("a grandchild that panics", func() {
		var f *nestFixture
		var lead, spec string
		var recovered any
		BeforeEach(func() {
			f = newNestFixture(1)
			f.scripts.hook = func(_ context.Context, role string) {
				if role == "specialist-body" {
					panic("provider exploded")
				}
			}
			f.say("root", delegates(`{"agent":"lead","task":"plan it"}`), finishText("root done"))
			f.say("lead", delegates(`{"agent":"specialist","task":"read it"}`), finishText("lead done"))
			// A slot that is never released would deadlock the lead's reclaim: cancel after a bound.
			cctx, cancel := context.WithCancel(ctx)
			stop := time.AfterFunc(10*time.Second, cancel)
			DeferCleanup(func() { stop.Stop(); cancel() })
			func() {
				defer func() { recovered = recover() }()
				_, _ = f.runRoot(cctx)
			}()
			lead = f.childOf(f.parent.ID)
			spec = f.childOf(lead)
		})
		It("re-raises the panic to the host", func() { Expect(recovered).To(Equal("provider exploded")) })
		It("fails the specialist", func() { Expect(f.status(spec)).To(Equal("failed")) })
		It("fails the lead, which would otherwise stay active", func() { Expect(f.status(lead)).To(Equal("failed")) })
		It("records panic as the lead's reason", func() {
			Expect(f.logField(lead, TypeDelegationFailed, "error")).To(Equal("panic"))
		})
		It("settles the lead's reservation", func() {
			Expect(f.reservation(lead).Status).To(Equal(store.ReservationStatusCharged))
		})
		It("releases every descendant slot", func() { Expect(f.tool.cfg.limiter.sem).To(BeEmpty()) })
	})
})

var _ = Describe("hook set inheritance below the root (ADR-012 D22)", func() {
	It("starts a tool's children from the tool's root hook set", func() {
		t := NewDelegateTool(DelegateToolConfig{Hooks: stubHookSet("root")})
		Expect(t.baseRequest().Hooks).To(Equal(HookSet(stubHookSet("root"))))
	})

	It("starts no hooks for a tool that has none", func() {
		Expect(NewDelegateTool(DelegateToolConfig{}).baseRequest().Hooks).To(BeNil())
	})

	It("gives a nested child's delegate tool the root hook set", func() {
		registry := tool.NewRegistry()
		req := &DelegateRequest{Hooks: stubHookSet("root"), Profile: agent.Profile{Name: "child"}}
		if err := bindNestedDelegate(context.Background(), req, &delegateRun{child: &store.Session{}}, registry); err != nil {
			panic(err)
		}
		nested := must(registry.Get(agent.DelegateTool))
		Expect(nested.(*DelegateTool).cfg.Hooks).To(Equal(HookSet(stubHookSet("root"))))
	})
})

// stubHookSet is a HookSet whose digest is its own text, so a spec can tell two sets apart.
type stubHookSet string

func (s stubHookSet) Digest() string { return string(s) }

var _ = Describe("delegateLoopConfig hook set (ADR-012 D22)", func() {
	It("hands the request's root hook set to the child's loop", func() {
		req := &DelegateRequest{Hooks: stubHookSet("root")}
		cfg, err := delegateLoopConfig(context.Background(), req, &delegateRun{child: &store.Session{}, grant: 1}, nil)
		if err != nil {
			panic(err)
		}
		Expect(cfg.Hooks).To(Equal(HookSet(stubHookSet("root"))))
	})

	It("leaves the child's set empty when the root's is empty", func() {
		cfg, err := delegateLoopConfig(context.Background(), &DelegateRequest{}, &delegateRun{child: &store.Session{}, grant: 1}, nil)
		if err != nil {
			panic(err)
		}
		Expect(cfg.Hooks).To(BeNil())
	})
})

var _ = Describe("planResume hook set (ADR-012 D22)", func() {
	It("gives a resumed child's loop the root hook set the request carried", func() {
		f := newDelegateFixture(finishText("never"))
		child := f.seedChild(f.parent, 2)
		rr := f.resumeRequest(child.ID, "")
		rr.Hooks = stubHookSet("root")
		req := rr.DelegateRequest
		run, registry, err := planResume(context.Background(), &req, rr)
		if err != nil {
			panic(err)
		}
		cfg, err := delegateLoopConfig(context.Background(), &req, run, registry)
		if err != nil {
			panic(err)
		}
		Expect(cfg.Hooks).To(Equal(HookSet(stubHookSet("root"))))
	})
})
