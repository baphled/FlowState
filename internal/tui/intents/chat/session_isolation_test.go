package chat_test

import (
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	contextpkg "github.com/baphled/flowstate/internal/context"
	"github.com/baphled/flowstate/internal/engine"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/recall"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/tui/intents/chat"
	"github.com/baphled/flowstate/internal/tui/intents/sessionbrowser"
)

// persistingSessionLister is a stubSessionLister that also satisfies
// SwarmEventPersister so session-switch tests exercise the full
// clear-before-restore-without-double-write contract established by P5/B2.
//
// It records every AppendEvent call so a test can assert that the restore
// path does NOT re-fire disk appends when handleSessionLoaded repopulates the
// in-memory store from a previously persisted WAL.
type persistingSessionLister struct {
	mu                sync.Mutex
	loadStoreByID     map[string]*recall.FileContextStore
	loadEventsByID    map[string][]streaming.SwarmEvent
	appendedBySession map[string][]streaming.SwarmEvent
	savedBySession    map[string][]streaming.SwarmEvent
}

func newPersistingSessionLister() *persistingSessionLister {
	return &persistingSessionLister{
		loadStoreByID:     map[string]*recall.FileContextStore{},
		loadEventsByID:    map[string][]streaming.SwarmEvent{},
		appendedBySession: map[string][]streaming.SwarmEvent{},
		savedBySession:    map[string][]streaming.SwarmEvent{},
	}
}

func (p *persistingSessionLister) List() []contextpkg.SessionInfo { return nil }

func (p *persistingSessionLister) SetTitle(_ string, _ string) error { return nil }

func (p *persistingSessionLister) Load(sessionID string) (*recall.FileContextStore, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.loadStoreByID[sessionID]; ok {
		return s, nil
	}
	return recall.NewEmptyContextStore(""), nil
}

func (p *persistingSessionLister) Save(sessionID string, _ *recall.FileContextStore, _ contextpkg.SessionMetadata) error {
	_ = sessionID
	return nil
}

func (p *persistingSessionLister) Delete(_ string) error { return nil }

func (p *persistingSessionLister) Fork(_ string, _ string) (string, error) {
	// Stub: isolation tests do not exercise the P18b fork path; the
	// method exists solely so the SessionLister interface is satisfied.
	return "", nil
}

func (p *persistingSessionLister) SaveEvents(sessionID string, evs []streaming.SwarmEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	dup := make([]streaming.SwarmEvent, len(evs))
	copy(dup, evs)
	p.savedBySession[sessionID] = dup
	return nil
}

func (p *persistingSessionLister) LoadEvents(sessionID string) ([]streaming.SwarmEvent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	evs := p.loadEventsByID[sessionID]
	dup := make([]streaming.SwarmEvent, len(evs))
	copy(dup, evs)
	return dup, nil
}

func (p *persistingSessionLister) AppendEvent(sessionID string, ev streaming.SwarmEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.appendedBySession[sessionID] = append(p.appendedBySession[sessionID], ev)
	return nil
}

// AppendedFor returns a defensive snapshot of events routed through
// AppendEvent for the given session.
func (p *persistingSessionLister) AppendedFor(sessionID string) []streaming.SwarmEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	in := p.appendedBySession[sessionID]
	out := make([]streaming.SwarmEvent, len(in))
	copy(out, in)
	return out
}

// SeedEvents configures the canned WAL contents a subsequent LoadEvents call
// will return for the given sessionID.
func (p *persistingSessionLister) SeedEvents(sessionID string, evs []streaming.SwarmEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	dup := make([]streaming.SwarmEvent, len(evs))
	copy(dup, evs)
	p.loadEventsByID[sessionID] = dup
}

// newSessionIsolationIntent constructs an Intent with the minimal wiring
// required for the handleSessionLoaded path to run without panicking. The
// engine is necessary because handleSessionLoaded calls SetContextStore on it.
func newSessionIsolationIntent(sessionID string, lister *persistingSessionLister) *chat.Intent {
	reg := provider.NewRegistry()
	reg.Register(&streamingStubProvider{
		providerName: "test-provider",
		chunks:       []provider.StreamChunk{},
	})
	eng := engine.New(engine.Config{
		Registry: reg,
		Manifest: stubManifestWithProvider("test-provider", "test-model"),
	})
	return chat.NewIntent(chat.IntentConfig{
		Engine:       eng,
		Streamer:     eng,
		AgentID:      "agent-a",
		SessionID:    sessionID,
		ProviderName: "test-provider",
		ModelName:    "test-model",
		TokenBudget:  4096,
		SessionStore: lister,
	})
}

