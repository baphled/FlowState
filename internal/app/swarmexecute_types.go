package app

import (
	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
	"github.com/baphled/flowstate/internal/swarm"
)

// Outcomes of ExecuteSwarm, carried in ExecuteSwarmResult.Status. The store
// records only pass or fail; the finer distinction lives here so a caller can
// map it onto its own exit codes.
const (
	// SwarmExecAccepted marks a run whose coordinator completed, whose gates all
	// passed and whose packets were all admitted.
	SwarmExecAccepted = "accepted"
	// SwarmExecNotAccepted marks a completed run with every gate passed but a
	// packet not admitted (not run, not completed or refused by admission).
	SwarmExecNotAccepted = "not_accepted"
	// SwarmExecGateFailed marks a completed run in which a gate did not pass,
	// or in which the manifest has no gate to pass.
	SwarmExecGateFailed = "gate_failed"
	// SwarmExecFailed marks a run whose coordinator did not complete (a failure,
	// max_turns, an exhausted budget or a passed deadline, reason deadline) or
	// whose gates were cut short by a deadline.
	SwarmExecFailed = "failed"
	// SwarmExecCancelled marks a run cancelled before its gates finished.
	SwarmExecCancelled = "cancelled"
)

// ExecuteSwarmConfig carries everything ExecuteSwarm needs; the caller loads
// the manifest, the profiles and the collaborators, so no file or network is
// read here.
type ExecuteSwarmConfig struct {
	// Manifest is the swarm to execute; it must name a coordinator.
	Manifest swarm.Manifest
	// Profiles are the loaded agent profiles: the coordinator's and the roles'.
	Profiles []agent.Profile
	// Streamer is the provider the coordinator and every child run on.
	Streamer provider.Streamer
	// Provider, ModelID, Harness and ConfigSHA identify the coordinator session.
	Provider, ModelID, Harness, ConfigSHA string
	// Workdir is the coordinator's tool working directory; it must exist.
	Workdir string
	// MaxTurns bounds the coordinator's turns; zero uses the coordinator
	// profile's max_turns.
	MaxTurns int64
	// MaxTokens caps each provider response; <= 0 uses the model-table default (ResolveMaxOutput).
	MaxTokens int64
	// Budget is the turn pool of the coordinator, from which every child's
	// grant is reserved; zero means unlimited.
	Budget int64
	// Compaction is the config's compaction object (ADR-010, D7). The coordinator and every
	// child, nested ones included, compact by it; the coordinator's window is its profile's
	// context_window when set. The zero value applies the built-in defaults.
	Compaction CompactionConfig
	// Admitter decides each packet once the coordinator and the gates are done.
	Admitter swarm.RunAdmitter
	// Gates runs the manifest's gates; the engine runs them, never the model.
	// Its Dir must equal Workdir (swarm.NewGateRunnerIn), so the gates judge the
	// tree the children worked in; any other runner is refused.
	Gates *swarm.GateRunner
}

// ExecutedChild is one session the coordinator delegated to.
type ExecutedChild struct {
	// ID is the child session id.
	ID string
	// Profile is the profile the child ran under.
	Profile string
	// PacketID is the manifest packet the child is attributed to; empty for an
	// unattributed child (no valid marker, an unknown packet or another role).
	PacketID string
	// Status is the child session's status.
	Status string
	// Turns is the number of provider turns the child's log charges it.
	Turns int64
}

// ExecutedGate is the engine's own result of one manifest gate.
type ExecutedGate struct {
	// Name is the gate name.
	Name string
	// Passed reports whether the gate command exited zero.
	Passed bool
	// Interrupted reports a gate cut short by cancellation.
	Interrupted bool
}

// ExecutedPacket is the admission decision of one manifest packet.
type ExecutedPacket struct {
	// ID is the packet id.
	ID string
	// Admitted reports whether the packet was admitted.
	Admitted bool
	// Reason says why it was not: not run, its child did not complete, or the
	// admitter's refusal; empty for an admitted packet.
	Reason string
	// Evidence names the child that admitted the packet (the first completed
	// one when several were attributed); empty if the packet was not admitted.
	Evidence string
}

// ExecuteSwarmResult is the outcome of one executed swarm.
type ExecuteSwarmResult struct {
	// RunID is the stored swarm run's id; empty if no run was recorded.
	RunID string
	// Status is one of the SwarmExec constants.
	Status string
	// Reason says why a failed or cancelled run stopped; empty otherwise.
	Reason string
	// Cause is the error that stopped a failed or cancelled coordinator (a
	// provider error, ErrBudgetExceeded, ErrMaxTurns, ErrSessionCancelled, a
	// deadline), so a caller can classify it with errors.Is; nil when the
	// coordinator completed or the run was stopped after it (the gates).
	Cause error
	// CoordinatorID is the coordinator's session id.
	CoordinatorID string
	// Children lists the coordinator's delegated children in start order.
	Children []ExecutedChild
	// Gates lists the gate results in manifest order; empty if no gate ran.
	Gates []ExecutedGate
	// Packets lists the admission decisions; empty if admission did not run.
	Packets []ExecutedPacket
	// Manifest is the manifest that was executed.
	Manifest swarm.Manifest
}

// packetAdmitter wraps the configured admitter with the evidence of the run:
// a packet is admitted only if a child attributed to it completed, then the
// wrapped admitter decides. It records every decision.
type packetAdmitter struct {
	inner     swarm.RunAdmitter
	byPacket  map[string]ExecutedChild
	decisions []ExecutedPacket
}

// idCaptureSink forwards a run to the store and keeps the id it was given.
type idCaptureSink struct {
	inner swarm.RunSink
	id    string
}
