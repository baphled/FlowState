// Package orchestrator provides the canonical user-input → event-stream
// pipeline shared by every FlowState access method (CLI, API, TUI).
//
// Per ADR - Multi-Access Method Architecture (ADR-001) and ADR -
// Session Orchestrator for Surface Parity, surfaces are thin wrappers
// over Orchestrator.ProcessUserInput; they own only their I/O
// adapter (StreamConsumer implementation), never the dispatch
// lifecycle. Lives in its own package so the api/ and tui/ trees
// can import it without forcing import cycles through internal/app.
package orchestrator

import (
	"context"
	"errors"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/streaming"
	"github.com/baphled/flowstate/internal/swarm"
)

// Orchestrator is the canonical user-input → event-stream
// pipeline shared by CLI, API, and TUI surfaces.
//
// Per ADR - Multi-Access Method Architecture (ADR-001) §"Five
// Principles", access methods MUST be thin wrappers over `internal/`
// services with no business logic. The Orchestrator is the
// service that owns @-mention resolution, swarm dispatch lifecycle,
// and stream event delivery for any user-input → response interaction.
//
// Per ADR - Session Orchestrator for Surface Parity (the follow-up
// ADR that extends ADR-001 to the input pipeline), every surface
// that processes a user message routes through ProcessUserInput.
// Surface-specific I/O is adapted via the streaming.StreamConsumer
// interface — surfaces own their consumer (WriterConsumer, SSEConsumer,
// channel-pump consumer for the TUI's Bubble Tea event loop) but
// never reimplement the dispatch lifecycle.
//
// Internally the orchestrator delegates to swarm.DispatchSwarm so
// CLI and TUI cannot diverge on snapshot/restore-around-stream
// semantics — the architectural drift that produced multiple
// recurring bug classes (TUI persistent identity swap, child
// session events.jsonl gaps, manifest leak across turns).
type Orchestrator struct {
	engine        swarm.DispatchEngine
	agentRegistry *agent.Registry
	swarmRegistry *swarm.Registry
	streamer      streaming.Streamer
}

// UserInput is the surface-agnostic input to ProcessUserInput.
//
// Surface conventions:
//
//   - **CLI** (`flowstate run --agent <id>`, `flowstate chat
//     --message --agent <id>`): DefaultAgent = `--agent` flag,
//     ScanMentions = false. CLI callers route explicitly via the
//     flag and the message body is treated as a plain prompt.
//   - **API** (`POST /api/chat`): DefaultAgent = body's `agent_id`,
//     ScanMentions = false. API callers also route explicitly.
//   - **TUI** (chat input): DefaultAgent = the chat's persistent
//     `agentID` (typically the boot default), ScanMentions = true.
//     Chat scans the input for `@<id>` mentions; the first one that
//     resolves to a swarm wins, otherwise the message goes to
//     DefaultAgent.
//
// The orchestrator never mutates DefaultAgent — surfaces own their
// own persistent identity (e.g. the TUI's `i.agentID` is preserved
// across swarm dispatches per Phase 2 of ADR - Swarm Dispatch
// Across Access Methods).
type UserInput struct {
	// Message is the user's raw input text.
	Message string
	// DefaultAgent is the surface's baseline agent id. Used when
	// ScanMentions is false, or when ScanMentions is true but no
	// @-mention resolves to a swarm.
	DefaultAgent string
	// ScanMentions enables @-mention scanning of Message. When true,
	// the first @-mention that resolves to a swarm overrides
	// DefaultAgent for this call only. Agent @-mentions and unknown
	// @-mentions fall through to DefaultAgent.
	ScanMentions bool
}

// errNoTarget fires when ProcessUserInput is called without a usable
// DefaultAgent and ScanMentions is false (or set but matched no
// mention). Surfaces SHOULD validate their input shape before calling
// the orchestrator; this error is a defensive guard.
var errNoTarget = errors.New("session orchestrator: no agent or swarm target resolved from input")

