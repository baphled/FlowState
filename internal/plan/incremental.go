package plan

// GenerationPhase represents a phase of plan generation.
type GenerationPhase string

// GenerationPhase constants define the ordered steps of plan generation.
const (
	PhaseRationale       GenerationPhase = "Rationale"
	PhaseTasks           GenerationPhase = "Tasks"
	PhaseWaves           GenerationPhase = "Waves"
	PhaseSuccessCriteria GenerationPhase = "SuccessCriteria"
	PhaseRisks           GenerationPhase = "Risks"
)

// AllPhases is the ordered list of all phases.
var AllPhases = []GenerationPhase{
	PhaseRationale,
	PhaseTasks,
	PhaseWaves,
	PhaseSuccessCriteria,
	PhaseRisks,
}

// IncrementalGenerator generates a plan incrementally by phase.
type IncrementalGenerator struct {
	Streamer interface{}
}
