package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/swarm"
	"github.com/baphled/flowstate/internal/tool"
)

// SuggestDelegateTool is a read-only tool offered to agents whose manifests set
// can_delegate:false. When the user's prompt references an @<agent> the current
// agent cannot reach directly, the model can call this tool to surface a
// structured "switch agent?" suggestion to the UI. The tool does NOT perform
// delegation — it simply reports the intent in a format the chat layer can
// render as an actionable notification.
//
// It is the inverse counterpart of DelegateTool: a given agent has one or the
// other, never both. See also: P7 premature-delegation warning path in the
// chat intent, which remains active as a defence-in-depth signal for models
// that emit bare tool_use chunks instead of calling suggest_delegate.
type SuggestDelegateTool struct {
	registry      *agent.Registry
	swarmRegistry *swarm.Registry
	sourceAgentID string
}

// preferredRouterAliases lists agent IDs that indicate a natural delegating
// entrypoint when multiple can_delegate agents are configured. The first match
// wins, so "router" takes precedence over "orchestrator" when both exist.
var preferredRouterAliases = []string{"router", "orchestrator"}

var (
	errSuggestDelegateTargetRequired    = errors.New("target_agent is required and must be a string")
	errSuggestDelegateReasonRequired    = errors.New("reason is required and must be a string")
	errSuggestDelegateNoDelegatingAgent = errors.New(
		"delegation not configured: no agent with can_delegate:true is registered to route this request",
	)
)

// NewSuggestDelegateTool creates a SuggestDelegateTool bound to the given
// agent registry and source agent identifier.
//
// Expected:
//   - reg is the agent registry used to resolve target and router agents.
//   - sourceAgentID is the ID of the agent that owns this tool (the current,
//     non-delegating agent).
//
// Returns:
//   - A configured SuggestDelegateTool instance.
//
// Side effects:
//   - None.
func NewSuggestDelegateTool(reg *agent.Registry, sourceAgentID string) *SuggestDelegateTool {
	return &SuggestDelegateTool{registry: reg, sourceAgentID: sourceAgentID}
}

// NewSuggestDelegateToolWithSwarms constructs a SuggestDelegateTool
// that resolves a target id against the agent registry first and the
// swarm registry second. Mirrors the precedence rule in
// internal/swarm.Resolve and the chat-input @-mention resolver. When
// the target matches a swarm id, the emitted payload sets
// target_kind="swarm", target_swarm to the swarm id, and to_agent /
// target_lead to the swarm's lead so the chat layer renders a
// swarm-dispatch suggestion instead of a plain agent-switch
// suggestion.
//
// A nil swarmReg makes this constructor functionally identical to
// NewSuggestDelegateTool.
func NewSuggestDelegateToolWithSwarms(reg *agent.Registry, swarmReg *swarm.Registry, sourceAgentID string) *SuggestDelegateTool {
	return &SuggestDelegateTool{registry: reg, swarmRegistry: swarmReg, sourceAgentID: sourceAgentID}
}

// SetSourceAgentID updates the agent id this tool reports as `from_agent`
// in its emitted payloads and uses to detect "lead suggesting its own swarm"
// at Execute time. Engine.SetManifest calls this whenever the active
// manifest changes so the tool's view of the current agent stays in sync
// with the engine's runtime state.
//
// Expected:
//   - id is the new active agent's ID; an empty string clears it.
//
// Returns:
//   - None.
//
// Side effects:
//   - Replaces the internal sourceAgentID used during Execute().
func (s *SuggestDelegateTool) SetSourceAgentID(id string) {
	s.sourceAgentID = id
}

// errSuggestDelegateLeadSelfDispatch fires when the active agent calls
// suggest_delegate against the swarm it already leads. The lead has been
// dispatched; suggesting "dispatch this swarm again" is nonsensical and
// historically caused the model to relay the tool's user_prompt as an
// "Action Required: confirm dispatch" message to the user, blocking the
// swarm on a confirmation gate that should not exist.
var errSuggestDelegateLeadSelfDispatch = errors.New(
	"already leading this swarm; delegate to a member with the delegate tool instead of suggesting the same swarm",
)

// Name returns the tool name.
//
// Returns:
//   - The string "suggest_delegate".
//
// Side effects:
//   - None.
func (s *SuggestDelegateTool) Name() string {
	return "suggest_delegate"
}

// Description returns a human-readable description of the tool's purpose.
//
// Returns:
//   - A string describing what the tool does.
//
// Side effects:
//   - None.
func (s *SuggestDelegateTool) Description() string {
	return "Surface a suggestion that the user switch to a delegating agent to reach the requested " +
		"target. Use this when the user's prompt references @<agent> and this agent cannot delegate " +
		"directly. This tool does not perform delegation — it only reports the intent."
}

// Schema returns the JSON schema for the suggest_delegate tool input.
//
// Returns:
//   - A tool.Schema describing the required target_agent and reason properties.
//
// Side effects:
//   - None.
func (s *SuggestDelegateTool) Schema() tool.Schema {
	return tool.Schema{
		Type: "object",
		Properties: map[string]tool.Property{
			"target_agent": {
				Type:        "string",
				Description: "The @name of the agent the user wants to reach (as mentioned in the prompt)",
			},
			"reason": {
				Type:        "string",
				Description: "One-sentence justification for why switching to route to this target is appropriate",
			},
		},
		Required: []string{"target_agent", "reason"},
	}
}

