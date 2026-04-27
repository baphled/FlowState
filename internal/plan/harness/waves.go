package harness

import (
	"context"
	"strings"
)

// WaveStage describes one stage of a multi-stage planning loop. Each
// stage has named expected outputs that the orchestrator (typically the
// planner agent) must produce before advancing to the next stage. The
// harness uses these to detect when an orchestrator is yielding to the
// user prematurely — i.e. before the current stage's pre-requisites
// have all returned and been written to the coordination store.
//
// The terminology is intentionally aligned with FlowState's existing
// vocabulary:
//
//   - "wave"     — a stage of parallel work that advances together
//     (existing in the swarm spec §T38a "Dependency wave
//     scheduler").
//   - "fan-in"   — the synchronisation point at the end of a wave
//     where N parallel children must all return before
//     the orchestrator advances. Standard concurrency-
//     literature term and the natural complement of the
//     "fan-out" already used in FlowState's tool-call-
//     atomicity ADR.
//
// `gate` is deliberately NOT used here: in FlowState, gate already
// means a *validation* checkpoint with pass/fail semantics (swarm
// spec §3 — pre/post/pre-member/post-member gates). A wave fan-in is
// a *synchronisation* checkpoint with present/missing semantics. The
// two compose (a wave can have validation gates on its outputs) but
// they are not the same primitive.
type WaveStage struct {
	// Name is a short identifier for the wave (e.g. "evidence",
	// "analysis", "writing", "review"). Surfaced in re-prompt
	// feedback so the planner can name the stage it's stuck on.
	Name string

	// ExpectedKeys is the list of coordination-store keys the wave
	// must populate before the orchestrator may advance. Templated
	// strings — the literal substring `{chainID}` is replaced with
	// the active chain id at validation time.
	ExpectedKeys []string

	// Description is an optional human-readable summary the harness
	// includes in re-prompt feedback. Helps the planner reason about
	// what's missing rather than just naming keys.
	Description string
}

// WaveValidator checks whether the current chain has all expected
// outputs for a wave. The harness invokes this before the orchestrator
// is allowed to yield to the user (i.e. before the early-exit on
// non-PhaseGeneration turns in runStreamEvaluation).
//
// Implementations are expected to read the chain id from ctx (using
// whatever convention the caller-supplied harness adapter is wired
// for — typically session.IDKey or a chain-id context key) and check
// the configured store for each expected key.
type WaveValidator interface {
	// MissingForChain returns the list of expected keys that are NOT
	// yet present for the chain associated with ctx, scoped to the
	// named wave. Returns an empty slice when all expected keys are
	// populated. May return an error when the underlying store call
	// fails — the harness treats errors as "wave incomplete" so a
	// store hiccup never causes the planner to skip the gate.
	MissingForChain(ctx context.Context, agentID string, wave WaveStage) ([]string, error)
}

// WithWaves attaches a wave configuration + validator to the harness.
// When non-nil, the harness checks the validator before the early-exit
// on a planner turn whose output isn't structurally a plan (i.e. the
// usual conversational synthesis turn). If any wave reports missing
// keys, the harness re-prompts the planner with a directive feedback
// instead of yielding to the user.
//
// Expected:
//   - stages is ordered oldest-first; the harness walks them sequentially
//     and re-prompts on the FIRST one that has missing keys.
//   - validator is non-nil; passing nil is treated as "no waves" (legacy
//     behaviour — the harness yields to the user as before).
//
// Returns:
//   - An Option that wires the config + validator onto the harness.
//
// Side effects:
//   - None at construction.
func WithWaves(stages []WaveStage, validator WaveValidator) Option {
	return func(h *Harness) {
		if validator == nil {
			return
		}
		h.waves = append([]WaveStage(nil), stages...)
		h.waveValidator = validator
	}
}

// checkWavesIncomplete walks the configured waves in order and returns
// directive feedback for the first wave with missing keys. Returns the
// empty string when all waves are complete OR no waves are configured
// (the legacy fast path). The feedback is shaped to push the planner
// toward producing the missing outputs rather than yielding to the user.
//
// agentID is forwarded to the validator unchanged so the validator can
// scope its lookup (e.g. one chain may have multiple sub-agents writing
// to different key spaces).
//
// Side effects:
//   - May call the validator (which itself may read from a store).
func (h *Harness) checkWavesIncomplete(ctx context.Context, agentID string) string {
	if h.waveValidator == nil || len(h.waves) == 0 {
		return ""
	}
	for _, stage := range h.waves {
		missing, err := h.waveValidator.MissingForChain(ctx, agentID, stage)
		if err != nil {
			// Treat lookup errors as "wave incomplete" so a transient
			// store hiccup never silently lets the planner yield past
			// an incomplete fan-in. The feedback names the failure so
			// the planner can react reasonably.
			return buildWaveFeedback(stage, nil, err)
		}
		if len(missing) > 0 {
			return buildWaveFeedback(stage, missing, nil)
		}
	}
	return ""
}

// buildWaveFeedback formats a re-prompt directive for the planner.
// Shape matches the existing critic-feedback format so the planner's
// retry-loop prompt-augmentation hook handles it uniformly.
func buildWaveFeedback(stage WaveStage, missing []string, err error) string {
	var b strings.Builder
	b.WriteString("Wave fan-in incomplete: stage `")
	b.WriteString(stage.Name)
	b.WriteString("` is not yet ready to advance.\n\n")

	if err != nil {
		b.WriteString("The harness could not verify wave outputs:\n  ")
		b.WriteString(err.Error())
		b.WriteString("\n\n")
	}

	if len(missing) > 0 {
		b.WriteString("Missing coordination_store keys (these MUST be populated before you advance):\n")
		for _, k := range missing {
			b.WriteString("  - ")
			b.WriteString(k)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	if stage.Description != "" {
		b.WriteString("What this stage is for: ")
		b.WriteString(stage.Description)
		b.WriteString("\n\n")
	}

	b.WriteString("Continue delegating until every missing key above is written. ")
	b.WriteString("Use background_output to wait for in-flight delegations to finish before yielding. ")
	b.WriteString("Do NOT respond to the user until the next wave's pre-requisites are all in place.")
	return b.String()
}