// --- P5/B2 session isolation regression gate ----------------------------

var _ = Describe("ChatIntent session isolation (P5/B2)", func() {
	sampleEvent := func(id string, ts time.Time) streaming.SwarmEvent {
		return streaming.SwarmEvent{
			ID:            id,
			Type:          streaming.EventToolCall,
			Status:        "completed",
			Timestamp:     ts,
			AgentID:       "agent-a",
			SchemaVersion: streaming.CurrentSchemaVersion,
		}
	}

	It("clears the swarmStore before restoring events on session switch", func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		refTime := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)
		lister := newPersistingSessionLister()
		lister.SeedEvents("session-b", []streaming.SwarmEvent{
			sampleEvent("b-only-1", refTime),
			sampleEvent("b-only-2", refTime.Add(time.Second)),
		})

		intent := newSessionIsolationIntent("session-a", lister)

		// Simulate session A having accumulated events in memory.
		sessionAOnly := []streaming.SwarmEvent{
			sampleEvent("a-leaked-1", refTime),
			sampleEvent("a-leaked-2", refTime.Add(time.Second)),
		}
		for _, ev := range sessionAOnly {
			intent.SwarmStoreForTest().Append(ev)
		}

		// Switch to session B (the handleSessionLoaded path used by
		// the session browser, session tree, and the session-switcher).
		intent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "session-b",
			Store:     recall.NewEmptyContextStore(""),
		})

		// After the switch the store must contain ONLY session B's events.
		// Before the fix this assertion fails because the restore path appends
		// onto whatever was already in the store — session A's events leak.
		got := intent.SwarmStoreForTest().All()
		ids := make([]string, len(got))
		for i, ev := range got {
			ids[i] = ev.ID
		}
		Expect(ids).To(Equal([]string{"b-only-1", "b-only-2"}),
			"handleSessionLoaded must Clear() before restoring events, "+
				"otherwise session A events leak into session B's timeline")
	})

	It("does not re-fire the disk AppendFunc during restore", func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		refTime := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)
		lister := newPersistingSessionLister()
		lister.SeedEvents("session-b", []streaming.SwarmEvent{
			sampleEvent("b-existing-1", refTime),
			sampleEvent("b-existing-2", refTime.Add(time.Second)),
			sampleEvent("b-existing-3", refTime.Add(2*time.Second)),
		})

		intent := newSessionIsolationIntent("session-a", lister)

		// handleSessionLoaded routes through a store constructed for "session-a".
		// After switching to session-b the restore must NOT write the
		// previously persisted events back to disk — that would double the
		// on-disk file on every session switch and destroy long-lived WALs.
		intent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "session-b",
			Store:     recall.NewEmptyContextStore(""),
		})

		// The in-memory store is bound to session-a's AppendFunc (the intent
		// was constructed with sessionID="session-a"). The restore must not
		// fire any AppendEvent — neither to session-a nor session-b.
		Expect(lister.AppendedFor("session-a")).To(BeEmpty(),
			"restoring session-b events must not write to session-a's WAL")
		Expect(lister.AppendedFor("session-b")).To(BeEmpty(),
			"restoring events must not re-write them to any session's WAL; "+
				"the WAL is the source the restore just read from")
	})

	It("shows an empty timeline when switching to a new blank session", func() {
		chat.SetRunningInTestsForTest(true)
		DeferCleanup(func() { chat.SetRunningInTestsForTest(false) })

		refTime := time.Date(2026, 4, 17, 9, 0, 0, 0, time.UTC)
		lister := newPersistingSessionLister()
		// No SeedEvents for session-blank — loading returns nil slice.

		intent := newSessionIsolationIntent("session-a", lister)

		// Seed the store with session-A events so we can observe whether
		// Clear happens even when the new session has no events to restore.
		intent.SwarmStoreForTest().Append(sampleEvent("a-1", refTime))
		intent.SwarmStoreForTest().Append(sampleEvent("a-2", refTime.Add(time.Second)))

		intent.Update(sessionbrowser.SessionLoadedMsg{
			SessionID: "session-blank",
			Store:     recall.NewEmptyContextStore(""),
		})

		Expect(intent.SwarmStoreForTest().All()).To(BeEmpty(),
			"switching to a blank session must clear session-a's events "+
				"even when the destination has no events to restore")
	})
})
