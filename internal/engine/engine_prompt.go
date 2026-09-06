package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/baphled/flowstate/internal/agent"
	"github.com/baphled/flowstate/internal/skill"
	"github.com/baphled/flowstate/internal/swarm"
)

// BuildSystemPrompt returns the system prompt for the engine's active manifest.
//
// Expected: parameters for BuildSystemPrompt.
// Returns: result of BuildSystemPrompt.
// Side effects: None.
func (e *Engine) BuildSystemPrompt() string {
	return e.BuildSystemPromptCtx(context.Background())
}

// BuildSystemPromptCtx is the manifest-binding-aware variant of
// BuildSystemPrompt. When ctx carries a bound manifest (via
// WithBoundManifest), the prompt is composed from that manifest
// directly and the engine's prompt cache is bypassed — concurrent
// streams pinned to different manifests cannot share or invalidate
// each other's cached prompt.
//
// When ctx carries no bound manifest the call is identical to
// BuildSystemPrompt's historical behaviour, including cache use.
//
// Expected:
//   - ctx is a valid context; nil is treated as an unbound ctx.
//
// Returns:
//   - The concatenated system prompt string for the ctx-bound manifest
//     when present, otherwise for the engine's active manifest.
//
// Side effects:
//   - Caches the result against the engine's active manifest only.
//   - Loads agent files once and caches them on the engine; the cached
//     files are shared across manifests because they are project-level,
//     not manifest-level.
func (e *Engine) BuildSystemPromptCtx(ctx context.Context) string {
	if bound, ok := manifestFromContext(ctx); ok {
		return e.buildSystemPromptFor(ctx, bound)
	}
	if _, present := swarm.ScopeFromContext(ctx); present {
		swarmPromptAppend := e.resolveSwarmPromptAppendFor(ctx, e.manifest)
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.assembleSystemPromptLocked(e.manifest, e.skills, swarmPromptAppend)
	}

	e.mu.RLock()
	if !e.systemPromptDirty {
		cached := e.cachedSystemPrompt
		e.mu.RUnlock()
		return cached
	}
	e.mu.RUnlock()

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.systemPromptDirty {
		return e.cachedSystemPrompt
	}

	swarmPromptAppend := e.resolveSwarmPromptAppendFor(ctx, e.manifest)
	base := e.assembleSystemPromptLocked(e.manifest, e.skills, swarmPromptAppend)

	e.cachedSystemPrompt = base
	e.systemPromptDirty = false

	return base
}

// buildSystemPromptFor composes a system prompt for the supplied
// manifest, fully isolated from the engine's cached prompt state.
// Used by the ctx-bound path so concurrent streams pinned to
// different manifests each receive a freshly-built prompt that
// reflects only their own manifest, skills, and delegation
// allowlist.
//
// Skills resolution falls back to the engine's stored skills slice
// when no per-manifest resolver is wired — this matches historical
// single-session behaviour and keeps tests that pre-load skills
// without a resolver working unchanged.
//
// Expected:
//   - manifest is the manifest the caller wants this prompt to
//     describe; ID and Instructions.SystemPrompt are populated.
//
// Returns:
//   - The composed system prompt string.
//
// Side effects:
//   - Loads agent files once via the engine's loader (cached on
//     the engine — agent files are project-level, not
//     manifest-level, so the cache is safe to share).
func (e *Engine) buildSystemPromptFor(ctx context.Context, manifest agent.Manifest) string {
	swarmPromptAppend := e.resolveSwarmPromptAppendFor(ctx, manifest)
	e.mu.Lock()
	defer e.mu.Unlock()

	skills := e.skills
	if e.skillsResolver != nil {
		skills = e.skillsResolver(manifest)
	}
	return e.assembleSystemPromptLocked(manifest, skills, swarmPromptAppend)
}

