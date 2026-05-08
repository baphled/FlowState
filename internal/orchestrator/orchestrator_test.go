package orchestrator_test

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/agent"
	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/orchestrator"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
	"testing"
)

func TestOrchestrator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Orchestrator Suite")
}

// fakeOrchestratorStreamer captures the agent id + message threaded to
// streaming.Run by the orchestrator. The streamed channel emits a
// single Done chunk so DispatchSwarm wraps up promptly.
type fakeOrchestratorStreamer struct {
	capturedAgentID string
	capturedMessage string
	chunks          []provider.StreamChunk
	err             error
}

func (f *fakeOrchestratorStreamer) Stream(_ context.Context, agentID string, message string) (<-chan provider.StreamChunk, error) {
	f.capturedAgentID = agentID
	f.capturedMessage = message
	if f.err != nil {
		return nil, f.err
	}
	out := make(chan provider.StreamChunk, len(f.chunks)+1)
	for _, c := range f.chunks {
		out <- c
	}
	out <- provider.StreamChunk{Done: true}
	close(out)
	return out, nil
}

// fakeOrchestratorEngine satisfies swarm.DispatchEngine plus the wider
// orchestrator engine surface (SetManifest, SetModelPreference,
// SetContextStore, MaybeCompactForModel) so the new lifecycle methods
// can be exercised in isolation. Records every invocation for
// assertion. Phase-5 Slice α added MaybeCompactForModel so SwitchModel
// can force-fire compaction before the engine swings the preference.
type fakeOrchestratorEngine struct {
	contexts      []*swarm.Context
	flushCalls    int
	snapshotCalls int
	restoreCalls  int
	skipFiles     bool

	// Lifecycle-method recorders.
	manifestSet           []agent.Manifest
	modelPrefProviders    []string
	modelPrefModels       []string
	contextStores         []*recall.FileContextStore
	contextStoreSessions  []string
	contextStoreCallCount int

	// Phase-5 Slice α — model-switch compaction trigger.
	// MaybeCompactForModel call recorder + invocation order trace.
	// orderTrace records the sequence of MaybeCompactForModel,
	// SetModelPreference (and any future switch-time calls) so the
	// orchestrator-side spec can pin the ordering invariant: the
	// trigger must run BEFORE the preference swings — otherwise the
	// engine resolves limits against the new model and the
	// estimated-vs-usable comparison becomes self-fulfilling.
	maybeCompactCalls     []fakeMaybeCompactCall
	maybeCompactReturn    string
	orderTrace            []string
}

type fakeMaybeCompactCall struct {
	sessionID string
	provider  string
	model     string
}

func (f *fakeOrchestratorEngine) SetSwarmContext(ctx *swarm.Context) {
	f.contexts = append(f.contexts, ctx)
}

func (f *fakeOrchestratorEngine) FlushSwarmLifecycle(_ context.Context) error {
	f.flushCalls++
	return nil
}

func (f *fakeOrchestratorEngine) ManifestSnapshot() any {
	f.snapshotCalls++
	return "pre-dispatch-token"
}

func (f *fakeOrchestratorEngine) RestoreManifest(_ any) {
	f.restoreCalls++
}

func (f *fakeOrchestratorEngine) SkipAgentFiles() bool        { return f.skipFiles }
func (f *fakeOrchestratorEngine) SetSkipAgentFiles(skip bool) { f.skipFiles = skip }

func (f *fakeOrchestratorEngine) SetManifest(m agent.Manifest) {
	f.manifestSet = append(f.manifestSet, m)
}

func (f *fakeOrchestratorEngine) SetModelPreference(providerName, modelName string) {
	f.modelPrefProviders = append(f.modelPrefProviders, providerName)
	f.modelPrefModels = append(f.modelPrefModels, modelName)
	f.orderTrace = append(f.orderTrace, "SetModelPreference")
}

func (f *fakeOrchestratorEngine) MaybeCompactForModel(_ context.Context, sessionID, providerName, modelName string) string {
	f.maybeCompactCalls = append(f.maybeCompactCalls, fakeMaybeCompactCall{
		sessionID: sessionID,
		provider:  providerName,
		model:     modelName,
	})
	f.orderTrace = append(f.orderTrace, "MaybeCompactForModel")
	return f.maybeCompactReturn
}

