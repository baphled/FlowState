// dispatch_service_test.go pins the behaviour of swarm.DispatchSwarm
// (the shared service used by every CLI / API / TUI dispatch path). The
// May 2026 forensic audit of plan-writer dispatches (session
// 981b9fac-b6a4-4341-8ade-857107794c08) uncovered a spurious
// agent.switched event firing at terminal response: even when no
// swarm context was installed (swarmCtx=nil — the plain-agent fast
// path used by `flowstate run --agent <id>`), DispatchSwarm
// unconditionally called eng.ManifestSnapshot() before the stream and
// eng.RestoreManifest() after. RestoreManifest re-applied the
// PRE-stream manifest (which Engine.Stream had since swapped to the
// caller-requested agent via the documented `flowstate run --agent
// <id>` auto-swap), flipping the engine back to the configured
// default_agent at the moment of terminal response. This pins the
// expected behaviour: snapshot+restore are SWARM-LIFECYCLE concerns
// and must NOT fire when swarmCtx is nil.
package swarm_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// recordingDispatchEngine satisfies swarm.DispatchEngine and records
// every lifecycle call so the spec can assert exact call counts. Mirrors
// the dispatcher_test.go fakeDispatchEngine shape but lives inside the
// swarm package so the spec doesn't pull in the dispatch import.
type recordingDispatchEngine struct {
	mu                  sync.Mutex
	setSwarmContextArgs []*swarm.Context
	flushCalls          int
	snapshotCalls       int
	restoreCalls        int
}

func (r *recordingDispatchEngine) SetSwarmContext(ctx *swarm.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setSwarmContextArgs = append(r.setSwarmContextArgs, ctx)
}

func (r *recordingDispatchEngine) FlushSwarmLifecycle(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushCalls++
	return nil
}

func (r *recordingDispatchEngine) ManifestSnapshot() any {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snapshotCalls++
	return nil
}

func (r *recordingDispatchEngine) RestoreManifest(_ any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.restoreCalls++
}

func (r *recordingDispatchEngine) SkipAgentFiles() bool    { return false }
func (r *recordingDispatchEngine) SetSkipAgentFiles(_ bool) {}

func (r *recordingDispatchEngine) snapshotCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotCalls
}

func (r *recordingDispatchEngine) restoreCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.restoreCalls
}

func (r *recordingDispatchEngine) flushCallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.flushCalls
}

func (r *recordingDispatchEngine) setSwarmContextCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.setSwarmContextArgs)
}

// silentStreamer satisfies streaming.Streamer with a clean Done chunk.
// DispatchSwarm calls streaming.Run with this; the consumer drains the
// channel without side-effects on the recording engine.
type silentStreamer struct {
	lastAgentID string
	lastMessage string
}

func (s *silentStreamer) Stream(_ context.Context, agentID, message string) (<-chan provider.StreamChunk, error) {
	s.lastAgentID = agentID
	s.lastMessage = message
	ch := make(chan provider.StreamChunk, 1)
	ch <- provider.StreamChunk{Done: true}
	close(ch)
	return ch, nil
}

// silentConsumer satisfies streaming.StreamConsumer and discards every
// callback. DispatchSwarm drives the streamer through streaming.Run,
// which fans every chunk type through the consumer; we accept-and-ignore
// so the test stays focused on the engine lifecycle calls.
type silentConsumer struct{}

func (silentConsumer) WriteChunk(string) error  { return nil }
func (silentConsumer) WriteError(error)         {}
func (silentConsumer) Done()                    {}
func (silentConsumer) WriteToolCall(string)     {}
func (silentConsumer) WriteToolResult(string)   {}

var _ streaming.StreamConsumer = silentConsumer{}