// assembleSystemPromptLocked is the pure composition routine
// shared by the engine-state and ctx-bound build paths. The caller
// must hold e.mu (write lock — agent file loading needs to mutate
// the engine's cached-files state on first access).
//
// Expected:
//   - e.mu is held for write.
//   - manifest is the manifest to render against.
//   - skills are the skills to inject into the prompt body in the
//     order they should appear.
//
// Returns:
//   - The composed system prompt string.
//
// Side effects:
//   - Populates e.cachedAgentFiles on first access.
func (e *Engine) assembleSystemPromptLocked(manifest agent.Manifest, skills []skill.Skill, swarmPromptAppend string) string {
	base := manifest.Instructions.SystemPrompt

	base = base + "\n\n" + buildTemporalSection(e.nowFunc)

	if section := buildToolDisciplineSection(manifest); section != "" {
		base = base + "\n\n" + section
	}

	if e.agentsFileLoader != nil && !e.skipAgentFiles {
		if !e.agentFilesCached {
			e.cachedAgentFiles = e.agentsFileLoader.LoadFiles()
			e.agentFilesCached = true
		}
		for _, f := range e.cachedAgentFiles {
			base = base + "\n\nInstructions from: " + f.Path + "\n" + f.Content
		}
	}

	for i := range skills {
		base = base + "\n\n# Skill: " + skills[i].Name + "\n\n" + skills[i].Content
	}

	if manifest.Delegation.CanDelegate {
		base = e.appendDelegationSectionsFor(base, manifest)
	}

	base = e.appendSwarmLeadSectionFor(base, manifest)
	if swarmPromptAppend != "" {
		base = base + swarmPromptAppend
	}

	if e.agentOverrides != nil {
		if appendText, ok := e.agentOverrides[manifest.ID]; ok && appendText != "" {
			base = base + "\n\n" + appendText
		}
	}

	base += buildToolUsageRequirement(manifest)

	base += "\n\nNEVER read or write `coordination.json` (the coordination-store backing file) via file tools or shell commands; coordination data moves only through sanctioned tools and CLI verbs.\n"

	return base
}

// resolveSwarmPromptAppendFor ...
//
// Expected: parameters for resolveSwarmPromptAppendFor.
//
// Returns: result of resolveSwarmPromptAppendFor.
//
// Side effects: None.
func (e *Engine) resolveSwarmPromptAppendFor(ctx context.Context, manifest agent.Manifest) string {
	swarmManifest, swarmCtx, ok := e.activeSwarmManifestForPrompt(ctx)
	if !ok {
		return ""
	}
	appends := make([]string, 0, 2)
	if swarmCtx.LeadAgent == manifest.ID {
		if text := resolveSwarmPromptAppend(swarmManifest.SourceDir, swarmManifest.Prompt.LeadAppend, swarmManifest.Prompt.LeadAppendFile); text != "" {
			appends = append(appends, text)
		}
	}
	if cfg, ok := swarmManifest.Prompt.MemberAppends[manifest.ID]; ok {
		if text := resolveSwarmPromptAppend(swarmManifest.SourceDir, cfg.Append, cfg.File); text != "" {
			appends = append(appends, text)
		}
	}
	if len(appends) == 0 {
		return ""
	}
	return "\n\n# Swarm Prompt Injection\n\n" + strings.Join(appends, "\n\n")
}

// activeSwarmManifestForPrompt ...
//
// Expected: parameters for activeSwarmManifestForPrompt.
//
// Returns: result of activeSwarmManifestForPrompt.
//
// Side effects: None.
func (e *Engine) activeSwarmManifestForPrompt(ctx context.Context) (*swarm.Manifest, *swarm.Context, bool) {
	swarmCtx := e.swarmContext
	if scoped, present := swarm.ScopeFromContext(ctx); present {
		swarmCtx = scoped
	}
	if swarmCtx == nil || swarmCtx.SwarmID == "" || e.swarmRegistry == nil {
		return nil, nil, false
	}
	manifest, ok := e.swarmRegistry.Get(swarmCtx.SwarmID)
	if !ok || manifest == nil {
		return nil, nil, false
	}
	return manifest, swarmCtx, true
}

