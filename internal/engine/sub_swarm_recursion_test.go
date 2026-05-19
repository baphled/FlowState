package engine_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	delegationpkg "github.com/baphled/flowstate/internal/delegation"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/session"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// recordingAppender is a session.MessageAppender stub that captures the
// (sessionID, agentID) pairs every AppendMessage call sees so the
// sub-swarm dispatch specs can pin "no persona-stamp leakage on the
// parent session". The leak shape is `AppendMessage(parentSessionID,
// {AgentID: memberID, ...})` — exactly what session
// 1e99f552-5223-4c38-8d83-225ea3ba16af.meta.json shows under jq
// `[.messages[].agentId] | unique` returning five non-coordinator
// personas on a single coordinator session with zero child sessions
// in `parent_id=<coord>` view.
type recordingAppender struct {
	mu    sync.Mutex
	stamp []appendedStamp
}

type appendedStamp struct {
	sessionID string
	agentID   string
	role      string
}

func newRecordingAppender() *recordingAppender { return &recordingAppender{} }

func (r *recordingAppender) AppendMessage(sessionID string, msg session.Message) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stamp = append(r.stamp, appendedStamp{
		sessionID: sessionID,
		agentID:   msg.AgentID,
		role:      msg.Role,
	})
}

func (r *recordingAppender) UpdateDelegation(string, string, func(*session.Message)) {}

func (r *recordingAppender) stampsForSession(id string) []appendedStamp {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]appendedStamp, 0, len(r.stamp))
	for _, s := range r.stamp {
		if s.sessionID == id {
			out = append(out, s)
		}
	}
	return out
}

// registerTwoLevelSwarm seeds reg with parent-swarm and child-swarm
// manifests so a member of the parent resolves to the child via
// swarm.Resolve.
func registerTwoLevelSwarm(reg *swarm.Registry) {
	reg.Register(&swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "parent-swarm",
		Lead:          "lead-a",
		Members:       []string{"child-swarm"},
		SwarmType:     swarm.SwarmTypeAnalysis,
	})
	reg.Register(&swarm.Manifest{
		SchemaVersion: "1.0.0",
		ID:            "child-swarm",
		Lead:          "lead-b",
		Members:       []string{"reviewer"},
		SwarmType:     swarm.SwarmTypeAnalysis,
	})
}

// trivialStreamer drains a single Done chunk, optionally counting
// its invocations on the supplied atomic.
func trivialStreamer(calls *atomic.Int32) streaming.Streamer {
	return streamerFunc(func(_ context.Context, _ string, _ string) (<-chan provider.StreamChunk, error) {
		if calls != nil {
			calls.Add(1)
		}
		ch := make(chan provider.StreamChunk, 1)
		ch <- provider.StreamChunk{Content: "ok", Done: true}
		close(ch)
		return ch, nil
	})
}