var _ = Describe("swarm.DispatchSwarm", func() {
	Context("when swarmCtx is nil (the plain-agent fast path used by `flowstate run --agent <id>`)", func() {
		// The forensic-audit symptom (session 981b9fac-…): a non-swarm
		// dispatch fired a SECOND agent.switched event at terminal
		// response. Tracing: DispatchSwarm called ManifestSnapshot
		// BEFORE streaming.Run installed the caller's requested manifest
		// (Engine.Stream auto-swaps), so the snapshot captured the
		// PRE-stream baseline (the configured default_agent). After the
		// stream, DispatchSwarm called RestoreManifest with that
		// pre-stream snapshot, flipping the engine BACK to the default
		// at the moment of terminal response. The user-facing result:
		// `.json` records `agent_id: <default>` instead of `agent_id:
		// <caller-requested>`, and the .meta.json sidecar (written at
		// session creation by persistRootSessionMetadata) disagrees with
		// the .json — two files, two answers.
		//
		// Fix: snapshot+restore are SWARM-LIFECYCLE concerns. They MUST
		// NOT fire when swarmCtx is nil — the caller-requested agent
		// swap is the desired terminal state, not a swarm-lead override
		// that needs unwinding.
		It("does not call ManifestSnapshot — there is no swarm context to unwind", func() {
			eng := &recordingDispatchEngine{}
			streamer := &silentStreamer{}
			consumer := silentConsumer{}

			err := swarm.DispatchSwarm(
				context.Background(),
				eng,
				nil, // swarmCtx — plain-agent path
				streamer,
				consumer,
				"plan-writer",
				"hello",
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(eng.snapshotCallCount()).To(Equal(0),
				"DispatchSwarm must NOT snapshot the manifest when swarmCtx is nil — there is no swarm-lead override to restore from. Capturing a snapshot on the plain-agent path leads to a spurious RestoreManifest at terminal response that reverts the engine to the configured default_agent (the session 981b9fac… symptom).")
		})

		It("does not call RestoreManifest — preventing the spurious agent.switched flip at terminal response", func() {
			eng := &recordingDispatchEngine{}
			streamer := &silentStreamer{}
			consumer := silentConsumer{}

			err := swarm.DispatchSwarm(
				context.Background(),
				eng,
				nil, // swarmCtx — plain-agent path
				streamer,
				consumer,
				"plan-writer",
				"hello",
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(eng.restoreCallCount()).To(Equal(0),
				"DispatchSwarm must NOT restore the manifest when swarmCtx is nil. The engine's manifest, post-stream, reflects the caller-requested agent (`plan-writer`), which IS the desired terminal state. Restoring to the pre-stream baseline (the configured default_agent) at end-of-turn flips engine.manifest.ID — emitting a spurious agent.switched event AND corrupting the agent_id stamp the CLI's saveSession path later reads via Engine.Manifest().ID for the .json file write.")
		})

		It("still installs the nil swarm context — preserves the existing SetSwarmContext(nil) wind-down contract", func() {
			eng := &recordingDispatchEngine{}
			streamer := &silentStreamer{}
			consumer := silentConsumer{}

			err := swarm.DispatchSwarm(
				context.Background(),
				eng,
				nil,
				streamer,
				consumer,
				"plan-writer",
				"hello",
			)
			Expect(err).NotTo(HaveOccurred())

			// Dispatcher.runEphemeralStream's comment (dispatcher.go:
			// 429-441) calls out the SetSwarmContext(nil) wind-down as
			// the load-bearing reason the plain-agent path routes
			// through DispatchSwarm rather than streaming.Run directly:
			// "preserves the SetSwarmContext(nil) / FlushSwarmLifecycle
			// wind-down that /api/chat used to get for free via the
			// orchestrator". Our snapshot/restore fix MUST keep that
			// surface intact.
			Expect(eng.setSwarmContextCount()).To(Equal(1),
				"DispatchSwarm must still call SetSwarmContext(nil) — the wind-down for any prior swarm context on the engine is the load-bearing reason the plain-agent path routes through DispatchSwarm rather than directly through streaming.Run")
			Expect(eng.flushCallCount()).To(Equal(1),
				"DispatchSwarm must still call FlushSwarmLifecycle on the nil-swarm path — post-swarm gate cleanup runs unconditionally")
		})
	})

	Context("when swarmCtx is non-nil (the real swarm-dispatch path)", func() {
		// Symmetric guard: the spec above intentionally narrows the
		// snapshot/restore gate to nil-swarm-ctx. The real swarm path
		// (e.g. `flowstate run @planner`) DOES need the snapshot/restore
		// pair so the engine reverts from the swarm lead's identity
		// back to the pre-dispatch agent for subsequent turns. Without
		// this guard a future change could mistake the fix for "remove
		// the snapshot/restore entirely" and re-introduce the TUI's
		// "lead-identity leaks into next turn" regression.
		It("calls ManifestSnapshot and RestoreManifest once each — the existing swarm-lifecycle contract", func() {
			eng := &recordingDispatchEngine{}
			streamer := &silentStreamer{}
			consumer := silentConsumer{}

			swarmCtx := swarm.NewContext("planning-loop", &swarm.Manifest{
				SchemaVersion: swarm.SchemaVersionV1,
				ID:            "planning-loop",
				Lead:          "planner",
				Members:       []string{"plan-writer"},
			})

			err := swarm.DispatchSwarm(
				context.Background(),
				eng,
				&swarmCtx,
				streamer,
				consumer,
				"planner",
				"hello",
			)
			Expect(err).NotTo(HaveOccurred())

			Expect(eng.snapshotCallCount()).To(Equal(1),
				"DispatchSwarm must snapshot the manifest before driving a real swarm dispatch — the snapshot is what RestoreManifest unwinds to after the swarm lead's stream completes")
			Expect(eng.restoreCallCount()).To(Equal(1),
				"DispatchSwarm must restore the manifest after a real swarm dispatch — without restore the swarm lead's identity leaks into the engine's persistent manifest and every subsequent turn streams under the lead's persona")
		})
	})
})