// Execute validates the target and returns a structured payload the UI can
// render as a "switch agent?" prompt. It performs no delegation itself.
//
// Expected:
//   - ctx is a valid context (unused but kept for interface conformance).
//   - input carries "target_agent" and "reason" string arguments.
//
// Returns:
//   - A tool.Result whose Output is a JSON payload containing suggestion,
//     from_agent, to_agent, target_agent, reason, and user_prompt fields.
//   - An error when arguments are invalid, the target is unknown, or no
//     delegating agent is registered.
//
// Side effects:
//   - None. This tool is read-only.
func (s *SuggestDelegateTool) Execute(_ context.Context, input tool.Input) (tool.Result, error) {
	targetRaw, ok := input.Arguments["target_agent"]
	if !ok {
		return tool.Result{}, errSuggestDelegateTargetRequired
	}
	target, ok := targetRaw.(string)
	if !ok || target == "" {
		return tool.Result{}, errSuggestDelegateTargetRequired
	}

	reasonRaw, ok := input.Arguments["reason"]
	if !ok {
		return tool.Result{}, errSuggestDelegateReasonRequired
	}
	reason, ok := reasonRaw.(string)
	if !ok || reason == "" {
		return tool.Result{}, errSuggestDelegateReasonRequired
	}

	if s.registry == nil {
		return tool.Result{}, errSuggestDelegateNoDelegatingAgent
	}

	targetManifest, found := s.registry.GetByNameOrAlias(target)
	if found && targetManifest != nil {
		return s.buildAgentSuggestion(targetManifest, reason)
	}

	if swarmManifest, ok := s.lookupSwarm(target); ok {
		return s.buildSwarmSuggestion(swarmManifest, reason)
	}

	return tool.Result{}, fmt.Errorf("target agent or swarm not found: %q", target)
}

// lookupSwarm reports whether id resolves to a registered swarm. nil
// swarmRegistry is treated as an empty registry so the historical
// agent-only constructor still works.
func (s *SuggestDelegateTool) lookupSwarm(id string) (*swarm.Manifest, bool) {
	if s.swarmRegistry == nil {
		return nil, false
	}
	m, ok := s.swarmRegistry.Get(id)
	if !ok || m == nil {
		return nil, false
	}
	return m, true
}

// buildAgentSuggestion is the agent-target payload path: resolves a
// router via resolveRouter and emits target_kind="agent". Preserves
// the historical payload shape; target_kind is additive.
func (s *SuggestDelegateTool) buildAgentSuggestion(targetManifest *agent.Manifest, reason string) (tool.Result, error) {
	routerID := s.resolveRouter()
	if routerID == "" {
		return tool.Result{}, errSuggestDelegateNoDelegatingAgent
	}

	payload := map[string]interface{}{
		"suggestion":   "switch_agent",
		"from_agent":   s.sourceAgentID,
		"to_agent":     routerID,
		"target_agent": targetManifest.ID,
		"target_kind":  "agent",
		"reason":       reason,
		"user_prompt": fmt.Sprintf(
			"Switch to %s to delegate to @%s?", routerID, targetManifest.ID,
		),
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling suggest_delegate payload: %w", err)
	}

	return tool.Result{
		Output: string(out),
		Title:  "Suggested switch: " + routerID,
	}, nil
}

// buildSwarmSuggestion emits the payload variant the chat layer
// renders as a "dispatch swarm @<id>?" suggestion. to_agent /
// target_lead point at the swarm's lead so the user sees which agent
// will receive the prompt; target_swarm carries the swarm id so the
// chat layer can construct an `@<swarm-id>` re-prompt verbatim.
func (s *SuggestDelegateTool) buildSwarmSuggestion(swarmManifest *swarm.Manifest, reason string) (tool.Result, error) {
	leadID := swarmManifest.Lead
	if leadID == "" {
		return tool.Result{}, fmt.Errorf("swarm %q has no lead agent registered", swarmManifest.ID)
	}
	if leadID == s.sourceAgentID {
		return tool.Result{}, errSuggestDelegateLeadSelfDispatch
	}

	payload := map[string]interface{}{
		"suggestion":   "dispatch_swarm",
		"from_agent":   s.sourceAgentID,
		"to_agent":     leadID,
		"target_swarm": swarmManifest.ID,
		"target_lead":  leadID,
		"target_kind":  "swarm",
		"reason":       reason,
		"user_prompt": fmt.Sprintf(
			"Dispatch swarm @%s (led by %s)?", swarmManifest.ID, leadID,
		),
	}

	out, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, fmt.Errorf("marshalling suggest_delegate swarm payload: %w", err)
	}
	return tool.Result{
		Output: string(out),
		Title:  "Suggested swarm dispatch: " + swarmManifest.ID,
	}, nil
}

// resolveRouter returns the ID of the preferred delegating agent in the
// registry, or the empty string when no such agent exists.
//
// Resolution order:
//  1. An agent whose ID matches a preferred router alias (router,
//     orchestrator) and has CanDelegate == true.
//  2. The first agent with CanDelegate == true in the sorted registry.
//
// Expected:
//   - s.registry is non-nil.
//
// Returns:
//   - The resolved router agent ID, or "" when none is found.
//
// Side effects:
//   - None.
func (s *SuggestDelegateTool) resolveRouter() string {
	manifests := s.registry.List()

	for _, alias := range preferredRouterAliases {
		for _, m := range manifests {
			if m == nil {
				continue
			}
			if m.ID == alias && m.Delegation.CanDelegate {
				return m.ID
			}
		}
	}

	for _, m := range manifests {
		if m == nil {
			continue
		}
		if m.ID == s.sourceAgentID {
			continue
		}
		if m.Delegation.CanDelegate {
			return m.ID
		}
	}
	return ""
}