func (f *fakeOrchestratorEngine) SetContextStore(store *recall.FileContextStore, sessionID string) {
	f.contextStores = append(f.contextStores, store)
	f.contextStoreSessions = append(f.contextStoreSessions, sessionID)
	f.contextStoreCallCount++
}

// fakeSessionManager satisfies the orchestrator's SessionManager
// edge interface and records every UpdateSessionAgent / UpdateSessionModel
// call so the parity-fan-out tests can assert both the engine and the
// manager mutate together.
type fakeSessionManager struct {
	agentSessions   []string
	agentIDs        []string
	modelSessions   []string
	modelProviders  []string
	modelModels     []string
	updateAgentErr  error
	updateModelErr  error
}

func (f *fakeSessionManager) UpdateSessionAgent(sessionID, agentID string) error {
	f.agentSessions = append(f.agentSessions, sessionID)
	f.agentIDs = append(f.agentIDs, agentID)
	return f.updateAgentErr
}

func (f *fakeSessionManager) UpdateSessionModel(sessionID, providerName, modelName string) error {
	f.modelSessions = append(f.modelSessions, sessionID)
	f.modelProviders = append(f.modelProviders, providerName)
	f.modelModels = append(f.modelModels, modelName)
	return f.updateModelErr
}

// fakeSessionStore satisfies the orchestrator's SessionStore edge
// interface — Save / Load — and records inputs for assertion.
type fakeSessionStore struct {
	saved        []fakeSessionSaveCall
	loadStore    *recall.FileContextStore
	loadErr      error
	saveErr      error
}

type fakeSessionSaveCall struct {
	sessionID string
	store     *recall.FileContextStore
	meta      contextpkg.SessionMetadata
}

func (f *fakeSessionStore) Save(sessionID string, store *recall.FileContextStore, meta contextpkg.SessionMetadata) error {
	f.saved = append(f.saved, fakeSessionSaveCall{sessionID: sessionID, store: store, meta: meta})
	return f.saveErr
}

func (f *fakeSessionStore) Load(_ string) (*recall.FileContextStore, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return f.loadStore, nil
}

// fakeSessionStoreWithEvents mirrors fakeSessionStore but additionally
// satisfies the orchestrator's SwarmEventPersister capability so
// LoadSession / SaveTurnEnd can be exercised against the optional
// event-persistence path.
type fakeSessionStoreWithEvents struct {
	fakeSessionStore
	loadedEvents       []streaming.SwarmEvent
	loadEventsErr      error
	savedEventsBySess  map[string][]streaming.SwarmEvent
	saveEventsErr      error
	saveEventsCalls    int
}

func (f *fakeSessionStoreWithEvents) LoadEvents(_ string) ([]streaming.SwarmEvent, error) {
	if f.loadEventsErr != nil {
		return nil, f.loadEventsErr
	}
	return f.loadedEvents, nil
}

func (f *fakeSessionStoreWithEvents) SaveEvents(sessionID string, evs []streaming.SwarmEvent) error {
	f.saveEventsCalls++
	if f.savedEventsBySess == nil {
		f.savedEventsBySess = map[string][]streaming.SwarmEvent{}
	}
	f.savedEventsBySess[sessionID] = evs
	return f.saveEventsErr
}

// fakeStreamConsumer implements streaming.StreamConsumer minimally.
type fakeStreamConsumer struct {
	chunks []string
	err    error
	done   bool
}

func (f *fakeStreamConsumer) WriteChunk(content string) error {
	f.chunks = append(f.chunks, content)
	return nil
}
func (f *fakeStreamConsumer) WriteError(err error) { f.err = err }
func (f *fakeStreamConsumer) Done()                { f.done = true }