// buildEnginesForRecursion constructs the lead + sub-lead + reviewer
// engine triple plus the engines map the DelegateTool needs.
func buildEnginesForRecursion() (*engine.Engine, map[string]*engine.Engine) {
	lead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead"},
		Manifest: agent.Manifest{
			ID:                "lead-a",
			Name:              "Lead A",
			Instructions:      agent.Instructions{SystemPrompt: "lead"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	subLead := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "lead-b"},
		Manifest: agent.Manifest{
			ID:                "lead-b",
			Name:              "Lead B",
			Instructions:      agent.Instructions{SystemPrompt: "lead-b"},
			Delegation:        agent.Delegation{CanDelegate: true},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	reviewer := engine.New(engine.Config{
		ChatProvider: &mockProvider{name: "reviewer"},
		Manifest: agent.Manifest{
			ID:                "reviewer",
			Name:              "Reviewer",
			Instructions:      agent.Instructions{SystemPrompt: "reviewer"},
			ContextManagement: agent.DefaultContextManagement(),
		},
	})
	engines := map[string]*engine.Engine{
		"lead-a":   lead,
		"lead-b":   subLead,
		"reviewer": reviewer,
	}
	return lead, engines
}

var _ = Describe("SubSwarmRecursion", func() {
	Context("when a member of the parent resolves to a child swarm id", func() {
		It("recurses into the child swarm and dispatches the inner member", func() {
			reg := swarm.NewRegistry()
			registerTwoLevelSwarm(reg)
			lead, engines := buildEnginesForRecursion()

			parentManifest, _ := reg.Get("parent-swarm")
			parentCtx := swarm.NewContext(parentManifest.ID, parentManifest)
			lead.SetSwarmContext(&parentCtx)

			var reviewerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"reviewer": trivialStreamer(&reviewerCalls),
				"lead-b":   trivialStreamer(nil),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead-a").
				WithStreamers(streamers).
				WithSwarmRegistry(reg)

			err := delegateTool.DispatchSwarmMembers(context.Background(), &parentCtx, []string{"child-swarm"}, "go")

			Expect(err).NotTo(HaveOccurred())
			Expect(reviewerCalls.Load()).To(Equal(int32(1)))
		})

		It("propagates the depth through NestSubSwarm so the child carries Depth=2", func() {
			reg := swarm.NewRegistry()
			registerTwoLevelSwarm(reg)
			parentManifest, _ := reg.Get("parent-swarm")
			parentCtx := swarm.NewContext(parentManifest.ID, parentManifest)

			childCtx := parentCtx.NestSubSwarm("child-swarm")

			Expect(parentCtx.Depth).To(Equal(1))
			Expect(childCtx.Depth).To(Equal(2))
			Expect(childCtx.ChainPrefix).To(Equal("parent-swarm/child-swarm"))
		})
	})

	// Meta-Swarm Coordinator Architecture (May 2026) — Phase 3.
	//
	// When the coordinator (inside meta-swarm) calls
	// `delegate("a-team", brief)`, the delegate tool's Execute entry
	// point must route through the swarm-target dispatch branch
	// instead of the agent-engine lookup. Without this, Execute calls
	// resolveAgentID → ResolveByNameOrAlias → registry miss → error
	// because the agent registry has no `a-team` agent (and shouldn't —
	// `a-team` is a swarm).
	//
	// The swarm-target dispatch branch detects when subagent_type
	// resolves to a swarm in the swarm registry AND the active swarm
	// context lists that id in Members[], then fans out via
	// DispatchSwarmMembers with a child Context constructed from the
	// sub-swarm manifest.
	Context("when the delegate tool is invoked with a swarm-id target from inside a parent swarm", func() {
		It("dispatches the named sub-swarm's members and returns a tool.Result", func() {
			reg := swarm.NewRegistry()
			registerTwoLevelSwarm(reg)
			lead, engines := buildEnginesForRecursion()

			// Active swarm context = parent-swarm. Members lists
			// `child-swarm` as the swarm-id target.
			parentManifest, _ := reg.Get("parent-swarm")
			parentCtx := swarm.NewContext(parentManifest.ID, parentManifest)
			lead.SetSwarmContext(&parentCtx)

			var reviewerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"reviewer": trivialStreamer(&reviewerCalls),
				"lead-b":   trivialStreamer(nil),
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead-a").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithOwnerEngine(lead)

			result, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "child-swarm",
					"message":       "fan out the work",
				},
			})

			Expect(err).NotTo(HaveOccurred(),
				"delegate('child-swarm', ...) must take the swarm-dispatch branch — "+
					"agent-registry miss should NOT short-circuit to errAgentNotInAllowlist when the id is a swarm in Members[]")
			Expect(reviewerCalls.Load()).To(Equal(int32(1)),
				"the child swarm's reviewer member must have been dispatched once")
			Expect(result.Output).NotTo(BeEmpty(),
				"swarm-dispatch must synthesise a tool.Result.Output so the caller's transcript shows the delegation happened")
		})

		It("preserves the agent-target path when subagent_type is an agent in the active swarm's members", func() {
			// Regression pin: from inside a swarm whose Members[] lists
			// an AGENT (not a sub-swarm), delegate to that agent must
			// still take the agent-engine path. The swarm-dispatch
			// branch must not steal agent-target traffic.
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "review-swarm",
				Lead:          "lead-a",
				Members:       []string{"reviewer"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})
			lead, engines := buildEnginesForRecursion()

			ctx := swarm.NewContext("review-swarm", &swarm.Manifest{
				ID:      "review-swarm",
				Lead:    "lead-a",
				Members: []string{"reviewer"},
			})
			lead.SetSwarmContext(&ctx)

			var reviewerCalls atomic.Int32
			streamers := map[string]streaming.Streamer{
				"reviewer": trivialStreamer(&reviewerCalls),
			}

			agentReg := agent.NewRegistry()
			agentReg.Register(&agent.Manifest{
				ID:   "reviewer",
				Name: "Reviewer",
			})

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "lead-a").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithRegistry(agentReg).
				WithOwnerEngine(lead)

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "reviewer",
					"message":       "please review",
				},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(reviewerCalls.Load()).To(Equal(int32(1)),
				"the agent-target path must still fire — swarm-dispatch must not capture agent-id targets")
		})
	})

	// Delegation Regression Fix (May 2026).
	//
	// The swarm-target dispatch path (tryDispatchSwarmTarget →
	// DispatchSwarmMembers → buildMemberRunner closure → streamAndCollect)
	// historically routed every member's stream through the COORDINATOR's
	// ctx without rebinding session.IDKey to a per-member child session.
	// The accumulator on that path called
	// AppendMessage(coordinatorSessionID, {AgentID: memberID, ...}),
	// stamping non-coordinator personas onto coordinator-owned message
	// rows while no child session was ever spawned.
	//
	// Production symptom: session
	// 1e99f552-5223-4c38-8d83-225ea3ba16af.meta.json (May 2026) where
	// `jq '[.messages[].agentId] | unique'` returns
	// [analyst, coordinator, explorer, librarian, plan-reviewer,
	//  plan-writer] on ONE coordinator session, with
	// `grep -l '"parent_id":"1e99f552-..."'` returning no child sessions.
	//
	// Contract pinned: every member dispatched through the swarm-target
	// path MUST land on its own child session (parent_id = coordinator's
	// id, agent_id = memberID) — the coordinator session never sees a
	// non-coordinator AgentID stamp via AppendMessage.
	Context("when delegate routes through the swarm-target dispatch path", func() {
		It("spawns a child session per member with parent_id=coordinator, agent_id=memberID", func() {
			// Behaviour-Pinned: the swarm-target dispatch path must
			// spawn a child session per member via the
			// sessionCreator (the same seam executeSync uses for
			// agent-target delegations). Previously the swarm-target
			// path bypassed createChildSession entirely.
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "planning-loop",
				Lead:          "explorer",
				Members:       []string{"explorer", "librarian"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})

			coord := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "coord"},
				Manifest: agent.Manifest{
					ID:                "coordinator",
					Name:              "Coordinator",
					Instructions:      agent.Instructions{SystemPrompt: "coord"},
					Delegation:        agent.Delegation{CanDelegate: true},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			explorer := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "explorer"},
				Manifest:     agent.Manifest{ID: "explorer", Name: "Explorer", ContextManagement: agent.DefaultContextManagement()},
			})
			librarian := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "librarian"},
				Manifest:     agent.Manifest{ID: "librarian", Name: "Librarian", ContextManagement: agent.DefaultContextManagement()},
			})
			engines := map[string]*engine.Engine{
				"coordinator": coord,
				"explorer":    explorer,
				"librarian":   librarian,
			}

			coordCtx := swarm.NewContext("meta-swarm", &swarm.Manifest{
				ID:      "meta-swarm",
				Lead:    "coordinator",
				Members: []string{"planning-loop"},
			})
			coord.SetSwarmContext(&coordCtx)

			streamers := map[string]streaming.Streamer{
				"explorer":  trivialStreamer(nil),
				"librarian": trivialStreamer(nil),
			}
			mgr := session.NewManager(coord)
			appender := newRecordingAppender()
			mgr.RegisterSession("coord-session", "coordinator")

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "coordinator").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionCreator(mgr).
				WithSessionManager(mgr).
				WithMessageAppender(appender).
				WithOwnerEngine(coord)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "coord-session")
			_, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "planning-loop",
					"message":       "investigate auth-store coverage",
				},
			})
			Expect(err).NotTo(HaveOccurred(),
				"the swarm-target dispatch must succeed end-to-end")

			children, err := mgr.ChildSessions("coord-session")
			Expect(err).NotTo(HaveOccurred())
			Expect(children).To(HaveLen(2),
				"every member of planning-loop must spawn a child session anchored to the coordinator (parent_id=coord-session); "+
					"production session 1e99f552-5223-4c38-8d83-225ea3ba16af.meta.json shows zero child sessions despite five non-coordinator personas streaming")

			childAgents := []string{children[0].AgentID, children[1].AgentID}
			Expect(childAgents).To(ContainElements("explorer", "librarian"),
				"each spawned child session must carry agent_id matching the dispatched member id")

			for _, c := range children {
				Expect(c.ParentID).To(Equal("coord-session"),
					"every member's child session must be parented to the coordinator's session id")
			}
		})

		It("never stamps a non-coordinator agentId on the coordinator session via AppendMessage", func() {
			// Behaviour-Pinned: the coordinator's persisted message
			// log must contain ZERO rows with AgentID != "coordinator"
			// after a swarm-target delegate. Pre-fix the accumulator
			// on the swarm-target path stamped memberID onto rows
			// written to the coordinator session.
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "planning-loop",
				Lead:          "explorer",
				Members:       []string{"explorer", "librarian"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})

			coord := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "coord"},
				Manifest: agent.Manifest{
					ID:                "coordinator",
					Name:              "Coordinator",
					Delegation:        agent.Delegation{CanDelegate: true},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			explorer := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "explorer"},
				Manifest:     agent.Manifest{ID: "explorer", Name: "Explorer", ContextManagement: agent.DefaultContextManagement()},
			})
			librarian := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "librarian"},
				Manifest:     agent.Manifest{ID: "librarian", Name: "Librarian", ContextManagement: agent.DefaultContextManagement()},
			})
			engines := map[string]*engine.Engine{
				"coordinator": coord,
				"explorer":    explorer,
				"librarian":   librarian,
			}

			coordCtx := swarm.NewContext("meta-swarm", &swarm.Manifest{
				ID:      "meta-swarm",
				Lead:    "coordinator",
				Members: []string{"planning-loop"},
			})
			coord.SetSwarmContext(&coordCtx)

			streamers := map[string]streaming.Streamer{
				"explorer":  trivialStreamer(nil),
				"librarian": trivialStreamer(nil),
			}
			mgr := session.NewManager(coord)
			appender := newRecordingAppender()
			mgr.RegisterSession("coord-session", "coordinator")

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "coordinator").
				WithStreamers(streamers).
				WithSwarmRegistry(reg).
				WithSessionCreator(mgr).
				WithSessionManager(mgr).
				WithMessageAppender(appender).
				WithOwnerEngine(coord)

			ctx := context.WithValue(context.Background(), session.IDKey{}, "coord-session")
			_, err := delegateTool.Execute(ctx, tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "planning-loop",
					"message":       "investigate auth-store coverage",
				},
			})
			Expect(err).NotTo(HaveOccurred())

			coordStamps := appender.stampsForSession("coord-session")
			for _, s := range coordStamps {
				Expect(s.agentID).To(Equal("coordinator"),
					"AppendMessage to the coordinator session must only carry AgentID=coordinator; "+
						"pre-fix the swarm-target path stamped member personas (explorer/librarian/...) onto the coordinator's row store because ctx was never re-bound to a per-member child session before wrapWithAccumulator")
			}
		})
	})

	// Commit 3 — Gap C: the swarm-target dispatch path
	// (tryDispatchSwarmTarget at delegation.go:1344) historically fired
	// BEFORE prepareExecution, so a coordinator could route a swarm-id
	// delegate without hitting the shared gates the agent-target path
	// honours (circuit-breaker, can-delegate, spawn-limit). The fix
	// hoists circuit-breaker + can-delegate + spawn-limit checks into
	// a shared pre-flight that runs first; rejection-tracker stays
	// post-resolve on the agent path because it needs the resolved
	// target's chain id.
	//
	// The audit cited this gap; commit 1's engineer explicitly noted
	// it was deferred to commit 3.
	Context("when the swarm-target dispatch path is hit with gate-relevant input", func() {
		It("rejects with the same budget-limit error shape when spawn-limit is exhausted", func() {
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "planning-loop",
				Lead:          "explorer",
				Members:       []string{"explorer"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})

			coord := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "coord"},
				Manifest: agent.Manifest{
					ID:                "coordinator",
					Name:              "Coordinator",
					Delegation:        agent.Delegation{CanDelegate: true},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			explorer := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "explorer"},
				Manifest:     agent.Manifest{ID: "explorer", Name: "Explorer", ContextManagement: agent.DefaultContextManagement()},
			})
			coordCtx := swarm.NewContext("meta-swarm", &swarm.Manifest{
				ID:      "meta-swarm",
				Lead:    "coordinator",
				Members: []string{"planning-loop"},
			})
			coord.SetSwarmContext(&coordCtx)

			engines := map[string]*engine.Engine{
				"coordinator": coord,
				"explorer":    explorer,
			}
			bgManager := engine.NewBackgroundTaskManager()
			delegateTool := engine.NewDelegateToolWithBackground(engines, agent.Delegation{CanDelegate: true}, "coordinator", bgManager, nil).
				WithStreamers(map[string]streaming.Streamer{"explorer": trivialStreamer(nil)}).
				WithSwarmRegistry(reg).
				WithOwnerEngine(coord)

			limits := delegationpkg.DefaultSpawnLimits()
			limits.MaxTotalBudget = 1
			delegateTool.WithSpawnLimits(limits)

			// Fill the budget with a long-running background task so the
			// next delegate call hits the budget gate.
			bgManager.Launch(context.Background(), "blocking-task", "test-agent", "test", func(ctx context.Context) (string, error) {
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-time.After(10 * time.Second):
					return "done", nil
				}
			})

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "planning-loop",
					"message":       "go",
				},
			})

			Expect(err).To(HaveOccurred(),
				"swarm-target dispatch must honour the spawn-limit gate that the agent-target path enforces — pre-commit-3 it bypassed every gate by firing before prepareExecution")
			Expect(err.Error()).To(ContainSubstring("budget limit exceeded"),
				"the error message must match the agent-target path's failure shape so operator triage stays uniform regardless of which dispatch branch was hit")
		})

		It("rejects with circuit-breaker error after enough failures consecutively pop the breaker", func() {
			// Drive the breaker by exhausting it via the agent-target
			// path's recorded-failure seam (runStreamWithLegacyBreaker,
			// delegation.go:2458-2479) — note this seam fires only when
			// NO swarm context is active, so we pop the breaker first
			// and only then install the swarm context for the swarm-
			// target call. The breaker is a per-tool piece of state, so
			// any path that calls preFlightSharedGates must observe its
			// Allow() verdict regardless of swarm-context state.
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "planning-loop",
				Lead:          "explorer",
				Members:       []string{"explorer"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})

			coord := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "coord"},
				Manifest: agent.Manifest{
					ID:                "coordinator",
					Name:              "Coordinator",
					Delegation:        agent.Delegation{CanDelegate: true},
					ContextManagement: agent.DefaultContextManagement(),
				},
			})

			// Failing agent for the breaker-pop phase.
			failProvider := &mockProvider{name: "fail-provider", streamErr: fmt.Errorf("always fails")}
			failEngine := engine.New(engine.Config{
				ChatProvider: failProvider,
				Manifest: agent.Manifest{
					ID:                "fail-agent",
					Name:              "Fail Agent",
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			engines := map[string]*engine.Engine{
				"coordinator": coord,
				"fail-agent":  failEngine,
			}

			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: true}, "coordinator").
				WithSwarmRegistry(reg).
				WithOwnerEngine(coord).
				WithStreamers(map[string]streaming.Streamer{"explorer": trivialStreamer(nil)})

			// Phase 1: pop the breaker via agent-target failures
			// (swarm-context not yet set so RecordFailure fires on the
			// historical no-swarm seam).
			agentInput := tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "fail-agent",
					"message":       "fail please",
				},
			}
			for range 3 {
				_, _ = delegateTool.Execute(context.Background(), agentInput)
			}

			// Phase 2: install the swarm context so the swarm-target
			// branch can fire.
			coordCtx := swarm.NewContext("meta-swarm", &swarm.Manifest{
				ID:      "meta-swarm",
				Lead:    "coordinator",
				Members: []string{"planning-loop"},
			})
			coord.SetSwarmContext(&coordCtx)

			// Pre-commit-3 this skipped the open breaker entirely.
			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "planning-loop",
					"message":       "go",
				},
			})

			Expect(err).To(HaveOccurred(),
				"once the breaker is open, EVERY delegate call must short-circuit regardless of dispatch path; pre-commit-3 the swarm-target path silently bypassed the open breaker")
			Expect(err.Error()).To(ContainSubstring("circuit breaker open"),
				"the error message must match the agent-target path's failure shape — operators triage breaker incidents off this exact substring")
		})

		It("rejects with delegation-not-allowed when can_delegate is false even on the swarm-target path", func() {
			// Pre-commit-3 the swarm-target path bypassed the
			// d.delegation.CanDelegate check too. A coordinator that
			// somehow had CanDelegate=false should NOT be able to
			// dispatch a swarm-id delegate.
			reg := swarm.NewRegistry()
			reg.Register(&swarm.Manifest{
				SchemaVersion: "1.0.0",
				ID:            "planning-loop",
				Lead:          "explorer",
				Members:       []string{"explorer"},
				SwarmType:     swarm.SwarmTypeAnalysis,
			})

			coord := engine.New(engine.Config{
				ChatProvider: &mockProvider{name: "coord"},
				Manifest: agent.Manifest{
					ID:                "coordinator",
					Name:              "Coordinator",
					ContextManagement: agent.DefaultContextManagement(),
				},
			})
			coordCtx := swarm.NewContext("meta-swarm", &swarm.Manifest{
				ID:      "meta-swarm",
				Lead:    "coordinator",
				Members: []string{"planning-loop"},
			})
			coord.SetSwarmContext(&coordCtx)

			engines := map[string]*engine.Engine{"coordinator": coord}
			delegateTool := engine.NewDelegateTool(engines, agent.Delegation{CanDelegate: false}, "coordinator").
				WithStreamers(map[string]streaming.Streamer{"explorer": trivialStreamer(nil)}).
				WithSwarmRegistry(reg).
				WithOwnerEngine(coord)

			_, err := delegateTool.Execute(context.Background(), tool.Input{
				Name: "delegate",
				Arguments: map[string]interface{}{
					"subagent_type": "planning-loop",
					"message":       "go",
				},
			})

			Expect(err).To(HaveOccurred(),
				"can_delegate=false must veto swarm-target dispatch too; pre-commit-3 the swarm-target branch fired without consulting d.delegation.CanDelegate")
			Expect(err.Error()).To(ContainSubstring("delegation not allowed"),
				"the error message must match the agent-target path's failure shape so operators triage delegation-policy incidents off the same substring")
		})
	})
})
