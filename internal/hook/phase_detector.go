package hook

import (
	"context"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/provider"
)

// PlanPhase represents the phase of a plan document.
type PlanPhase int

const (
	// PhaseUnknown indicates the phase could not be determined.
	PhaseUnknown PlanPhase = iota
	// PhaseInterview indicates an interview phase (no frontmatter).
	PhaseInterview
	// PhaseGeneration indicates a generation phase (has YAML frontmatter).
	PhaseGeneration
)

// contextKey is a type for context keys to avoid collisions.
type contextKey string

const planPhaseKey contextKey = "plan-phase"

// DetectPhase determines whether text is an interview or generation phase.
//
// Expected:
//   - text is the input string to analyse.
//
// Returns:
//   - PhaseGeneration if text contains YAML frontmatter (---...---)
//   - PhaseInterview if text is non-empty without frontmatter
//   - PhaseUnknown if text is empty or only whitespace
//
// Side effects:
//   - None.
func DetectPhase(text string) PlanPhase {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return PhaseUnknown
	}

	parts := strings.SplitN(trimmed, "---", 3)
	if len(parts) >= 3 && strings.TrimSpace(parts[0]) == "" {
		return PhaseGeneration
	}

	return PhaseInterview
}

// PhaseDetectorHook returns a hook that detects the plan phase and stores it in context.
//
// Expected:
//   - manifestGetter returns the current agent manifest on each call.
//
// Returns:
//   - A Hook that detects phase from the user message and stores it via context.WithValue.
//   - When the active manifest has HarnessEnabled=false, the hook is a no-op.
//
// Side effects:
//   - Stores the detected phase in the context passed to the next handler.
func PhaseDetectorHook(manifestGetter func() agent.Manifest) Hook {
	return func(next HandlerFunc) HandlerFunc {
		return func(ctx context.Context, req *provider.ChatRequest) (<-chan provider.StreamChunk, error) {
			if !manifestGetter().HarnessEnabled {
				return next(ctx, req)
			}
			userMsg := extractUserMessage(req.Messages)
			phase := DetectPhase(userMsg)
			ctx = context.WithValue(ctx, planPhaseKey, phase)
			return next(ctx, req)
		}
	}
}

// PhaseFromContext retrieves the detected plan phase from the context.
//
// Expected:
//   - ctx is a context that may contain a stored phase value.
//
// Returns:
//   - The PlanPhase stored in the context, or PhaseUnknown if not found.
//
// Side effects:
//   - None.
func PhaseFromContext(ctx context.Context) PlanPhase {
	phase, ok := ctx.Value(planPhaseKey).(PlanPhase)
	if !ok {
		return PhaseUnknown
	}
	return phase
}