// resolveSwarmPromptAppend ...
//
// Expected: parameters for resolveSwarmPromptAppend.
//
// Returns: result of resolveSwarmPromptAppend.
//
// Side effects: None.
func resolveSwarmPromptAppend(sourceDir string, inline string, file string) string {
	parts := make([]string, 0, 2)
	if trimmed := strings.TrimSpace(inline); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if trimmed := strings.TrimSpace(file); trimmed != "" {
		path := trimmed
		if !filepath.IsAbs(path) && strings.TrimSpace(sourceDir) != "" {
			path = filepath.Join(sourceDir, path)
		}
		if body, err := os.ReadFile(path); err == nil {
			if text := strings.TrimSpace(string(body)); text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n\n")
}

// buildToolUsageRequirement ...
//
// Expected: parameters for buildToolUsageRequirement.
//
// Returns: result of buildToolUsageRequirement.
//
// Side effects: None.
func buildToolUsageRequirement(manifest agent.Manifest) string {
	for _, t := range manifest.Capabilities.Tools {
		if t != "coordination_store" {
			continue
		}

		return "\n\n## Tool-Usage Requirement\n\n" +
			"`coordination_store` is the inter-agent handoff channel for delegation " +
			"chains and swarms: use it to publish contracted outputs for other " +
			"agents and to read theirs. It is not general storage and it is never " +
			"the destination for user-facing output. When operating as part of a " +
			"swarm, you MUST write your contracted output key via the " +
			"`coordination_store` tool — the system validates that the tool call " +
			"occurred. Outside a delegation chain, do not use `coordination_store`. " +
			"When asked to write something, use the `write` tool: writing means " +
			"writing to a file.\n\n" +
			"Coordination-store data MUST ONLY be written through the `coordination_store` tool and read through that tool or sanctioned CLI verbs (`flowstate coordination`, `flowstate autoresearch`). NEVER read, create, or edit the backing store file (`coordination.json`, by default `~/.local/share/flowstate/coordination.json`) using the read/write/edit tools, `bash`, `cat`, or shell redirection. Coordination keys are entries inside that single JSON file — no per-key files exist. Direct file writes are invisible to the running process and are silently overwritten on its next persist. This is a hard prohibition with no exceptions.\n"
	}

	return ""
}

// appendSwarmLeadSectionFor renders the swarm-lead block using the
// supplied manifest as the lead-identity source. Used by the
// ctx-bound build path so concurrent streams pinned to different
// manifests render their own swarm headers.
//
// Expected:
//   - base is the current system prompt string.
//   - manifest is the manifest the prompt is being built for.
//
// Returns:
//   - The base string with the swarm-lead block appended when the
//     engine carries a swarm context whose LeadAgent matches
//     manifest.ID; otherwise base is returned unchanged.
//
// Side effects:
//   - None.
func (e *Engine) appendSwarmLeadSectionFor(base string, manifest agent.Manifest) string {
	swarmCtx := e.swarmContext
	if swarmCtx == nil {
		return base
	}
	if swarmCtx.LeadAgent == "" || swarmCtx.LeadAgent != manifest.ID {
		return base
	}

	var b strings.Builder
	b.WriteString(base)
	b.WriteString("\n\n# Swarm Leadership\n\n")
	b.WriteString("You are leading swarm `")
	b.WriteString(swarmCtx.SwarmID)
	b.WriteString("`. The user's request is owned by this swarm; you coordinate the members below rather than answering alone.\n\n")
	b.WriteString("You have already been dispatched as the lead — the user does NOT need to confirm anything. ")
	b.WriteString("Do not write \"Action Required: confirm dispatch\", \"Proceed?\", \"Should I continue?\" ")
	b.WriteString("or any other prompt that asks the user to approve starting the swarm. ")
	b.WriteString("Begin by delegating to a member immediately. If the user's scope is too vague to act on, ")
	b.WriteString("delegate the scoping work itself (e.g. to an explorer or analyst member) rather than blocking on the user. ")
	b.WriteString("Only return to the user with the synthesised final report.\n\n")
	b.WriteString("Do NOT call `suggest_delegate` for this swarm or its members; the dispatch is already in flight, and the tool will refuse a self-dispatch suggestion. Use `delegate` for member calls.\n\n")

	b.WriteString("## Members\n\n")
	if len(swarmCtx.Members) == 0 {
		b.WriteString("- (no members declared)\n")
	}
	for _, memberID := range swarmCtx.Members {
		b.WriteString("- `")
		b.WriteString(memberID)
		b.WriteString("`")
		if name, role, ok := e.resolveSwarmMemberDetails(memberID); ok {
			if name != "" {
				b.WriteString(" — ")
				b.WriteString(name)
			}
			if role != "" {
				b.WriteString(" (")
				b.WriteString(role)
				b.WriteString(")")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Delegation\n\n")
	b.WriteString("Dispatch **independent** members in a **single message** by emitting multiple `delegate` tool calls simultaneously — do NOT wait for one independent member to finish before dispatching the next independent member. The engine runs concurrent tool calls in parallel; sequential dispatch of independent work wastes wall-clock time and burns unnecessary tokens on wait overhead.\n\n")
	b.WriteString("If some members depend on the output of earlier members (e.g. a codebase explorer that writes findings the review members will read), use sequential waves: dispatch the upstream members first, wait for their results, then dispatch the downstream members together in a single parallel message. After all member results are returned, synthesise their findings into a final report for the user.\n")

	chainPrefix := swarmCtx.ChainPrefix
	if chainPrefix == "" {
		chainPrefix = swarmCtx.SwarmID
	}
	b.WriteString("\n## Coordination namespace\n\n")
	b.WriteString("Write outputs to the coordination store under `")
	b.WriteString(chainPrefix)
	b.WriteString("/")
	b.WriteString(manifest.ID)
	b.WriteString("/...` so the swarm's members agree on where to read and write.\n")

	if swarmCtx.ChainIDAssigned {
		// Engine-owned chainID: the value is assigned by the engine at swarm
		// start (AssignRunChainID), NOT chosen by the model. Surface it
		// verbatim so the lead references THIS exact value in its delegate
		// messages instead of inventing a free-form one. The recurring
		// planning-loop doom-loop was the lead free-forming a chainID (often
		// with a slash) that diverged from the value the wave validator,
		// gates and publisher resolved. The engine ignores any chainID the
		// model supplies for an engine-owned run, so the only correct value
		// to write is this one.
		b.WriteString("\nThe coordination chainID for this run is **engine-assigned**: `")
		b.WriteString(chainPrefix)
		b.WriteString("`. Use this EXACT value as the `chainID` in every `delegate` message — do NOT invent your own. The engine owns this namespace; a chainID you supply is ignored in favour of it.\n")
	}

	b.WriteString("\n## Final Output\n\n")
	b.WriteString("After all members return their results, synthesise the findings into a final report. ")
	b.WriteString("Before delivering your final response to the user, you MUST write the complete report ")
	b.WriteString("to coordination_store key `" + chainPrefix + "/" + manifest.ID + "/output`. ")
	b.WriteString("This is mandatory — the report must be persisted, not just narrated. ")
	b.WriteString("That key is the inter-agent handoff for the publisher; the user-facing summary still goes in your reply. ")
	b.WriteString("While the swarm runs, keep reporting progress in this thread — do not detach into coordination-store polling or silent waiting loops.\n")

	return b.String()
}

// resolveSwarmMemberDetails looks up a swarm member id and returns its
// display Name and role text. The lookup honours the same precedence
// as swarm.Resolve: the agent registry wins, then the swarm registry.
// For agent members, returns (Name, Metadata.Role, true). For swarm
// members (meta-swarm's sub-swarms — `a-team`, `dev-swarm`,
// `planning-loop`, `board-room`), returns (Description, "swarm",
// true) so the lead-block renderer can append "(swarm)" as the kind
// marker and the model can tell at a glance that delegating to this
// member dispatches a whole sub-swarm rather than a single agent.
//
// The found flag is false only when the member resolves to neither
// registry, in which case the caller falls back to printing only the
// bare id.
//
// Expected:
//   - memberID is the swarm member's id; non-empty in normal use.
//
// Returns:
//   - name, role, true when either registry resolved the id. Role is
//     `"swarm"` literal for swarm-id matches so the renderer's parens-
//     suffix path picks up the kind marker.
//   - "", "", false otherwise.
//
// Side effects:
//   - None.
func (e *Engine) resolveSwarmMemberDetails(memberID string) (string, string, bool) {
	if memberID == "" {
		return "", "", false
	}
	if e.agentRegistry != nil {
		if manifest, ok := e.agentRegistry.Get(memberID); ok && manifest != nil {
			return manifest.Name, manifest.Metadata.Role, true
		}
		if manifest, ok := e.agentRegistry.GetByNameOrAlias(memberID); ok && manifest != nil {
			return manifest.Name, manifest.Metadata.Role, true
		}
	}
	// Sub-swarm member (meta-swarm pattern). The description is the
	// swarm manifest's human-readable blurb; the "swarm" kind marker
	// disambiguates the kind for the model so it knows delegate(memberID)
	// dispatches a whole sub-swarm, not an agent.
	if e.swarmRegistry != nil {
		if manifest, ok := e.swarmRegistry.Get(memberID); ok && manifest != nil {
			return manifest.Description, "swarm", true
		}
	}
	return "", "", false
}

// appendDelegationSectionsFor builds and appends delegation
// sections using the supplied manifest's allowlist. The ctx-bound
// build path calls this so each concurrent stream's prompt
// reflects its own manifest's delegation envelope.
//
// Expected:
//   - base is the current system prompt string.
//   - manifest is the manifest whose Delegation.DelegationAllowlist
//     drives the agent filtering.
//
// Returns:
//   - The base string with appended delegation sections.
//
// Side effects:
//   - None.
func (e *Engine) appendDelegationSectionsFor(base string, manifest agent.Manifest) string {
	if e.agentRegistry == nil {
		return base
	}

	agents := e.agentRegistry.List()

	allowlist := manifest.Delegation.DelegationAllowlist
	if len(allowlist) > 0 {
		agents = filterByAllowlist(agents, allowlist)
	}

	keyTriggers := buildKeyTriggersSection(agents)
	if keyTriggers != "" {
		base = base + "\n\n" + keyTriggers
	}

	toolSelection := buildToolSelectionSection(agents)
	if toolSelection != "" {
		base = base + "\n\n" + toolSelection
	}

	delegation := buildDelegationSection(agents)
	if delegation != "" {
		base = base + "\n\n" + delegation
	}

	if e.swarmRegistry != nil {
		swarmSection := buildSwarmSection(e.swarmRegistry)
		if swarmSection != "" {
			base = base + "\n\n" + swarmSection
		}
	}

	return base
}
