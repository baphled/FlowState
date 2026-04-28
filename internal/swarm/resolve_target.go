package swarm

import "fmt"

// ResolveTarget classifies id as agent-or-swarm and returns the
// dispatch shape both CLI run.go and TUI chat.intent need: the agent
// id to stream from (the swarm's lead for swarm targets; the input
// id verbatim for agent targets) plus the *Context to install on the
// engine when the target is a swarm.
//
// This is the consolidated "what does @<id> mean" resolver. The two
// callers were previously duplicating the wrapping logic — CLI's
// resolveAgentOrSwarm at internal/cli/run.go and the TUI chat intent's
// firstSwarmMention/maybeBeginSwarmDispatch pair. Per ADR - Swarm
// Dispatch Across Access Methods (KB), one shared resolver drives
// every access method; surfaces just adapt their input shape (a flag
// value for the CLI, an extracted @-mention for the TUI) and call
// here.
//
// Expected:
//   - hasAgent reports whether an id is registered as an agent. nil
//     short-circuits to "id verbatim, nil ctx, nil err" — preserves
//     the historical CLI bare-engine test contract.
//   - swarmReg is the swarm registry. nil short-circuits the same
//     way as a nil hasAgent.
//   - id is the user-typed token without a leading "@".
//
// Returns:
//   - leadID is the agent id the streamer should drive. For agent
//     hits and pass-through cases this is the input verbatim; for
//     swarm hits it is the swarm's lead.
//   - swarmCtx is a freshly constructed *Context when the id resolved
//     to a swarm; nil otherwise (single-agent shape).
//   - err is *NotFoundError when both registries are non-nil and
//     neither knows the id; an "no lead agent" error when the swarm
//     manifest is malformed; nil on success or pass-through.
//
// Side effects:
//   - None.
func ResolveTarget(hasAgent HasAgent, swarmReg *Registry, id string) (string, *Context, error) {
	if hasAgent == nil || swarmReg == nil {
		return id, nil, nil
	}
	kind, manifest := Resolve(id, hasAgent, swarmReg)
	switch kind {
	case KindAgent:
		return id, nil, nil
	case KindSwarm:
		if manifest == nil || manifest.Lead == "" {
			return "", nil, fmt.Errorf("swarm %q has no lead agent", id)
		}
		ctx := NewContext(id, manifest)
		return ctx.LeadAgent, &ctx, nil
	default:
		return "", nil, &NotFoundError{ID: id}
	}
}