// New wires the orchestrator's dependencies. All
// fields are required for production use; tests may pass fakes that
// satisfy the narrow interfaces.
//
// Expected:
//   - eng is a non-nil swarm.DispatchEngine (typically *engine.Engine).
//   - agentReg is the agent registry (may be nil; resolution falls
//     through to swarm-only matching when nil).
//   - swarmReg is the swarm registry (may be nil; orchestrator then
//     short-circuits dispatch to a plain agent stream).
//   - streamer drives the underlying provider stream (typically the
//     same *engine.Engine via the Streamer interface).
//
// Returns:
//   - A configured *Orchestrator.
//
// Side effects:
//   - None.
func New(
	eng swarm.DispatchEngine,
	agentReg *agent.Registry,
	swarmReg *swarm.Registry,
	streamer streaming.Streamer,
) *Orchestrator {
	return &Orchestrator{
		engine:        eng,
		agentRegistry: agentReg,
		swarmRegistry: swarmReg,
		streamer:      streamer,
	}
}

// ProcessUserInput is the canonical entry point for "a user/client
// sent input that should produce a streamed response". CLI, API, and
// TUI all route here; behaviour is identical across surfaces because
// the orchestrator drives swarm.DispatchSwarm internally — same
// resolver, same dispatch lifecycle (snapshot → SetSwarmContext →
// stream → flush → restore), same event delivery shape via the
// supplied consumer.
//
// Expected:
//   - ctx is a valid context controlling the streamed run.
//   - req carries the message and routing intent (DefaultAgent +
//     optional ScanMentions).
//   - consumer is the surface-specific event sink. CLI uses
//     WriterConsumer/JSONConsumer; API uses SSEConsumer; TUI uses
//     a channel-pump consumer that adapts to its Bubble Tea loop.
//
// Returns:
//   - errNoTarget when neither DefaultAgent nor a scanned @-mention
//     resolves to a known agent or swarm.
//   - The wrapped error from swarm.DispatchSwarm on stream/flush
//     failure.
//   - nil on success.
//
// Side effects:
//   - Drives swarm.DispatchSwarm — see that function for the full
//     side-effect list (manifest snapshot/restore, swarm context
//     install, post-flush).
func (o *Orchestrator) ProcessUserInput(
	ctx context.Context,
	req UserInput,
	consumer streaming.StreamConsumer,
) error {
	leadID, swarmCtx, err := o.resolve(req)
	if err != nil {
		return err
	}
	return swarm.DispatchSwarm(ctx, o.engine, swarmCtx, o.streamer, consumer, leadID, req.Message)
}

// resolve picks the target agent or swarm based on the supplied
// UserInput. ScanMentions=true causes a left-to-right scan of
// req.Message for @-mentions; the first one that resolves to a swarm
// wins. Agent @-mentions and unknown @-mentions are skipped and the
// resolver falls through to DefaultAgent.
//
// Expected:
//   - req is the orchestrator's input.
//
// Returns:
//   - leadID, swarmCtx as defined by swarm.ResolveTarget.
//   - errNoTarget when DefaultAgent is empty AND no scanned mention
//     resolved to a swarm.
//
// Side effects:
//   - None.
func (o *Orchestrator) resolve(req UserInput) (string, *swarm.Context, error) {
	hasAgent := o.agentLookup()

	if req.ScanMentions {
		for _, mention := range swarm.ExtractAtMentions(req.Message) {
			leadID, swarmCtx, err := swarm.ResolveTarget(hasAgent, o.swarmRegistry, mention)
			if err == nil && swarmCtx != nil {
				return leadID, swarmCtx, nil
			}
		}
	}

	if req.DefaultAgent == "" {
		return "", nil, errNoTarget
	}
	return swarm.ResolveTarget(hasAgent, o.swarmRegistry, req.DefaultAgent)
}

// agentLookup returns a swarm.HasAgent closure backed by the
// orchestrator's agentRegistry, or nil when the registry is unset.
// nil propagates through swarm.ResolveTarget which treats it as the
// historical "bare engine" pass-through case (returns id verbatim
// with nil swarmCtx).
//
// Side effects:
//   - None.
func (o *Orchestrator) agentLookup() swarm.HasAgent {
	if o.agentRegistry == nil {
		return nil
	}
	return func(name string) bool {
		if _, ok := o.agentRegistry.Get(name); ok {
			return true
		}
		_, ok := o.agentRegistry.GetByNameOrAlias(name)
		return ok
	}
}