var _ = Describe("SessionOrchestrator", func() {
	var (
		registry *agent.Registry
		swarmReg *swarm.Registry
		streamer *fakeOrchestratorStreamer
		eng      *fakeOrchestratorEngine
		consumer *fakeStreamConsumer
	)

	BeforeEach(func() {
		registry = agent.NewRegistry()
		registry.Register(&agent.Manifest{ID: "executor", Name: "Executor"})
		registry.Register(&agent.Manifest{ID: "Senior-Engineer", Name: "Senior Engineer"})
		registry.Register(&agent.Manifest{ID: "explorer", Name: "Explorer"})

		swarmReg = swarm.NewRegistry()
		swarmReg.Register(&swarm.Manifest{
			SchemaVersion: "1.0.0",
			ID:            "bug-hunt",
			Lead:          "Senior-Engineer",
			Members:       []string{"explorer"},
		})

		streamer = &fakeOrchestratorStreamer{}
		eng = &fakeOrchestratorEngine{}
		consumer = &fakeStreamConsumer{}
	})

	Describe("ProcessUserInput", func() {
		Context("when DefaultAgent resolves to an agent and ScanMentions is false", func() {
			It("streams from that agent without installing a swarm context", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "hello",
					DefaultAgent: "executor",
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("executor"))
				Expect(streamer.capturedMessage).To(Equal("hello"))
				// SetSwarmContext is called once — with nil — to keep
				// the engine in single-agent shape.
				Expect(eng.contexts).To(HaveLen(1))
				Expect(eng.contexts[0]).To(BeNil())
				// Symmetric snapshot/restore around the stream.
				Expect(eng.snapshotCalls).To(Equal(1))
				Expect(eng.restoreCalls).To(Equal(1))
				Expect(eng.flushCalls).To(Equal(1))
			})
		})

		Context("when DefaultAgent resolves to a swarm and ScanMentions is false", func() {
			It("streams from the swarm's lead and installs the swarm context", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "trace the auth path",
					DefaultAgent: "bug-hunt",
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("Senior-Engineer"))
				Expect(eng.contexts).To(HaveLen(1))
				Expect(eng.contexts[0]).NotTo(BeNil())
				Expect(eng.contexts[0].SwarmID).To(Equal("bug-hunt"))
				Expect(eng.contexts[0].LeadAgent).To(Equal("Senior-Engineer"))
			})
		})

		Context("when ScanMentions is true and the message contains @<swarm-id>", func() {
			It("the @-mention overrides DefaultAgent", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "@bug-hunt please look at the auth module",
					DefaultAgent: "executor",
					ScanMentions: true,
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("Senior-Engineer"),
					"swarm @-mention must override DefaultAgent")
				Expect(eng.contexts[0]).NotTo(BeNil())
				Expect(eng.contexts[0].SwarmID).To(Equal("bug-hunt"))
			})
		})

		Context("when ScanMentions is true but only agent @-mentions appear", func() {
			It("falls through to DefaultAgent (agent mentions don't redirect)", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "ask @explorer to look at this",
					DefaultAgent: "executor",
					ScanMentions: true,
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("executor"))
				Expect(eng.contexts[0]).To(BeNil())
			})
		})

		Context("when ScanMentions is true and an unknown @-mention appears", func() {
			It("skips it and falls through to DefaultAgent", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "ping @ghost-thing about it",
					DefaultAgent: "executor",
					ScanMentions: true,
				}, consumer)

				Expect(err).NotTo(HaveOccurred())
				Expect(streamer.capturedAgentID).To(Equal("executor"))
			})
		})

		Context("when both DefaultAgent is empty and ScanMentions matches no swarm", func() {
			It("returns the no-target error", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "",
					DefaultAgent: "",
				}, consumer)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("no agent or swarm target resolved"))
				// Streamer should NOT have been driven.
				Expect(streamer.capturedAgentID).To(Equal(""))
				Expect(eng.snapshotCalls).To(Equal(0))
			})
		})

		Context("when DefaultAgent is unknown", func() {
			It("returns swarm.NotFoundError without driving the streamer", func() {
				orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

				err := orch.ProcessUserInput(context.Background(), orchestrator.UserInput{
					Message:      "hi",
					DefaultAgent: "ghost",
				}, consumer)

				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("ghost"))
				Expect(streamer.capturedAgentID).To(Equal(""))
			})
		})
	})

	Describe("SwitchAgent", func() {
		It("swaps the engine manifest and updates the session manager on a single call", func() {
			mgr := &fakeSessionManager{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, mgr)

			manifest, err := orch.SwitchAgent(context.Background(), "session-x", "Senior-Engineer")

			Expect(err).NotTo(HaveOccurred())
			Expect(manifest).NotTo(BeNil())
			Expect(manifest.ID).To(Equal("Senior-Engineer"))
			Expect(eng.manifestSet).To(HaveLen(1))
			Expect(eng.manifestSet[0].ID).To(Equal("Senior-Engineer"))
			Expect(mgr.agentSessions).To(Equal([]string{"session-x"}))
			Expect(mgr.agentIDs).To(Equal([]string{"Senior-Engineer"}))
		})

		It("returns ErrAgentNotFound when the registry yields nothing", func() {
			mgr := &fakeSessionManager{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, mgr)

			manifest, err := orch.SwitchAgent(context.Background(), "session-x", "ghost-agent")

			Expect(err).To(MatchError(orchestrator.ErrAgentNotFound))
			Expect(manifest).To(BeNil())
			Expect(eng.manifestSet).To(BeEmpty())
			Expect(mgr.agentSessions).To(BeEmpty())
		})

		It("does not touch the session manager when sessionID is empty", func() {
			mgr := &fakeSessionManager{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, mgr)

			manifest, err := orch.SwitchAgent(context.Background(), "", "executor")

			Expect(err).NotTo(HaveOccurred())
			Expect(manifest.ID).To(Equal("executor"))
			Expect(eng.manifestSet).To(HaveLen(1))
			Expect(mgr.agentSessions).To(BeEmpty())
		})

		It("still mutates the engine when no session manager is wired", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			manifest, err := orch.SwitchAgent(context.Background(), "session-x", "executor")

			Expect(err).NotTo(HaveOccurred())
			Expect(manifest.ID).To(Equal("executor"))
			Expect(eng.manifestSet).To(HaveLen(1))
		})
	})

	Describe("SwitchModel", func() {
		It("sets the engine model preference and updates the session manager on a single call", func() {
			mgr := &fakeSessionManager{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, mgr)

			err := orch.SwitchModel(context.Background(), "session-x", "ollama", "llama3")

			Expect(err).NotTo(HaveOccurred())
			Expect(eng.modelPrefProviders).To(Equal([]string{"ollama"}))
			Expect(eng.modelPrefModels).To(Equal([]string{"llama3"}))
			Expect(mgr.modelSessions).To(Equal([]string{"session-x"}))
			Expect(mgr.modelProviders).To(Equal([]string{"ollama"}))
			Expect(mgr.modelModels).To(Equal([]string{"llama3"}))
		})

		It("does not touch the session manager when sessionID is empty", func() {
			mgr := &fakeSessionManager{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, mgr)

			err := orch.SwitchModel(context.Background(), "", "ollama", "llama3")

			Expect(err).NotTo(HaveOccurred())
			Expect(eng.modelPrefProviders).To(HaveLen(1))
			Expect(mgr.modelSessions).To(BeEmpty())
		})

		It("still mutates the engine when no session manager is wired", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			err := orch.SwitchModel(context.Background(), "session-x", "openai", "gpt-4o")

			Expect(err).NotTo(HaveOccurred())
			Expect(eng.modelPrefProviders).To(Equal([]string{"openai"}))
			Expect(eng.modelPrefModels).To(Equal([]string{"gpt-4o"}))
		})

		// Phase-5 Slice α — model-switch compaction trigger.
		//
		// SwitchModel must call MaybeCompactForModel BEFORE
		// SetModelPreference. The trigger inspects the persisted
		// history against the new model's window; if it ran AFTER the
		// preference swing the engine would resolve limits against the
		// new model regardless and the gate would no longer be the
		// "would the next request refuse?" check that motivates
		// firing.
		It("calls MaybeCompactForModel BEFORE SetModelPreference (Phase-5 Slice α)", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			err := orch.SwitchModel(context.Background(), "session-α", "tiny-provider", "tiny-model")

			Expect(err).NotTo(HaveOccurred())
			Expect(eng.maybeCompactCalls).To(HaveLen(1),
				"MaybeCompactForModel must fire on every SwitchModel call so a smaller-window switch cannot strand the next Stream behind the proactive overflow gate")
			Expect(eng.maybeCompactCalls[0].sessionID).To(Equal("session-α"))
			Expect(eng.maybeCompactCalls[0].provider).To(Equal("tiny-provider"))
			Expect(eng.maybeCompactCalls[0].model).To(Equal("tiny-model"))
			Expect(eng.orderTrace).To(Equal([]string{"MaybeCompactForModel", "SetModelPreference"}),
				"the trigger must run before the preference swings — otherwise the engine resolves limits against the new model and the estimated-vs-usable comparison becomes self-fulfilling")
		})

		It("does not call MaybeCompactForModel when sessionID is empty", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			err := orch.SwitchModel(context.Background(), "", "tiny-provider", "tiny-model")

			Expect(err).NotTo(HaveOccurred())
			Expect(eng.maybeCompactCalls).To(BeEmpty(),
				"a session-less SwitchModel cannot drive a session-scoped trigger; the preference still updates")
			Expect(eng.modelPrefProviders).To(Equal([]string{"tiny-provider"}))
		})
	})

	Describe("LoadSession", func() {
		It("loads the context store, installs it on the engine, and returns the bundle", func() {
			loaded := recall.NewEmptyContextStore("")
			loaded.Append(provider.Message{Role: "user", Content: "hello"})
			store := &fakeSessionStore{loadStore: loaded}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			ls, err := orch.LoadSession(context.Background(), "session-loaded")

			Expect(err).NotTo(HaveOccurred())
			Expect(ls).NotTo(BeNil())
			Expect(ls.Store).To(BeIdenticalTo(loaded))
			Expect(eng.contextStoreCallCount).To(Equal(1))
			Expect(eng.contextStores[0]).To(BeIdenticalTo(loaded))
			Expect(eng.contextStoreSessions[0]).To(Equal("session-loaded"))
		})

		It("returns the persisted swarm events when the store satisfies SwarmEventPersister", func() {
			loaded := recall.NewEmptyContextStore("")
			events := []streaming.SwarmEvent{
				{ID: "evt-1", AgentID: "explorer", Type: streaming.EventDelegation},
			}
			store := &fakeSessionStoreWithEvents{
				fakeSessionStore: fakeSessionStore{loadStore: loaded},
				loadedEvents:     events,
			}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			ls, err := orch.LoadSession(context.Background(), "session-loaded")

			Expect(err).NotTo(HaveOccurred())
			Expect(ls.SwarmEvents).To(HaveLen(1))
			Expect(ls.SwarmEvents[0].ID).To(Equal("evt-1"))
		})

		It("returns nil swarm events when the store does not satisfy SwarmEventPersister", func() {
			loaded := recall.NewEmptyContextStore("")
			store := &fakeSessionStore{loadStore: loaded}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			ls, err := orch.LoadSession(context.Background(), "session-loaded")

			Expect(err).NotTo(HaveOccurred())
			Expect(ls.SwarmEvents).To(BeNil())
		})

		It("swallows LoadEvents errors and returns nil swarm events (matches TUI fallback)", func() {
			loaded := recall.NewEmptyContextStore("")
			store := &fakeSessionStoreWithEvents{
				fakeSessionStore: fakeSessionStore{loadStore: loaded},
				loadEventsErr:    errors.New("WAL is corrupt"),
			}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			ls, err := orch.LoadSession(context.Background(), "session-loaded")

			Expect(err).NotTo(HaveOccurred())
			Expect(ls.SwarmEvents).To(BeEmpty())
		})

		It("returns ErrStoreNotConfigured when the orchestrator has no SessionStore", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			_, err := orch.LoadSession(context.Background(), "session-loaded")

			Expect(err).To(MatchError(orchestrator.ErrStoreNotConfigured))
		})
	})

	Describe("NewSession", func() {
		It("generates a UUID v4 and installs an empty store on the engine", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			sessionID, err := orch.NewSession(context.Background())

			Expect(err).NotTo(HaveOccurred())
			Expect(sessionID).NotTo(BeEmpty())
			// UUID v4 string length is 36 characters (8-4-4-4-12 hex with dashes).
			Expect(sessionID).To(HaveLen(36))
			Expect(eng.contextStoreCallCount).To(Equal(1))
			Expect(eng.contextStoreSessions[0]).To(Equal(sessionID))
			Expect(eng.contextStores[0]).NotTo(BeNil())
		})

		It("tolerates a nil engine for test-minimal compositions", func() {
			orch := orchestrator.New(nil, registry, swarmReg, streamer, nil, nil)

			sessionID, err := orch.NewSession(context.Background())

			Expect(err).NotTo(HaveOccurred())
			Expect(sessionID).NotTo(BeEmpty())
		})
	})

	Describe("SaveTurnEnd", func() {
		It("saves session metadata verbatim from the supplied snapshot", func() {
			store := &fakeSessionStore{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			cs := recall.NewEmptyContextStore("")
			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{
				Store:        cs,
				AgentID:      "executor",
				SystemPrompt: "you are an executor",
				LoadedSkills: []string{"pre-action", "memory-keeper"},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(store.saved).To(HaveLen(1))
			Expect(store.saved[0].sessionID).To(Equal("session-x"))
			Expect(store.saved[0].store).To(BeIdenticalTo(cs))
			Expect(store.saved[0].meta.AgentID).To(Equal("executor"))
			Expect(store.saved[0].meta.SystemPrompt).To(Equal("you are an executor"))
			Expect(store.saved[0].meta.LoadedSkills).To(Equal([]string{"pre-action", "memory-keeper"}))
		})

		It("calls SaveEvents when the store supports it AND swarm events are non-empty", func() {
			store := &fakeSessionStoreWithEvents{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			cs := recall.NewEmptyContextStore("")
			events := []streaming.SwarmEvent{
				{ID: "evt-1", AgentID: "explorer"},
			}
			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{
				Store:       cs,
				AgentID:     "executor",
				SwarmEvents: events,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(store.saveEventsCalls).To(Equal(1))
			Expect(store.savedEventsBySess["session-x"]).To(HaveLen(1))
		})

		It("does NOT call SaveEvents when the swarm-event slice is empty", func() {
			store := &fakeSessionStoreWithEvents{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			cs := recall.NewEmptyContextStore("")
			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{
				Store:       cs,
				AgentID:     "executor",
				SwarmEvents: nil,
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(store.saveEventsCalls).To(Equal(0))
		})

		It("returns the underlying Save error verbatim (fatal)", func() {
			store := &fakeSessionStore{saveErr: errors.New("disk full")}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			cs := recall.NewEmptyContextStore("")
			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{
				Store: cs,
			})

			Expect(err).To(MatchError("disk full"))
		})

		It("ignores SaveEvents errors (best-effort, mirrors TUI saveSession asymmetry)", func() {
			store := &fakeSessionStoreWithEvents{
				saveEventsErr: errors.New("event WAL fsync failed"),
			}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			cs := recall.NewEmptyContextStore("")
			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{
				Store:       cs,
				SwarmEvents: []streaming.SwarmEvent{{ID: "evt-1"}},
			})

			Expect(err).NotTo(HaveOccurred())
			Expect(store.saveEventsCalls).To(Equal(1))
		})

		It("returns ErrStoreNotConfigured when no SessionStore is wired", func() {
			orch := orchestrator.New(eng, registry, swarmReg, streamer, nil, nil)

			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{})

			Expect(err).To(MatchError(orchestrator.ErrStoreNotConfigured))
		})

		It("is a no-op when snapshot.Store is nil (caller has no engine state to persist)", func() {
			store := &fakeSessionStore{}
			orch := orchestrator.New(eng, registry, swarmReg, streamer, store, nil)

			err := orch.SaveTurnEnd(context.Background(), "session-x", orchestrator.TurnSnapshot{Store: nil})

			Expect(err).NotTo(HaveOccurred())
			Expect(store.saved).To(BeEmpty())
		})
	})
})
